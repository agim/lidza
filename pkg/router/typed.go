package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/agim/lidza/pkg/validate"
)

// MaxBodyBytes is the largest JSON request body a typed handler accepts.
const MaxBodyBytes = 1 << 20

// None is the In type of a handler without a request body and the Out type
// of one that replies 204 No Content.
type None struct{}

// Request is what a typed handler receives.
type Request[In any] struct {
	// Body is the decoded JSON body; the zero value for None or when the
	// method carries no body.
	Body In
	// Raw is the underlying request, for headers, path values and query.
	Raw     *http.Request
	status  int
	header  http.Header
	cookies []*http.Cookie
}

// Header returns response headers to send with the reply.
func (r *Request[In]) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}

// SetCookie adds a cookie to the reply.
func (r *Request[In]) SetCookie(c *http.Cookie) { r.cookies = append(r.cookies, c) }

func (r *Request[In]) apply(w http.ResponseWriter) {
	for k, v := range r.header {
		w.Header()[k] = v
	}
	for _, c := range r.cookies {
		http.SetCookie(w, c)
	}
}

// Param returns a path parameter from the pattern, e.g. {id}.
func (r *Request[In]) Param(name string) string { return r.Raw.PathValue(name) }

// Query returns a query string parameter.
func (r *Request[In]) Query(name string) string { return r.Raw.URL.Query().Get(name) }

// Status sets the response status for a successful reply (default 200,
// or 204 when Out is None).
func (r *Request[In]) Status(code int) { r.status = code }

// Handler is a typed handler: JSON in, JSON out, errors mapped to status
// codes by Route.
type Handler[In, Out any] func(ctx context.Context, req *Request[In]) (Out, error)

// HTTPError is an error with a status code. Route sends {"error": Message}
// with that status; the message is what the client sees.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("%d %s", e.Status, e.Message) }

// Errorf builds an HTTPError.
func Errorf(status int, format string, a ...any) error {
	return &HTTPError{Status: status, Message: fmt.Sprintf(format, a...)}
}

// NotFound is a 404 with the given message.
func NotFound(what string) error {
	return &HTTPError{Status: http.StatusNotFound, Message: what + " not found"}
}

// Route registers a typed handler at a ServeMux pattern. The request body
// is decoded from JSON into In (up to MaxBodyBytes) for methods that carry
// one; when In implements validate.Validator the body is validated and a
// failure replies 422 with the field errors. Out is encoded as JSON with
// status 200 (or the status set on the request); None replies 204.
// Errors: *HTTPError sends its status, *validate.Errors sends 422, anything
// else sends 500 and is logged, its text never reaching the client.
func Route[In, Out any](r *Router, pattern string, h Handler[In, Out]) {
	_, noBody := any(*new(In)).(None)
	_, noReply := any(*new(Out)).(None)
	r.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
		typed := &Request[In]{Raw: req}
		if !noBody && hasBody(req) {
			if err := decodeBody(req, &typed.Body); err != nil {
				Error(w, http.StatusBadRequest, err.Error())
				return
			}
			if v, ok := any(typed.Body).(validate.Validator); ok {
				if err := v.Validate(); err != nil {
					writeError(w, err)
					return
				}
			}
		}
		out, err := h(req.Context(), typed)
		if err != nil {
			writeError(w, err)
			return
		}
		typed.apply(w)
		if noReply {
			if typed.status == 0 {
				typed.status = http.StatusNoContent
			}
			w.WriteHeader(typed.status)
			return
		}
		if typed.status == 0 {
			typed.status = http.StatusOK
		}
		JSON(w, typed.status, out)
	})
}

func hasBody(req *http.Request) bool {
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return req.ContentLength > 0
}

func decodeBody(req *http.Request, dst any) error {
	body := http.MaxBytesReader(nil, req.Body, MaxBodyBytes)
	dec := json.NewDecoder(body)
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("request body larger than %d bytes", MaxBodyBytes)
		}
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty; expected JSON")
		}
		return fmt.Errorf("invalid JSON body: %v", err)
	}
	if dec.More() {
		return errors.New("invalid JSON body: trailing data")
	}
	return nil
}

func writeError(w http.ResponseWriter, err error) {
	var httpErr *HTTPError
	var valErr *validate.Errors
	switch {
	case errors.As(err, &httpErr):
		Error(w, httpErr.Status, httpErr.Message)
	case errors.As(err, &valErr):
		JSON(w, http.StatusUnprocessableEntity, valErr)
	default:
		log.Printf("handler error: %v", err)
		Error(w, http.StatusInternalServerError, "internal error")
	}
}
