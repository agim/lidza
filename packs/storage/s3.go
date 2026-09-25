package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza"
)

// s3 speaks the S3 REST API with AWS Signature Version 4: PUT, GET, HEAD
// and DELETE on objects, ListObjectsV2, and presigned URLs. Any
// S3-compatible service works; the bucket goes in the host (AWS) or the
// path (everything else, or STORAGE_PATH_STYLE=true).
type s3 struct {
	endpoint  *url.URL
	bucket    string
	region    string
	access    string
	secret    string
	pathStyle bool
	public    string
	now       func() time.Time
}

func newS3(cfg Config) (*s3, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("storage: STORAGE_ENDPOINT %q is not a URL like https://s3.amazonaws.com", cfg.Endpoint)
	}
	pathStyle := !strings.HasSuffix(u.Host, "amazonaws.com")
	if cfg.PathStyle != nil {
		pathStyle = *cfg.PathStyle
	}
	return &s3{endpoint: u, bucket: cfg.Bucket, region: cfg.Region, access: cfg.AccessKey, secret: cfg.SecretKey, pathStyle: pathStyle, public: strings.TrimRight(cfg.PublicURL, "/"), now: time.Now}, nil
}

func (p *s3) Name() string { return "s3" }

// objectURL is the object's address: host or path style.
func (p *s3) objectURL(key string) *url.URL {
	u := *p.endpoint
	if p.pathStyle {
		u.Path = "/" + p.bucket
		if key != "" {
			u.Path += "/" + key
		}
	} else {
		u.Host = p.bucket + "." + u.Host
		u.Path = "/" + key
	}
	u.RawPath = encodePath(u.Path)
	return &u
}

// encodePath escapes each segment as SigV4 wants (RFC 3986 unreserved
// characters kept, everything else percent-encoded, "/" kept).
func encodePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = awsEscape(s)
	}
	return strings.Join(parts, "/")
}

func awsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

const unsignedPayload = "UNSIGNED-PAYLOAD"

// sign adds the SigV4 authorization to req; payloadHash is hex sha256 of
// the body, or unsignedPayload.
func (p *s3) sign(req *http.Request, payloadHash string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signed, canonicalHeaders := canonicalHeaders(req)
	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(req.URL.Query()),
		canonicalHeaders,
		signed,
		payloadHash,
	}, "\n")
	scope := date + "/" + p.region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA256([]byte(canonical))
	sig := hex.EncodeToString(hmacSHA256(p.signingKey(date), []byte(toSign)))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+p.access+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

// presign returns req's URL with the signature in the query, valid for
// ttl.
func (p *s3) presign(req *http.Request, ttl time.Duration, now time.Time) string {
	amzDate := now.UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	scope := date + "/" + p.region + "/s3/aws4_request"
	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", p.access+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(ttl.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(q),
		"host:" + req.URL.Host + "\n",
		"host",
		unsignedPayload,
	}, "\n")
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA256([]byte(canonical))
	q.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA256(p.signingKey(date), []byte(toSign))))
	u := *req.URL
	u.RawQuery = canonicalQuery(q)
	return u.String()
}

func (p *s3) signingKey(date string) []byte {
	k := hmacSHA256([]byte("AWS4"+p.secret), []byte(date))
	k = hmacSHA256(k, []byte(p.region))
	k = hmacSHA256(k, []byte("s3"))
	return hmacSHA256(k, []byte("aws4_request"))
}

func canonicalHeaders(req *http.Request) (signed, canonical string) {
	var names []string
	for name := range req.Header {
		lower := strings.ToLower(name)
		if lower == "host" || strings.HasPrefix(lower, "x-amz-") || lower == "content-type" || lower == "cache-control" {
			names = append(names, lower)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		v := req.Header.Get(n)
		if n == "host" {
			v = req.URL.Host
		}
		b.WriteString(n + ":" + strings.Join(strings.Fields(v), " ") + "\n")
	}
	return strings.Join(names, ";"), b.String()
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// do sends a signed request. A status outside 2xx (404 aside, which is
// ErrNotFound) is an error with the service's message.
func (p *s3) do(ctx context.Context, method string, u *url.URL, body []byte, headers map[string]string) (*http.Response, error) {
	var reader io.Reader
	payloadHash := hexSHA256(nil)
	if body != nil {
		reader = bytes.NewReader(body)
		payloadHash = hexSHA256(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	p.sign(req, payloadHash, p.now())
	client := lidza.HTTPClient(ctx)
	client.Timeout = 0
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: %s: %w", u.Host, err)
	}
	if res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		return nil, ErrNotFound
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
		res.Body.Close()
		var e struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		}
		xml.Unmarshal(msg, &e)
		if e.Code != "" {
			return nil, fmt.Errorf("storage: %s %s: %d %s: %s", method, u.Path, res.StatusCode, e.Code, e.Message)
		}
		return nil, fmt.Errorf("storage: %s %s: %d %s", method, u.Path, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return res, nil
}

func (p *s3) Put(ctx context.Context, key string, data []byte, opt PutOptions) (Object, error) {
	res, err := p.do(ctx, http.MethodPut, p.objectURL(key), data, map[string]string{"Content-Type": opt.ContentType, "Cache-Control": opt.CacheControl})
	if err != nil {
		return Object{}, err
	}
	res.Body.Close()
	return Object{Key: key, Size: int64(len(data)), ContentType: opt.ContentType, ModifiedAt: p.now().UTC(), ETag: strings.Trim(res.Header.Get("ETag"), `"`)}, nil
}

func (p *s3) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	res, err := p.do(ctx, http.MethodGet, p.objectURL(key), nil, nil)
	if err != nil {
		return nil, Object{}, err
	}
	return res.Body, objectFromHeaders(key, res.Header), nil
}

func (p *s3) Stat(ctx context.Context, key string) (Object, error) {
	res, err := p.do(ctx, http.MethodHead, p.objectURL(key), nil, nil)
	if err != nil {
		return Object{}, err
	}
	res.Body.Close()
	return objectFromHeaders(key, res.Header), nil
}

func objectFromHeaders(key string, h http.Header) Object {
	size, _ := strconv.ParseInt(h.Get("Content-Length"), 10, 64)
	mod, _ := http.ParseTime(h.Get("Last-Modified"))
	return Object{Key: key, Size: size, ContentType: h.Get("Content-Type"), ModifiedAt: mod.UTC(), ETag: strings.Trim(h.Get("ETag"), `"`)}
}

func (p *s3) Delete(ctx context.Context, key string) error {
	res, err := p.do(ctx, http.MethodDelete, p.objectURL(key), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func (p *s3) List(ctx context.Context, prefix string, limit int) ([]Object, error) {
	var out []Object
	token := ""
	for {
		u := p.objectURL("")
		q := url.Values{"list-type": {"2"}, "max-keys": {strconv.Itoa(min(limit-len(out), 1000))}}
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		u.RawQuery = q.Encode()
		res, err := p.do(ctx, http.MethodGet, u, nil, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Contents []struct {
				Key          string `xml:"Key"`
				Size         int64  `xml:"Size"`
				LastModified string `xml:"LastModified"`
				ETag         string `xml:"ETag"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(res.Body).Decode(&page)
		res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("storage: list: %w", err)
		}
		for _, c := range page.Contents {
			mod, _ := time.Parse(time.RFC3339, c.LastModified)
			out = append(out, Object{Key: c.Key, Size: c.Size, ModifiedAt: mod.UTC(), ETag: strings.Trim(c.ETag, `"`)})
		}
		if !page.IsTruncated || page.NextContinuationToken == "" || len(out) >= limit {
			break
		}
		token = page.NextContinuationToken
	}
	return out, nil
}

func (p *s3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := http.NewRequest(http.MethodGet, p.objectURL(key).String(), nil)
	if err != nil {
		return "", err
	}
	return p.presign(req, ttl, p.now()), nil
}

func (p *s3) PresignPut(ctx context.Context, key string, ttl time.Duration, contentType string) (string, error) {
	req, err := http.NewRequest(http.MethodPut, p.objectURL(key).String(), nil)
	if err != nil {
		return "", err
	}
	return p.presign(req, ttl, p.now()), nil
}

func (p *s3) URL(key string) string {
	if p.public != "" {
		return p.public + "/" + key
	}
	return ""
}

// createBucket makes the bucket when the service allows it (MinIO, a
// development account); a bucket that exists is fine.
func (p *s3) createBucket(ctx context.Context) error {
	u := p.objectURL("")
	if !p.pathStyle {
		u.Path = "/"
	}
	res, err := p.do(ctx, http.MethodPut, u, []byte{}, nil)
	if err != nil && (strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") || strings.Contains(err.Error(), "BucketAlreadyExists")) {
		return nil
	}
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// EnsureBucket creates the S3 bucket when it does not exist (MinIO and
// development accounts); the local provider needs nothing.
func (s *Storage) EnsureBucket(ctx context.Context) error {
	if p, ok := s.providerNow().(*s3); ok {
		return p.createBucket(ctx)
	}
	return nil
}
