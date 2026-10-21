package gateway_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agim/lidza/packs/cache"
	"github.com/agim/lidza/pkg/gateway"
)

func TestRealSharedClaims(t *testing.T) {
	endpoint := os.Getenv("LIDZA_TEST_GATEWAY_CACHE_URL")
	if endpoint == "" {
		t.Skip("set isolated LIDZA_TEST_GATEWAY_CACHE_URL for shared replay-store acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := cache.NewValkey(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := cache.NewValkey(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	prefix := fmt.Sprintf("gateway-test:%d:", time.Now().UnixNano())
	defer first.DeletePrefix(context.Background(), prefix)
	guards := []func(context.Context, string, time.Duration) (bool, error){gateway.Claims(first, prefix), gateway.Claims(second, prefix)}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fresh, err := guards[i%2](ctx, "same-nonce", 30*time.Second)
			if err != nil {
				t.Error(err)
			}
			if fresh {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("atomic shared acceptance count=%d", accepted.Load())
	}
	restarted, err := cache.NewValkey(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if fresh, err := gateway.Claims(restarted, prefix)(ctx, "same-nonce", 30*time.Second); err != nil || fresh {
		t.Fatalf("replay after verifier client restart accepted=%v err=%v", fresh, err)
	}
	t.Log("100 concurrent claims across independent verifier clients accepted exactly once; replay remained rejected after client restart")
}
