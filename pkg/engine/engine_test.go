package engine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildTestWasm compiles a crate using the scaffold's abi.rs with one
// capability, reverse, plus a busy loop for the deadline test.
func buildTestWasm(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not installed")
	}
	abi, err := os.ReadFile("../pack/files/abi.rs.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"enginetest\"\nversion = \"0.1.0\"\nedition = \"2024\"\n\n[lib]\ncrate-type = [\"cdylib\"]\n\n[dependencies]\nserde = { version = \"1\", features = [\"derive\"] }\nserde_json = \"1\"\n\n[profile.release]\nopt-level = \"s\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "abi.rs"), abi, 0o644)
	os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(`pub mod abi;
use serde::{Deserialize, Serialize};

#[derive(Deserialize)]
struct In { text: String }
#[derive(Serialize)]
struct Out { reversed: String, len: usize }

lidza_export!(reverse, |i: In| -> Result<Out, String> {
    if i.text.is_empty() { return Err("empty text".into()); }
    Ok(Out { reversed: i.text.chars().rev().collect(), len: i.text.len() })
});

#[derive(Deserialize)]
struct Spin { n: u64 }
lidza_export!(spin, |s: Spin| -> Result<u64, String> {
    let mut x: u64 = 0;
    for i in 0..s.n { x = x.wrapping_add(i ^ (x >> 3)); }
    Ok(x)
});
`), 0o644)
	cmd := exec.Command("cargo", "build", "--release", "--target", "wasm32-wasip1", "--quiet")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CARGO_TARGET_DIR="+filepath.Join(os.TempDir(), "lidza-engine-test-target"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cargo build: %v\n%s", err, out)
	}
	wasm, err := os.ReadFile(filepath.Join(os.TempDir(), "lidza-engine-test-target", "wasm32-wasip1", "release", "enginetest.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	return wasm
}

func TestPool(t *testing.T) {
	wasm := buildTestWasm(t)
	ctx := context.Background()
	m, err := Compile(ctx, wasm, Options{MemoryMB: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	exports := strings.Join(m.Exports(), ",")
	for _, want := range []string{"reverse", "spin", "lidza_alloc", "lidza_free"} {
		if !strings.Contains(exports, want) {
			t.Fatalf("exports %s missing %s", exports, want)
		}
	}
	pool, err := NewPool(ctx, m, 2, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close(ctx)

	var out struct {
		Reversed string `json:"reversed"`
		Len      int    `json:"len"`
	}
	if err := CallJSON(ctx, pool, "reverse", map[string]string{"text": "līdza"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Reversed != "azdīl" || out.Len != 6 {
		t.Fatalf("got %+v", out)
	}

	// Errors from the capability.
	err = CallJSON(ctx, pool, "reverse", map[string]string{"text": ""}, &out)
	var ce *CallError
	if err == nil || !errorAs(err, &ce) || ce.Message != "empty text" {
		t.Fatalf("capability error: %v", err)
	}
	if err := CallJSON(ctx, pool, "reverse", map[string]int{"text": 1}, &out); err == nil || !strings.Contains(err.Error(), "invalid input") {
		t.Fatalf("bad input: %v", err)
	}
	if _, err := pool.Call(ctx, "nope", []byte("{}")); err == nil || !strings.Contains(err.Error(), "not exported") {
		t.Fatalf("unknown fn: %v", err)
	}

	// Concurrency beyond the pool size queues, never fails.
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var o struct{ Reversed string }
			errs <- CallJSON(ctx, pool, "reverse", map[string]string{"text": "ab"}, &o)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	// A call past the deadline is cut off and the instance replaced.
	short, err := NewPool(ctx, m, 1, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer short.Close(ctx)
	start := time.Now()
	_, err = short.Call(ctx, "spin", []byte(`{"n":100000000000}`))
	if err == nil || !strings.Contains(err.Error(), "deadline") || time.Since(start) > 2*time.Second {
		t.Fatalf("deadline: %v after %v", err, time.Since(start))
	}
	var n uint64
	if err := CallJSON(ctx, short, "spin", map[string]int{"n": 10}, &n); err != nil {
		t.Fatalf("pool unusable after deadline: %v", err)
	}
	if raw, err := short.Call(ctx, "spin", []byte(`{"n":0}`)); err != nil || string(raw) != "0" {
		t.Fatalf("raw: %s %v", raw, err)
	}
	_ = json.Valid
}

func errorAs(err error, target **CallError) bool {
	for err != nil {
		if e, ok := err.(*CallError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
