package auth

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/credentials"
)

// TestGeneratedSecret: without AUTH_SECRET, the app generates one on its
// first start and keeps it in the database; nodes starting together and
// every later start use the same one. Without a master key it says
// what to set.
func TestGeneratedSecret(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := credentials.Generate(dir); err != nil {
		t.Fatal(err)
	}
	a.pool.Exec(ctx, `DELETE FROM credential WHERE name = 'AUTH_SECRET'`)
	t.Cleanup(func() { a.pool.Exec(context.Background(), `DELETE FROM credential WHERE name = 'AUTH_SECRET'`) })
	store := db.NewCredentialStore(a.pool, dir)
	s := lidza.NewServices()
	lidza.Provide[credentials.Store](s, store)

	const nodes = 4
	got := make([]string, nodes)
	var wg sync.WaitGroup
	for i := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = generatedSecret(ctx, s)
		}()
	}
	wg.Wait()
	for i := range got {
		if len(got[i]) < 32 || got[i] != got[0] {
			t.Fatalf("nodes disagree: %q", got)
		}
	}
	if again, _ := generatedSecret(ctx, s); again != got[0] {
		t.Fatal("a later start generated another secret")
	}

	// No master key: nowhere to keep it.
	noKey := lidza.NewServices()
	lidza.Provide[credentials.Store](noKey, db.NewCredentialStore(a.pool, filepath.Join(t.TempDir(), "nokey")))
	t.Setenv(credentials.EnvMasterKey, "")
	t.Setenv(credentials.EnvKeyOff, "1")
	if _, err := generatedSecret(ctx, noKey); err == nil || !strings.Contains(err.Error(), "LIDZA_MASTER_KEY") {
		t.Fatalf("no key: %v", err)
	}
}
