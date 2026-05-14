package ratelimit

import "testing"

func TestQPSUsesSpecificEnvBeforeExchangeEnv(t *testing.T) {
	t.Setenv("NEXUS_BITGET_REST_QPS", "5")
	t.Setenv("NEXUS_BITGET_TRADE_QPS", "9")

	got := QPS(Profile{Exchange: "bitget", Bucket: "trade", DefaultQPS: 1})
	if got != 9 {
		t.Fatalf("QPS() = %d, want specific bucket override 9", got)
	}
}

func TestQPSCanDisableLimiter(t *testing.T) {
	t.Setenv("NEXUS_REST_QPS", "0")

	got := QPS(Profile{Exchange: "unknown", Bucket: "trade", DefaultQPS: 9})
	if got != 0 {
		t.Fatalf("QPS() = %d, want disabled 0", got)
	}
}
