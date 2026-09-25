package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza"
)

// send posts v as JSON with the headers and returns the response; a
// status outside 2xx is an *Error with the body, closed. The caller
// closes a successful response.
func send(ctx context.Context, provider, url string, headers map[string]string, v any) (*http.Response, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, val := range headers {
		req.Header.Set(k, val)
	}
	client := lidza.HTTPClient(ctx)
	client.Timeout = 0 // the context carries the attempt's deadline
	res, err := client.Do(req)
	if err != nil {
		return nil, &Error{Provider: provider, Err: err}
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 16<<10))
		res.Body.Close()
		e := &Error{Provider: provider, Status: res.StatusCode, Body: strings.TrimSpace(string(body))}
		if ra := res.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil {
				e.RetryAfter = time.Duration(secs) * time.Second
			}
		}
		return nil, e
	}
	return res, nil
}

// postJSON posts and decodes the reply into out.
func postJSON(ctx context.Context, provider, url string, headers map[string]string, v, out any) error {
	res, err := send(ctx, provider, url, headers, v)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return &Error{Provider: provider, Err: err}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("llm: %s: decode reply: %w", provider, err)
	}
	return nil
}

// readSSE reads a text/event-stream, calling fn with each event's data
// (the lines joined) until the stream ends, fn returns an error, or a
// "[DONE]" sentinel arrives.
func readSSE(r io.Reader, fn func(data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		d := strings.Join(data, "\n")
		data = data[:0]
		if d == "[DONE]" {
			return errDone
		}
		return fn(d)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				if errors.Is(err, errDone) {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := flush(); err != nil && !errors.Is(err, errDone) {
		return err
	}
	return sc.Err()
}

var errDone = errors.New("done")

// readLines reads newline-delimited JSON objects (Ollama's stream).
func readLines(r io.Reader, fn func(line []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := fn(append([]byte(nil), line...)); err != nil {
			return err
		}
	}
	return sc.Err()
}

// callID makes an id for providers that do not give tool calls one.
func callID(n int) string { return fmt.Sprintf("call_%d", n) }

// rawObject makes sure a tool input is a JSON object, never empty.
func rawObject(v json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(v)) == 0 || string(v) == "null" {
		return json.RawMessage("{}")
	}
	return v
}
