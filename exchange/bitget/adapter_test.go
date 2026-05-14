package bitget

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func TestParseBitgetBatchCancelFailuresReportsFailureList(t *testing.T) {
	raw, _ := json.Marshal(map[string]interface{}{
		"successList": []map[string]string{{"orderId": "1"}},
		"failureList": []map[string]string{{
			"orderId":   "2",
			"clientOid": "abc",
			"errorMsg":  "still open",
		}},
	})

	err := parseBitgetBatchCancelFailures(raw)
	if err == nil {
		t.Fatal("expected failure list to return error")
	}
	if !strings.Contains(err.Error(), "orderID=2") || !strings.Contains(err.Error(), "still open") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseBitgetBatchCancelFailuresAcceptsEmptyFailureList(t *testing.T) {
	raw, _ := json.Marshal(map[string]interface{}{
		"successList": []map[string]string{{"orderId": "1"}},
		"failureList": []map[string]string{},
	})
	if err := parseBitgetBatchCancelFailures(raw); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSameBitgetSymbolIgnoresEmptyAndDashFormatting(t *testing.T) {
	if !sameBitgetSymbol("ETH-USDT", "ETHUSDT") {
		t.Fatal("expected dashed and compact symbols to match")
	}
	if sameBitgetSymbol("XAGUSDT", "ETHUSDT") {
		t.Fatal("expected different symbols not to match")
	}
	if !sameBitgetSymbol("", "ETHUSDT") {
		t.Fatal("empty update symbol should be accepted for backward compatibility")
	}
}

func TestBitgetHedgeReduceOnlyUsesPositionSideForClose(t *testing.T) {
	tests := []struct {
		name      string
		side      Side
		reduce    bool
		wantSide  string
		wantTrade string
	}{
		{name: "open long", side: SideBuy, wantSide: "buy", wantTrade: "open"},
		{name: "open short", side: SideSell, wantSide: "sell", wantTrade: "open"},
		{name: "close long", side: SideSell, reduce: true, wantSide: "buy", wantTrade: "close"},
		{name: "close short", side: SideBuy, reduce: true, wantSide: "sell", wantTrade: "close"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSide, gotTrade := bitgetOrderSideAndTradeSide("hedge_mode", tt.side, tt.reduce)
			if gotSide != tt.wantSide || gotTrade != tt.wantTrade {
				t.Fatalf("got side=%s tradeSide=%s, want side=%s tradeSide=%s",
					gotSide, gotTrade, tt.wantSide, tt.wantTrade)
			}
		})
	}
}

func TestBitgetInternalOrderSideConvertsCloseOrdersToActionSide(t *testing.T) {
	tests := []struct {
		name      string
		side      string
		tradeSide string
		want      Side
	}{
		{name: "open long", side: "buy", tradeSide: "open", want: SideBuy},
		{name: "open short", side: "sell", tradeSide: "open", want: SideSell},
		{name: "close long", side: "buy", tradeSide: "close", want: SideSell},
		{name: "close short", side: "sell", tradeSide: "close", want: SideBuy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bitgetInternalOrderSide(tt.side, tt.tradeSide); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
