package bitget

import (
	"context"
	"testing"
	"time"

	"nexus-trade-bot/exchange/ratelimit"
)

func TestBitgetRateLimitProfileDefaultsAndEnv(t *testing.T) {
	t.Setenv("NEXUS_BITGET_REST_QPS", "")
	profile := bitgetRateLimitProfile("POST", "/api/v2/mix/order/place-order")
	if got := ratelimit.QPS(profile); got != defaultBitgetTradeQPS {
		t.Fatalf("default qps = %d, want %d", got, defaultBitgetTradeQPS)
	}
	t.Setenv("NEXUS_BITGET_REST_QPS", "12")
	if got := ratelimit.QPS(profile); got != 12 {
		t.Fatalf("env qps = %d, want 12", got)
	}
	t.Setenv("NEXUS_BITGET_REST_QPS", "0")
	if got := ratelimit.QPS(profile); got != 0 {
		t.Fatalf("disabled qps = %d, want 0", got)
	}
}

func TestSleepBitgetRESTBackoffHonorsRetryAfterCap(t *testing.T) {
	start := time.Now()
	sleepBitgetRESTBackoff(context.Background(), 0, "1")
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("backoff elapsed too short: %s", elapsed)
	}
}
