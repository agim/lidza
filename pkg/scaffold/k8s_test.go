package scaffold

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/agim/lidza/pkg/config"
)

// TestK8sManifests: --k8s writes a Deployment, Service and
// PodDisruptionBudget that hold the Dockerfile's contract (the nonroot
// user, a read-only root, the probes, port 3000), and gen deploy keeps
// them current afterwards.
func TestK8sManifests(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default("shop", "react")
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\ngo 1.27\n\nrequire github.com/agim/lidza v0.1.101\n"), 0o644)
	if written, _, err := DeployFiles(dir, &cfg, false); err != nil || strings.Contains(strings.Join(written, ","), "k8s") {
		t.Fatalf("plain gen deploy: %v %v", written, err)
	}
	written, _, err := K8sFiles(dir, &cfg, false)
	if err != nil || len(written) != 1 || written[0] != filepath.Join("deploy", "k8s", "shop.yaml") {
		t.Fatalf("k8s: %v %v", written, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "deploy", "k8s", "shop.yaml"))
	dec := yaml.NewDecoder(bytes.NewReader(data))
	kinds := map[string]map[string]any{}
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		kinds[doc["kind"].(string)] = doc
	}
	if len(kinds) != 3 || kinds["Deployment"] == nil || kinds["Service"] == nil || kinds["PodDisruptionBudget"] == nil {
		t.Fatalf("kinds: %v", kinds)
	}
	get := func(v any, path ...any) any {
		for _, p := range path {
			switch k := p.(type) {
			case string:
				v = v.(map[string]any)[k]
			case int:
				v = v.([]any)[k]
			}
		}
		return v
	}
	pod := get(kinds["Deployment"], "spec", "template", "spec")
	c := get(pod, "containers", 0)
	checks := map[string]bool{
		"runAsUser 65532":    get(pod, "securityContext", "runAsUser") == 65532,
		"runAsNonRoot":       get(pod, "securityContext", "runAsNonRoot") == true,
		"read-only root":     get(c, "securityContext", "readOnlyRootFilesystem") == true,
		"no escalation":      get(c, "securityContext", "allowPrivilegeEscalation") == false,
		"port 3000":          get(c, "ports", 0, "containerPort") == 3000,
		"readiness /readyz":  get(c, "readinessProbe", "httpGet", "path") == "/readyz",
		"liveness /healthz":  get(c, "livenessProbe", "httpGet", "path") == "/healthz",
		"secret by app name": get(c, "envFrom", 0, "secretRef", "name") == "shop",
		"service selects":    get(kinds["Service"], "spec", "selector", "app.kubernetes.io/name") == "shop",
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("deployment: %s", name)
		}
	}
	// Once present, plain gen deploy keeps the manifests current.
	os.WriteFile(filepath.Join(dir, "deploy", "k8s", "shop.yaml"), []byte("old"), 0o644)
	if _, kept, _ := DeployFiles(dir, &cfg, false); strings.Join(kept, ",") != filepath.Join("deploy", "k8s", "shop.yaml") {
		t.Fatalf("an edited manifest not reported: %v", kept)
	}
}
