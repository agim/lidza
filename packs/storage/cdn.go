package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// purger empties a CDN's cache for paths in the bucket ("a/b.png",
// "a/*").
type purger interface {
	purge(ctx context.Context, paths []string) error
}

// CDNs lists the STORAGE_CDN values.
var CDNs = []string{"digitalocean"}

func newPurger(cfg Config) (purger, error) {
	switch cfg.CDN {
	case "":
		return nil, nil
	case "digitalocean":
		if cfg.CDNEndpointID == "" || cfg.CDNToken == "" {
			return nil, fmt.Errorf("storage: STORAGE_CDN=digitalocean needs STORAGE_CDN_ENDPOINT_ID (the Spaces CDN endpoint's id) and STORAGE_CDN_TOKEN (keep it in the credentials)")
		}
		return &doCDN{api: strings.TrimSuffix(cfg.CDNAPIURL, "/"), endpoint: cfg.CDNEndpointID, token: cfg.CDNToken, client: &http.Client{Timeout: 30 * time.Second}}, nil
	}
	return nil, fmt.Errorf("storage: unknown STORAGE_CDN %q (one of %s, or empty)", cfg.CDN, strings.Join(CDNs, ", "))
}

// doCDN purges the DigitalOcean Spaces CDN: DELETE
// /v2/cdn/endpoints/{id}/cache with the paths, a wildcard purging a
// folder.
type doCDN struct {
	api, endpoint, token string
	client               *http.Client
}

// doBatch is how many paths go in one purge request.
const doBatch = 50

func (d *doCDN) purge(ctx context.Context, paths []string) error {
	for len(paths) > 0 {
		n := min(len(paths), doBatch)
		body, _ := json.Marshal(map[string][]string{"files": paths[:n]})
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, d.api+"/v2/cdn/endpoints/"+d.endpoint+"/cache", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+d.token)
		req.Header.Set("Content-Type", "application/json")
		res, err := d.client.Do(req)
		if err != nil {
			return fmt.Errorf("storage: purge: %w", err)
		}
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		res.Body.Close()
		if res.StatusCode >= 300 {
			return fmt.Errorf("storage: purge: DigitalOcean answered %s: %s", res.Status, strings.TrimSpace(string(msg)))
		}
		paths = paths[n:]
	}
	return nil
}
