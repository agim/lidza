package lidzatest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// RecordEnv, when set to 1, makes recorders call the network and write
// fixtures; otherwise they replay.
const RecordEnv = "LIDZA_RECORD"

// FixtureDir holds recorded HTTP exchanges, relative to the project root.
const FixtureDir = "testdata/http"

// Exchange is one recorded request and response.
type Exchange struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Body    string      `json:"body,omitempty"`
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	Reply   string      `json:"reply"`
}

// Recorder is an http.RoundTripper that records outbound calls to a
// fixture file on the first run (LIDZA_RECORD=1) and replays them after,
// so tests run offline. Requests match by method, URL and body; secrets
// in Authorization, Cookie and X-Api-Key headers are never stored.
type Recorder struct {
	path   string
	record bool
	next   http.RoundTripper

	mu        sync.Mutex
	exchanges map[string]*Exchange
	order     []string
}

// NewRecorder loads or creates the fixture called name under
// testdata/http in the project root.
func NewRecorder(name string) (*Recorder, error) {
	root := projectRoot()
	if root == "" {
		root = "."
	}
	r := &Recorder{
		path:      filepath.Join(root, FixtureDir, name+".json"),
		record:    os.Getenv(RecordEnv) == "1",
		next:      http.DefaultTransport,
		exchanges: map[string]*Exchange{},
	}
	data, err := os.ReadFile(r.path)
	switch {
	case err == nil:
		var list []*Exchange
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("fixture %s: %w", r.path, err)
		}
		for _, e := range list {
			k := key(e.Method, e.URL, e.Body)
			r.exchanges[k] = e
			r.order = append(r.order, k)
		}
	case errors.Is(err, os.ErrNotExist) && !r.record:
		return nil, fmt.Errorf("no fixture %s: run the test once with %s=1 to record it", r.path, RecordEnv)
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	return r, nil
}

// RoundTrip implements http.RoundTripper.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	k := key(req.Method, req.URL.String(), string(body))
	if !r.record {
		r.mu.Lock()
		e, ok := r.exchanges[k]
		r.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("no recorded reply for %s %s in %s (run with %s=1 to record)", req.Method, req.URL, r.path, RecordEnv)
		}
		return e.response(req), nil
	}
	res, err := r.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	reply, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return nil, err
	}
	e := &Exchange{Method: req.Method, URL: req.URL.String(), Body: string(body), Status: res.StatusCode, Headers: safeHeaders(res.Header), Reply: string(reply)}
	r.mu.Lock()
	if _, seen := r.exchanges[k]; !seen {
		r.order = append(r.order, k)
	}
	r.exchanges[k] = e
	r.mu.Unlock()
	if err := r.save(); err != nil {
		return nil, err
	}
	return e.response(req), nil
}

func (e *Exchange) response(req *http.Request) *http.Response {
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", e.Status, http.StatusText(e.Status)),
		StatusCode:    e.Status,
		Header:        e.Headers.Clone(),
		Body:          io.NopCloser(strings.NewReader(e.Reply)),
		ContentLength: int64(len(e.Reply)),
		Request:       req,
		Proto:         "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}
}

func (r *Recorder) save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]*Exchange, 0, len(r.order))
	for _, k := range r.order {
		list = append(list, r.exchanges[k])
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(r.path, append(data, '\n'), 0o644)
}

var secretHeaders = map[string]bool{"Authorization": true, "Cookie": true, "Set-Cookie": true, "X-Api-Key": true, "Proxy-Authorization": true}

func safeHeaders(h http.Header) http.Header {
	out := http.Header{}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !secretHeaders[http.CanonicalHeaderKey(k)] {
			out[k] = h[k]
		}
	}
	return out
}

func key(method, url, body string) string {
	sum := sha256.Sum256([]byte(method + "\n" + url + "\n" + body))
	return hex.EncodeToString(sum[:8])
}
