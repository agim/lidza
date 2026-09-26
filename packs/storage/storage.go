// Package storage is the file storage pack: one Put, Get, Stat, List,
// Delete and presigned URLs over any S3-compatible service (AWS S3,
// MinIO, Cloudflare R2, Backblaze B2, Wasabi, DigitalOcean Spaces) spoken
// directly with Signature V4 over HTTP, and a local provider that keeps
// files in a directory for development and tests. Handlers call
// storage.From(ctx); the provider and credentials come from .env and the
// credentials store (STORAGE_*).
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
)

// Config is read from .env and the credentials at start.
type Config struct {
	// Provider is local (default: files under Dir), s3 (Amazon S3, or
	// any S3-compatible service at Endpoint), or a named S3-compatible
	// service whose address follows from Region or AccountID: r2
	// (Cloudflare R2), spaces (DigitalOcean Spaces), b2 (Backblaze B2),
	// gcs (Google Cloud Storage with HMAC keys), minio (MinIO, RustFS or
	// another server at Endpoint).
	Provider string `env:"STORAGE_PROVIDER" default:"local"`
	// AccountID is the Cloudflare account of an R2 bucket.
	AccountID string `env:"STORAGE_ACCOUNT_ID"`
	// Dir holds the files of the local provider.
	Dir string `env:"STORAGE_DIR" default:"storage"`
	// Bucket is the S3 bucket.
	Bucket string `env:"STORAGE_BUCKET"`
	// Endpoint is the service: https://s3.amazonaws.com (default),
	// https://<account>.r2.cloudflarestorage.com, http://127.0.0.1:9000
	// for MinIO.
	Endpoint string `env:"STORAGE_ENDPOINT" default:"https://s3.amazonaws.com"`
	// Region signs the requests; us-east-1 by default (R2 and MinIO
	// accept auto or us-east-1).
	Region string `env:"STORAGE_REGION" default:"us-east-1"`
	// AccessKey and SecretKey authenticate; keep them in the credentials.
	AccessKey string `env:"STORAGE_ACCESS_KEY"`
	SecretKey string `env:"STORAGE_SECRET_KEY"`
	// PathStyle puts the bucket in the path (http://host/bucket/key)
	// instead of the host; on by default for every endpoint but AWS.
	PathStyle *bool `env:"STORAGE_PATH_STYLE"`
	// PublicURL is where objects are readable without signing (a CDN, a
	// public bucket); URL returns it plus the key.
	PublicURL string `env:"STORAGE_PUBLIC_URL"`
	// MaxSize bounds one Put.
	MaxSize int64 `env:"STORAGE_MAX_SIZE" default:"104857600"`
	// Timeout bounds one operation.
	Timeout time.Duration `env:"STORAGE_TIMEOUT" default:"60s"`
}

// Object describes a stored file.
type Object struct {
	Key         string    `json:"key"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	ModifiedAt  time.Time `json:"modifiedAt"`
	ETag        string    `json:"etag,omitempty"`
}

// PutOptions set what Put stores with the bytes.
type PutOptions struct {
	// ContentType is detected from the bytes and the key when empty.
	ContentType string
	// CacheControl is sent to browsers by public buckets and presigned
	// reads ("public, max-age=31536000" for immutable files).
	CacheControl string
}

// ErrNotFound is returned for a key that is not there.
var ErrNotFound = errors.New("storage: not found")

// Provider speaks one backend.
type Provider interface {
	Name() string
	Put(ctx context.Context, key string, data []byte, opt PutOptions) (Object, error)
	Get(ctx context.Context, key string) (io.ReadCloser, Object, error)
	Stat(ctx context.Context, key string) (Object, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string, limit int) ([]Object, error)
	// PresignGet and PresignPut return URLs that grant the operation
	// until ttl passes.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	PresignPut(ctx context.Context, key string, ttl time.Duration, contentType string) (string, error)
	// URL returns where the object is read without signing, "" when the
	// backend has no public address.
	URL(key string) string
}

// Providers lists the provider names.
var Providers = []string{"local", "s3", "r2", "spaces", "b2", "gcs", "minio"}

// defaultEndpoint is Endpoint's default, which the named services
// replace with their own address.
const defaultEndpoint = "https://s3.amazonaws.com"

// resolve fills Endpoint, Region and path style for a named service.
// An Endpoint set to something other than the default wins.
func resolve(cfg Config) (Config, error) {
	custom := cfg.Endpoint != "" && cfg.Endpoint != defaultEndpoint
	set := func(endpoint, region string) {
		if !custom {
			cfg.Endpoint = endpoint
		}
		if region != "" {
			cfg.Region = region
		}
	}
	switch cfg.Provider {
	case "s3":
		// Amazon S3 outside us-east-1 answers on its regional address.
		if !custom && cfg.Region != "" && cfg.Region != "us-east-1" {
			cfg.Endpoint = "https://s3." + cfg.Region + ".amazonaws.com"
		}
	case "r2":
		if cfg.AccountID == "" && !custom {
			return cfg, errors.New("storage: provider r2 needs STORAGE_ACCOUNT_ID, the Cloudflare account id")
		}
		set("https://"+cfg.AccountID+".r2.cloudflarestorage.com", "auto")
	case "spaces":
		if cfg.Region == "" || cfg.Region == "us-east-1" {
			return cfg, errors.New("storage: provider spaces needs STORAGE_REGION, the Spaces region (nyc3, ams3, fra1, sgp1, ...)")
		}
		set("https://"+cfg.Region+".digitaloceanspaces.com", "")
	case "b2":
		if cfg.Region == "" || cfg.Region == "us-east-1" {
			return cfg, errors.New("storage: provider b2 needs STORAGE_REGION, the bucket's region (us-west-004, eu-central-003, ...)")
		}
		set("https://s3."+cfg.Region+".backblazeb2.com", "")
	case "gcs":
		set("https://storage.googleapis.com", "auto")
	case "minio":
		if !custom {
			return cfg, errors.New("storage: provider minio needs STORAGE_ENDPOINT, the server's address (http://127.0.0.1:9000)")
		}
		if cfg.PathStyle == nil {
			on := true
			cfg.PathStyle = &on
		}
	}
	return cfg, nil
}

// Storage is the running pack.
type Storage struct {
	cfg      Config
	log      *slog.Logger
	provider Provider
	mu       sync.RWMutex
}

// Pack returns the pack for packs.go.
func Pack() lidza.Pack { return &Storage{log: slog.Default()} }

// New builds the pack from a configuration (tests; Start does it from
// .env and the credentials).
func New(cfg Config) (*Storage, error) {
	s := &Storage{cfg: cfg, log: slog.Default()}
	s.defaults()
	p, err := newProvider(s.cfg)
	if err != nil {
		return nil, err
	}
	s.provider = p
	return s, nil
}

func (s *Storage) defaults() {
	if s.cfg.Provider == "" {
		s.cfg.Provider = "local"
	}
	if s.cfg.Dir == "" {
		s.cfg.Dir = "storage"
	}
	if s.cfg.Endpoint == "" {
		s.cfg.Endpoint = defaultEndpoint
	}
	if s.cfg.Region == "" {
		s.cfg.Region = "us-east-1"
	}
	if s.cfg.MaxSize <= 0 {
		s.cfg.MaxSize = 100 << 20
	}
	if s.cfg.Timeout <= 0 {
		s.cfg.Timeout = time.Minute
	}
}

func newProvider(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "local":
		return newLocal(cfg.Dir, cfg.PublicURL)
	case "s3", "r2", "spaces", "b2", "gcs", "minio":
		if cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
			return nil, fmt.Errorf("storage: provider %s needs STORAGE_BUCKET, STORAGE_ACCESS_KEY and STORAGE_SECRET_KEY (keys in the credentials: lidza credentials set)", cfg.Provider)
		}
		resolved, err := resolve(cfg)
		if err != nil {
			return nil, err
		}
		return newS3(resolved)
	}
	return nil, fmt.Errorf("storage: unknown provider %q (one of %s)", cfg.Provider, strings.Join(Providers, ", "))
}

// From returns the pack from a handler's, job's or tool's context.
func From(ctx context.Context) *Storage { return lidza.Service[*Storage](ctx) }

// Name implements lidza.Pack.
func (s *Storage) Name() string { return "lidza/storage" }

// Start reads the configuration and provides the pack.
func (s *Storage) Start(ctx context.Context, svc *lidza.Services) error {
	var cfg Config
	if err := env.Load(".", &cfg); err != nil {
		return err
	}
	built, err := New(cfg)
	if err != nil {
		return err
	}
	s.cfg, s.provider = built.cfg, built.provider
	lidza.Provide(svc, s)
	return nil
}

// Stop implements lidza.Pack.
func (s *Storage) Stop(context.Context) error { return nil }

// Reconfigure reads the configuration again and switches the provider:
// what the admin pages call after a storage setting is saved.
func (s *Storage) Reconfigure(ctx context.Context) error {
	var cfg Config
	if err := env.Load(".", &cfg); err != nil {
		return err
	}
	built, err := New(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg, s.provider = built.cfg, built.provider
	s.mu.Unlock()
	s.log.Info("storage: reconfigured", "provider", s.cfg.Provider, "bucket", s.cfg.Bucket)
	return nil
}

func (s *Storage) providerNow() Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.provider
}

// Provider returns the configured provider's name.
func (s *Storage) Provider() string { return s.cfg.Provider }

// Put stores the reader's bytes under key (a path like
// "avatars/<id>.png"; no leading slash, no ".." segments) and returns
// the object. The bytes are read into memory up to STORAGE_MAX_SIZE.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, opt PutOptions) (Object, error) {
	if err := checkKey(key); err != nil {
		return Object{}, err
	}
	data, err := io.ReadAll(io.LimitReader(r, s.cfg.MaxSize+1))
	if err != nil {
		return Object{}, err
	}
	if int64(len(data)) > s.cfg.MaxSize {
		return Object{}, fmt.Errorf("storage: %s is larger than STORAGE_MAX_SIZE (%d bytes)", key, s.cfg.MaxSize)
	}
	if opt.ContentType == "" {
		opt.ContentType = detectContentType(key, data)
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	start := time.Now()
	obj, err := s.providerNow().Put(ctx, key, data, opt)
	if err != nil {
		return Object{}, err
	}
	lidza.Log(ctx).Info("storage put", "provider", s.cfg.Provider, "key", key, "bytes", len(data), "ms", time.Since(start).Milliseconds())
	return obj, nil
}

// Get opens the object for reading; the caller closes it. ErrNotFound
// for a missing key.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	if err := checkKey(key); err != nil {
		return nil, Object{}, err
	}
	return s.providerNow().Get(ctx, key)
}

// Stat describes the object without reading it.
func (s *Storage) Stat(ctx context.Context, key string) (Object, error) {
	if err := checkKey(key); err != nil {
		return Object{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	return s.providerNow().Stat(ctx, key)
}

// Delete removes the object; a missing key is not an error.
func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	return s.providerNow().Delete(ctx, key)
}

// List returns up to limit objects under prefix, by key.
func (s *Storage) List(ctx context.Context, prefix string, limit int) ([]Object, error) {
	if limit <= 0 {
		limit = 1000
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	return s.providerNow().List(ctx, strings.TrimPrefix(prefix, "/"), limit)
}

// PresignGet returns a URL that reads the object until ttl passes: hand
// it to a browser instead of streaming through the app.
func (s *Storage) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	return s.providerNow().PresignGet(ctx, key, ttl)
}

// PresignPut returns a URL a browser can PUT the file to directly, with
// that content type, until ttl passes.
func (s *Storage) PresignPut(ctx context.Context, key string, ttl time.Duration, contentType string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	return s.providerNow().PresignPut(ctx, key, ttl, contentType)
}

// URL returns the public address of the object (STORAGE_PUBLIC_URL plus
// the key, or the bucket's own address), "" when there is none: then
// use PresignGet or serve it through a handler.
func (s *Storage) URL(key string) string { return s.providerNow().URL(key) }

// checkKey refuses keys that would escape a prefix or a directory.
func checkKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || path.Clean("/"+key) != "/"+key || strings.HasSuffix(key, "/") {
		return fmt.Errorf("storage: invalid key %q (a path like \"avatars/<id>.png\": no leading slash, no . or .. segments)", key)
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("storage: invalid key %q (control character)", key)
		}
	}
	return nil
}

// detectContentType uses the key's extension, then the bytes.
func detectContentType(key string, data []byte) string {
	if ct := mimeByExtension(path.Ext(key)); ct != "" {
		return ct
	}
	return http.DetectContentType(data)
}
