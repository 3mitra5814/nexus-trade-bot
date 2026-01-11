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
