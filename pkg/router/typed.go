package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"

	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/report"
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

// SetCookie adds a cookie to the reply. On a request that came over
// HTTPS it is sent Secure even when c.Secure is false.
func (r *Request[In]) SetCookie(c *http.Cookie) { r.cookies = append(r.cookies, c) }

func (r *Request[In]) apply(w http.ResponseWriter) {
	for k, v := range r.header {
		w.Header()[k] = v
	}
	for _, c := range r.cookies {
		if r.Raw != nil && r.Raw.TLS != nil && !c.Secure {
			sc := *c
			sc.Secure = true
			c = &sc
		}
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
	// Code, when set, names the error for programs ("wrong_password"),
	// so a client tells apart two errors with one status without reading
	// the message. It is sent as "code" beside "error".
	Code string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("%d %s", e.Status, e.Message) }

// Errorf builds an HTTPError.
func Errorf(status int, format string, a ...any) error {
	return &HTTPError{Status: status, Message: fmt.Sprintf(format, a...)}
}

// ErrorCode is Errorf with a code the client can switch on: the reply is
// {"error": message, "code": code}.
func ErrorCode(status int, code, format string, a ...any) error {
	return &HTTPError{Status: status, Code: code, Message: fmt.Sprintf(format, a...)}
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
//
// With In = File the route is an upload: the body is the file itself,
// not JSON, handed to the handler unread (see File).
func Route[In, Out any](r *Router, pattern string, h Handler[In, Out], mw ...middleware.Middleware) {
	_, noBody := any(*new(In)).(None)
	_, upload := any(*new(In)).(File)
	_, noReply := any(*new(Out)).(None)
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		typed := &Request[In]{Raw: req}
		if upload {
			f, err := readFile(w, req)
			if err != nil {
				writeError(w, req, err)
				return
			}
			typed.Body = any(f).(In)
		} else if !noBody && hasBody(req) {
			if err := decodeBody(req, &typed.Body); err != nil {
				Error(w, http.StatusBadRequest, err.Error())
				return
			}
			if v, ok := any(typed.Body).(validate.Validator); ok {
				if err := v.Validate(); err != nil {
					writeError(w, req, err)
					return
				}
			}
		}
		out, err := h(req.Context(), typed)
		if err != nil {
			writeError(w, req, err)
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
	r.Handle(pattern, middleware.Chain(handler, mw...))
}

// MaxUploadBytes is the largest body an upload route (In = File) accepts
// unless UploadLimit sets another.
const MaxUploadBytes = 32 << 20

// File is the In type of an upload route: the request body is the file
// itself, sent raw (not JSON, not multipart form data). The generated
// client takes a File or Blob for it, with the path parameters:
//
//	router.Route(r, "PUT /api/v1/posts/{id}/image", uploadImage, router.UploadLimit(10<<20))
//
//	func uploadImage(ctx context.Context, req *router.Request[router.File]) (storage.Object, error) {
//		return storage.From(ctx).Put(ctx, "posts/"+req.Param("id")+"/image", req.Body.Body,
//			storage.PutOptions{ContentType: req.Body.ContentType})
//	}
//
//	await api.uploadImage({ id }, file, { onProgress: (sent, total) => ... })
//
// A body over the limit replies 413: at once when Content-Length says
// so, or when the handler returns the read error.
type File struct {
	// Body reads the uploaded bytes, bounded by the route's limit.
	Body io.Reader
	// ContentType is the request's Content-Type, application/octet-stream
	// when it has none.
	ContentType string
	// Name is the file name the client sent in Content-Disposition (the
	// generated client sends a File's name); empty otherwise. It is the
	// client's word: never use it as a path or a storage key.
	Name string
	// Size is the Content-Length, or -1 when the client did not send one.
	Size int64
}

type uploadLimitKey struct{}

// UploadLimit sets the largest body, in bytes, the upload routes it wraps
// accept; MaxUploadBytes otherwise.
func UploadLimit(n int64) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), uploadLimitKey{}, n)))
		})
	}
}

func readFile(w http.ResponseWriter, req *http.Request) (File, error) {
	limit := int64(MaxUploadBytes)
	if n, ok := req.Context().Value(uploadLimitKey{}).(int64); ok && n > 0 {
		limit = n
	}
	if req.ContentLength > limit {
		return File{}, &http.MaxBytesError{Limit: limit}
	}
	f := File{Body: http.MaxBytesReader(w, req.Body, limit), ContentType: req.Header.Get("Content-Type"), Size: req.ContentLength}
	if f.ContentType == "" {
		f.ContentType = "application/octet-stream"
	}
	if cd := req.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			f.Name = params["filename"]
		}
	}
	return f, nil
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

// WriteError answers a raw handler's error the way typed handlers do:
// an HTTPError with its status and message, validation errors as a 422
// with fields, anything else as a 500 whose text stays on the server.
func WriteError(w http.ResponseWriter, req *http.Request, err error) { writeError(w, req, err) }

func writeError(w http.ResponseWriter, req *http.Request, err error) {
	status, body := errorReply(req, err)
	JSON(w, status, body)
}

// errorReply maps a handler's error to the status and body the client
// gets, logging and reporting the ones whose text stays on the server.
func errorReply(req *http.Request, err error) (int, any) {
	var httpErr *HTTPError
	var valErr *validate.Errors
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("request body larger than %d bytes", tooLarge.Limit)}
	case errors.As(err, &httpErr):
		if httpErr.Code != "" {
			return httpErr.Status, map[string]string{"error": httpErr.Message, "code": httpErr.Code}
		}
		return httpErr.Status, map[string]string{"error": httpErr.Message}
	case errors.As(err, &valErr):
		return http.StatusUnprocessableEntity, valErr
	default:
		log.Printf("handler error: %v", err)
		report.Capture(req.Context(), report.Error{
			Source: "server", Message: err.Error(), Route: req.Pattern, Method: req.Method,
			URL: req.URL.RequestURI(), RequestID: middleware.GetRequestID(req.Context()),
		})
		return http.StatusInternalServerError, map[string]string{"error": "internal error"}
	}
}
