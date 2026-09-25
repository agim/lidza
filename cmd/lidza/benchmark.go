package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza"
)

// BenchmarksDir holds the k6 scenarios, relative to the project root.
const BenchmarksDir = "benchmarks"

const benchmarkUsage = `usage: lidza benchmark [scenario] [--addr http://127.0.0.1:3000] [--vus 500] [--duration 1m] [--warmup 10s]
Runs benchmarks/<scenario>.js (default scale_test) with k6 against a running
app (lidza dev, or the binary): a warm-up at the same load first, then the
measured run, comparing heap and goroutines before and after it. In dev
mode the heap is measured after a forced GC.
`

func runBenchmark(ctx context.Context, args []string) error {
	fs := flags("benchmark")
	dir := fs.String("dir", ".", "project directory")
	addr := fs.String("addr", "http://127.0.0.1:3000", "base URL of the running app")
	vus := fs.Int("vus", 500, "virtual users")
	duration := fs.String("duration", "1m", "test duration")
	warmup := fs.String("warmup", "10s", "warm-up run before measuring; 0 to skip")
	scenario := "scale_test"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		scenario, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := exec.LookPath("k6"); err != nil {
		fmt.Fprint(os.Stderr, benchmarkUsage)
		return errors.New("k6 is not installed (see docs/environment.md)")
	}
	script := filepath.Join(*dir, BenchmarksDir, scenario+".js")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("no scenario %s", script)
	}

	if _, err := sample(ctx, *addr); err != nil {
		return fmt.Errorf("app not reachable at %s: %w", *addr, err)
	}
	k6 := func(dur string, out io.Writer) error {
		cmd := exec.CommandContext(ctx, "k6", "run", "--vus", strconv.Itoa(*vus), "--duration", dur, "-e", "BASE_URL="+*addr, script)
		cmd.Dir = *dir
		cmd.Stdout = out
		cmd.Stderr = out
		return cmd.Run()
	}
	if *warmup != "0" && *warmup != "" {
		fmt.Printf("[lidza] warm-up: %d VUs for %s\n", *vus, *warmup)
		if err := k6(*warmup, io.Discard); err != nil {
			return fmt.Errorf("warm-up failed: %w", err)
		}
	}
	before, err := sample(ctx, *addr)
	if err != nil {
		return err
	}
	fmt.Printf("[lidza] before: heap %.1f MB, %d goroutines, %d requests served\n", before.heapMB, before.goroutines, before.requests)
	k6Err := k6(*duration, os.Stdout)

	after, err := sample(ctx, *addr)
	if err != nil {
		return fmt.Errorf("app not reachable after the run: %w", err)
	}
	fmt.Printf("[lidza] after:  heap %.1f MB, %d goroutines, %d requests served\n", after.heapMB, after.goroutines, after.requests)
	served := after.requests - before.requests
	heapDelta := after.heapMB - before.heapMB
	limit := max(5, before.heapMB*0.2)
	verdict := "flat"
	if heapDelta > limit || after.goroutines-before.goroutines > 50 {
		verdict = "GROWING"
	}
	fmt.Printf("[lidza] %d requests, heap %+.1f MB (limit %+.1f), goroutines %+d: memory %s\n",
		served, heapDelta, limit, after.goroutines-before.goroutines, verdict)
	if k6Err != nil {
		return fmt.Errorf("k6 reported failures (thresholds or errors): %w", k6Err)
	}
	if verdict != "flat" {
		return errors.New("memory grew under load")
	}
	return nil
}

type snapshot struct {
	heapMB     float64
	goroutines int
	requests   int
}

// sample forces a GC through the dev pprof endpoint when available, then
// reads the metrics that matter.
func sample(ctx context.Context, base string) (snapshot, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	if res, err := client.Get(base + "/debug/pprof/heap?gc=1"); err == nil {
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	time.Sleep(200 * time.Millisecond)
	res, err := client.Get(base + lidza.MetricsPath)
	if err != nil {
		return snapshot{}, err
	}
	defer res.Body.Close()
	var s snapshot
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "go_memstats_heap_inuse_bytes "):
			v, _ := strconv.ParseFloat(strings.TrimPrefix(line, "go_memstats_heap_inuse_bytes "), 64)
			s.heapMB = v / 1e6
		case strings.HasPrefix(line, "go_goroutines "):
			v, _ := strconv.ParseFloat(strings.TrimPrefix(line, "go_goroutines "), 64)
			s.goroutines = int(v)
		case strings.HasPrefix(line, "lidza_http_requests_total{"):
			if i := strings.LastIndex(line, " "); i > 0 {
				v, _ := strconv.ParseFloat(line[i+1:], 64)
				s.requests += int(v)
			}
		}
	}
	return s, sc.Err()
}
