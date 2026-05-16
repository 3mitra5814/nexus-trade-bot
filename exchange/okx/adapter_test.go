package okx

import (
	"encoding/base32"
	"testing"
)

func TestClientOrderIDEncodingRoundTripPreservesGridID(t *testing.T) {
	raw := "abc123_2n9c_B_L_t6z1ab01"
	encoded := encodeClientOrderID(raw)
	if encoded == raw {
		t.Fatalf("expected OKX client order id to remove separators")
	}
	if len(encoded) > 32 {
		t.Fatalf("encoded id exceeds OKX limit: len=%d id=%q", len(encoded), encoded)
	}
	if got := decodeClientOrderID(encoded); got != raw {
		t.Fatalf("decodeClientOrderID(%q) = %q, want %q", encoded, got, raw)
	}
}

func TestClientOrderIDDecodeKeepsLegacyBase32Compatibility(t *testing.T) {
	raw := "2n9c_B_L_t6z1ab01"
	legacy := "O" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(raw))
	if got := decodeClientOrderID(legacy); got != raw {
		t.Fatalf("legacy decode = %q, want %q", got, raw)
	}
}

func TestPosSideForOKXOrder(t *testing.T) {
	tests := []struct {
		name       string
		side       Side
		reduceOnly bool
		want       string
	}{
		{name: "open long", side: SideBuy, reduceOnly: false, want: "long"},
		{name: "close long", side: SideSell, reduceOnly: true, want: "long"},
		{name: "open short", side: SideSell, reduceOnly: false, want: "short"},
		{name: "close short", side: SideBuy, reduceOnly: true, want: "short"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := posSideForOKXOrder(tt.side, tt.reduceOnly); got != tt.want {
				t.Fatalf("posSideForOKXOrder() = %q, want %q", got, tt.want)
			}
		})
	}
}
