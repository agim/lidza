package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

// The benchmarks that back the guidance on when to use a pack: the cost
// of the boundary alone (JSON in, JSON out, one pool call) at three
// payload sizes, then the same two workloads in Go and in Rust under
// wazero: a numeric loop (brute-force nearest neighbours) and an
// allocation-heavy loop (word frequencies). Run with
//
//	go test ./pkg/engine -run xxx -bench . -benchmem
//
// and put the numbers in docs/lidza-guide.md.tmpl under "Rust: when and how".

// variants are the two ways a pack can be compiled: with deadline checks
// in every loop (the default) and without.
var variants = []struct {
	name string
	opt  Options
}{
	{"interruptible", Options{MemoryMB: 256}},
	{"uninterruptible", Options{MemoryMB: 256, Uninterruptible: true}},
}

func benchPool(b *testing.B, opt Options) *Pool {
	b.Helper()
	wasm := buildTestWasm(b)
	m, err := Compile(context.Background(), wasm, opt)
	if err != nil {
		b.Fatal(err)
	}
	p, err := NewPool(context.Background(), m, 1, 30*time.Second)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { p.Close(context.Background()); m.Close(context.Background()) })
	return p
}

func BenchmarkBoundary(b *testing.B) {
	for _, v := range variants {
		p := benchPool(b, v.opt)
		for _, size := range []int{100, 10 << 10, 1 << 20} {
			payload := strings.Repeat("x", size)
			in, _ := json.Marshal(map[string]string{"data": payload})
			b.Run(fmt.Sprintf("%s/echo-%s", v.name, byteSize(size)), func(b *testing.B) {
				b.SetBytes(int64(len(in)))
				for i := 0; i < b.N; i++ {
					if _, err := p.Call(context.Background(), "echo", in); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

type nearestIn struct {
	Points  [][2]float64 `json:"points"`
	Queries [][2]float64 `json:"queries"`
}

func nearestGo(in nearestIn) []int {
	out := make([]int, len(in.Queries))
	for qi, q := range in.Queries {
		best, bestD := 0, 1e308
		for i, p := range in.Points {
			dx, dy := p[0]-q[0], p[1]-q[1]
			if d := dx*dx + dy*dy; d < bestD {
				bestD, best = d, i
			}
		}
		out[qi] = best
	}
	return out
}

func BenchmarkNearest(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	in := nearestIn{Points: make([][2]float64, 2000), Queries: make([][2]float64, 500)}
	for i := range in.Points {
		in.Points[i] = [2]float64{rng.Float64(), rng.Float64()}
	}
	for i := range in.Queries {
		in.Queries[i] = [2]float64{rng.Float64(), rng.Float64()}
	}
	b.Run("go", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			nearestGo(in)
		}
	})
	data, _ := json.Marshal(in)
	var p *Pool
	for _, v := range variants {
		p = benchPool(b, v.opt)
		b.Run("rust-wasm-"+v.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := p.Call(context.Background(), "nearest", data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	// Correctness: both sides agree.
	var out struct{ Indices []int }
	if err := CallJSON(context.Background(), p, "nearest", in, &out); err != nil {
		b.Fatal(err)
	}
	if want := nearestGo(in); fmt.Sprint(out.Indices) != fmt.Sprint(want) {
		b.Fatal("rust and go disagree")
	}
}

func wordfreqGo(text string) (int, [][2]any) {
	counts := map[string]int{}
	for _, w := range strings.Fields(text) {
		counts[w]++
	}
	type wc struct {
		w string
		n int
	}
	top := make([]wc, 0, len(counts))
	for w, n := range counts {
		top = append(top, wc{w, n})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].n != top[j].n {
			return top[i].n > top[j].n
		}
		return top[i].w < top[j].w
	})
	if len(top) > 10 {
		top = top[:10]
	}
	out := make([][2]any, len(top))
	for i, t := range top {
		out[i] = [2]any{t.w, t.n}
	}
	return len(counts), out
}

func BenchmarkWordFreq(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var sb strings.Builder
	for sb.Len() < 200<<10 {
		fmt.Fprintf(&sb, "w%d ", rng.Intn(5000))
	}
	text := sb.String()
	b.Run("go", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			wordfreqGo(text)
		}
	})
	data, _ := json.Marshal(map[string]string{"text": text})
	var p *Pool
	for _, v := range variants {
		p = benchPool(b, v.opt)
		b.Run("rust-wasm-"+v.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := p.Call(context.Background(), "wordfreq", data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	var out struct {
		Unique int
		Top    [][2]any
	}
	if err := CallJSON(context.Background(), p, "wordfreq", map[string]string{"text": text}, &out); err != nil {
		b.Fatal(err)
	}
	unique, top := wordfreqGo(text)
	if out.Unique != unique || fmt.Sprint(out.Top) != fmt.Sprint(top) {
		b.Fatalf("rust and go disagree: %d %v vs %d %v", out.Unique, out.Top, unique, top)
	}
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%dMB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%dKB", n>>10)
	}
	return fmt.Sprintf("%dB", n)
}
