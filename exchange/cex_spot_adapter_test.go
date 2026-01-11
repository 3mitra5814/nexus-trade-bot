package exchange

import (
	"context"
	"testing"
)

func TestCEXSpotGetPositionsReturnsNoInventoryWhenBaseBalanceIsZero(t *testing.T) {
	adapter := &cexSpotAdapter{
		exchangeName: "test",
		symbol:       "ETHUSDT",
		baseAsset:    "ETH",
		quoteAsset:   "USDT",
	}

	positions, err := adapter.GetPositions(context.Background(), "ETHUSDT")
	if err != nil {
		t.Fatalf("GetPositions() error = %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("expected no spot positions when base balance is zero, got %d", len(positions))
	}
}

func TestOKXSpotClientOrderIDEncodingRoundTripPreservesGridID(t *testing.T) {
	raw := "abc123_2n9c_B_L_t6z1ab01"
	encoded := encodeSpotClientOrderID(raw)
	if encoded == raw {
		t.Fatalf("expected OKX spot client order id to remove separators")
	}
	if len(encoded) > 32 {
		t.Fatalf("encoded id exceeds OKX limit: len=%d id=%q", len(encoded), encoded)
	}
	if got := decodeSpotClientOrderID(encoded); got != raw {
		t.Fatalf("decodeSpotClientOrderID(%q) = %q, want %q", encoded, got, raw)
	}
}
