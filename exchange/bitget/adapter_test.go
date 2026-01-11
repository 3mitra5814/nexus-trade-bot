package bitget

import "testing"

func TestMapBitgetOrderStatusAcceptsRESTAndWSVariants(t *testing.T) {
	tests := map[string]OrderStatus{
		"new":              "NEW",
		"live":             "NEW",
		"partial-fill":     "PARTIALLY_FILLED",
		"partial_filled":   "PARTIALLY_FILLED",
		"partially_filled": "PARTIALLY_FILLED",
		"full-fill":        "FILLED",
		"filled":           "FILLED",
		"cancelled":        "CANCELED",
		"canceled":         "CANCELED",
		"expired":          "CANCELED",
		"rejected":         "CANCELED",
	}
	for input, want := range tests {
		if got := mapBitgetOrderStatus(input); got != want {
			t.Fatalf("mapBitgetOrderStatus(%q) = %s, want %s", input, got, want)
		}
	}
}

func TestParseBitgetExecutedQtyFallsBackAcrossFields(t *testing.T) {
	if got := parseBitgetExecutedQty("", "0.12", "0.34"); got != 0.12 {
		t.Fatalf("expected accBaseVolume fallback 0.12, got %.8f", got)
	}
	if got := parseBitgetExecutedQty("", "", "0.34"); got != 0.34 {
		t.Fatalf("expected baseVolume fallback 0.34, got %.8f", got)
	}
	if got := parseBitgetExecutedQty("0.56", "0.12", "0.34"); got != 0.56 {
		t.Fatalf("expected filledQty priority 0.56, got %.8f", got)
	}
}
