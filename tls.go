package lidza

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// TLS from the environment: with LIDZA_TLS_DOMAINS set the binary serves
// HTTPS itself, with certificates from Let's Encrypt (ACME) obtained and
// renewed by golang.org/x/crypto/acme/autocert, and answers plain HTTP
// only to redirect to HTTPS and to prove domain ownership. No proxy is
// needed in front. The certificates live in Postgres through the db
// pack, so every node of the app shares them, as the stateless rule
// requires; a single node without the db pack names a directory in
// LIDZA_TLS_CACHE_DIR instead.
const (
	// EnvTLSDomains lists the domains to serve, comma-separated; the first
	// is the app's public name (APP_URL when unset).
	EnvTLSDomains = "LIDZA_TLS_DOMAINS"
	// EnvTLSEmail is the ACME account contact (expiry warnings).
	EnvTLSEmail = "LIDZA_TLS_EMAIL"
	// EnvTLSAddr and EnvTLSHTTPAddr are the HTTPS and HTTP listen
	// addresses, :443 and :80 by default.
	EnvTLSAddr     = "LIDZA_TLS_ADDR"
	EnvTLSHTTPAddr = "LIDZA_TLS_HTTP_ADDR"
	// EnvTLSCacheDir stores certificates on disk instead of Postgres:
	// one node only.
	EnvTLSCacheDir = "LIDZA_TLS_CACHE_DIR"
	// EnvTLSDirectory is the ACME directory URL; Let's Encrypt by
	// default, its staging directory for rehearsals.
	EnvTLSDirectory = "LIDZA_TLS_DIRECTORY"
)

type tlsSettings struct {
	domains   []string
	email     string
	addr      string
	httpAddr  string
	cacheDir  string
	directory string
}

// tlsFromEnv reads the settings; nil without LIDZA_TLS_DOMAINS.
func tlsFromEnv() (*tlsSettings, error) {
	raw := strings.TrimSpace(os.Getenv(EnvTLSDomains))
	if raw == "" {
		return nil, nil
	}
	var domains []string
	for _, d := range strings.Split(raw, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if strings.ContainsAny(d, "/:* ") || !strings.Contains(d, ".") {
			return nil, fmt.Errorf("%s: %q is not a domain name (a.example.com, no scheme, port or wildcard)", EnvTLSDomains, d)
		}
		domains = append(domains, d)
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("%s is set but names no domain", EnvTLSDomains)
	}
	return &tlsSettings{
		domains:   domains,
		email:     os.Getenv(EnvTLSEmail),
		addr:      envOr(EnvTLSAddr, ":443"),
		httpAddr:  envOr(EnvTLSHTTPAddr, ":80"),
		cacheDir:  os.Getenv(EnvTLSCacheDir),
		directory: os.Getenv(EnvTLSDirectory),
	}, nil
}

// PublicURL is https://<first domain>.
func (t *tlsSettings) PublicURL() string { return "https://" + t.domains[0] }

// deriveEnv sets what follows from serving TLS, for the packs that read
// the environment when they start: APP_URL (links in emails) and
// AUTH_COOKIE_SECURE, unless already set.
func (t *tlsSettings) deriveEnv() {
	for k, v := range map[string]string{"APP_URL": t.PublicURL(), "AUTH_COOKIE_SECURE": "true"} {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

// cache picks where certificates live: the db pack's store when it is
// running (shared by every node), else the directory named in
// LIDZA_TLS_CACHE_DIR, else an error that says what is missing.
func (t *tlsSettings) cache(s *Services) (autocert.Cache, error) {
	if s != nil {
		if v, ok := s.Lookup(typeOf[autocert.Cache]()); ok {
			return v.(autocert.Cache), nil
		}
	}
	if t.cacheDir != "" {
		if err := os.MkdirAll(t.cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("%s: %w", EnvTLSCacheDir, err)
		}
		return autocert.DirCache(t.cacheDir), nil
	}
	return nil, fmt.Errorf("%s needs somewhere to keep the certificates: the db pack (`lidza pack add db`; stored in Postgres, shared by every node) or %s for a single node", EnvTLSDomains, EnvTLSCacheDir)
}

// manager builds the ACME manager.
func (t *tlsSettings) manager(cache autocert.Cache) *autocert.Manager {
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(t.domains...),
		Cache:      cache,
		Email:      t.email,
	}
	if t.directory != "" {
		m.Client = &acme.Client{DirectoryURL: t.directory, HTTPClient: acmeHTTPClient}
	}
	return m
}

// tlsListening, when set by a test, receives the bound HTTPS and HTTP
// addresses, so the test can listen on port 0 and still find the server.
var tlsListening func(https, http string)

// acmeHTTPClient is the client the ACME manager uses for the directory;
// nil is the default. A test against a local test authority sets one
// that trusts its certificate.
var acmeHTTPClient *http.Client

// redirectToHTTPS answers every plain-HTTP request that is not an ACME
// challenge with a permanent redirect to the same path over HTTPS.
func redirectToHTTPS(defaultHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == "" {
			host = defaultHost
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// serveTLS runs the HTTPS server and the HTTP redirector until ctx ends.
func serveTLS(ctx context.Context, booted *Booted, t *tlsSettings, appName string, onReady func(), log *slog.Logger) error {
	cache, err := t.cache(booted.Services)
	if err != nil {
		booted.Close(ctx)
		return err
	}
	m := t.manager(cache)
	tlsLn, err := net.Listen("tcp", t.addr)
	if err != nil {
		booted.Close(ctx)
		return fmt.Errorf("listen %s for HTTPS: %w (port 443 needs root or CAP_NET_BIND_SERVICE; deploy/%s.service grants it)", t.addr, err, appName)
	}
	httpLn, err := net.Listen("tcp", t.httpAddr)
	if err != nil {
		tlsLn.Close()
		booted.Close(ctx)
		return fmt.Errorf("listen %s for HTTP: %w", t.httpAddr, err)
	}
	errLog := slog.NewLogLogger(log.Handler(), slog.LevelWarn)
	srv := &http.Server{
		Handler:           booted.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          errLog,
	}
	redirect := &http.Server{
		Handler:           m.HTTPHandler(redirectToHTTPS(t.domains[0])),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       time.Minute,
		ErrorLog:          errLog,
	}
	log.Info("listening", "app", appName, "addr", t.PublicURL(), "https", tlsLn.Addr().String(), "http", httpLn.Addr().String(), "domains", t.domains, "mode", "production")
	if tlsListening != nil {
		tlsListening(tlsLn.Addr().String(), httpLn.Addr().String())
	}
	if onReady != nil {
		onReady()
	}
	errc := make(chan error, 2)
	go func() { errc <- srv.Serve(tls.NewListener(tlsLn, m.TLSConfig())) }()
	go func() { errc <- redirect.Serve(httpLn) }()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			redirect.Close()
			srv.Close()
			booted.Close(ctx)
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	redirect.Shutdown(shutdown)
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		booted.Close(shutdown)
		return err
	}
	return booted.Close(shutdown)
}
