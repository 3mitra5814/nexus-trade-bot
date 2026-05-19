package bitget

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestAddHistoryPositionPNLPaginatesAndDedupes(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("idLessThan") == "" {
			rows := make([]string, 0, 100)
			rows = append(rows, `{"positionId":"200","netProfit":"1.5","pnl":"2","totalFunding":"-0.1","openFee":"-0.2","closeFee":"-0.2","uTime":"2000"}`)
			for i := 199; i >= 101; i-- {
				rows = append(rows, fmt.Sprintf(`{"positionId":"%d","netProfit":"0","uTime":"2000"}`, i))
			}
			fmt.Fprintf(w, `{"code":"00000","data":{"list":[%s]}}`, strings.Join(rows, ","))
			return
		}
		fmt.Fprint(w, `{"code":"00000","data":{"list":[{"positionId":"100","netProfit":"2.5","pnl":"3","totalFunding":"-0.1","openFee":"-0.2","closeFee":"-0.2","uTime":"3000"}]}}`)
	}))
	defer server.Close()

	adapter := &BitgetAdapter{
		client:      NewClient("api", "secret", "pass"),
		symbol:      "ETHUSDT",
		productType: "usdt-futures",
	}
	adapter.client.baseURL = server.URL
	summary := &PNLSummary{}

	err := adapter.addHistoryPositionPNL(context.Background(), summary, time.UnixMilli(1000), time.UnixMilli(4000), time.UnixMilli(2500))
	if err != nil {
		t.Fatalf("addHistoryPositionPNL() error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected two paginated requests, got %d: %v", len(paths), paths)
	}
	if !strings.Contains(paths[1], "idLessThan=101") {
		t.Fatalf("expected second request to use the last first-page idLessThan=101, got %s", paths[1])
	}
	if summary.TotalRealizedPNL != 4 || summary.TodayRealizedPNL != 2.5 {
		t.Fatalf("unexpected pnl summary: %#v", summary)
	}
}

func TestAddHistoryPositionPNLUsesOfficialEndIDCursor(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("idLessThan") == "" {
			rows := make([]string, 0, 100)
			for i := 200; i >= 101; i-- {
				rows = append(rows, fmt.Sprintf(`{"positionId":"%d","netProfit":"0","uTime":"2000"}`, i))
			}
			fmt.Fprintf(w, `{"code":"00000","data":{"list":[%s],"endId":"cursor-100"}}`, strings.Join(rows, ","))
			return
		}
		fmt.Fprint(w, `{"code":"00000","data":{"list":[{"positionId":"100","netProfit":"1.25","uTime":"3000"}]}}`)
	}))
	defer server.Close()

	adapter := &BitgetAdapter{
		client:      NewClient("api", "secret", "pass"),
		symbol:      "ETHUSDT",
		productType: "usdt-futures",
	}
	adapter.client.baseURL = server.URL
	summary := &PNLSummary{}

	err := adapter.addHistoryPositionPNL(context.Background(), summary, time.UnixMilli(1000), time.UnixMilli(4000), time.UnixMilli(2500))
	if err != nil {
		t.Fatalf("addHistoryPositionPNL() error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected two paginated requests, got %d: %v", len(paths), paths)
	}
	if !strings.Contains(paths[1], "idLessThan=cursor-100") {
		t.Fatalf("expected second request to use official endId cursor, got %s", paths[1])
	}
	if summary.TotalRealizedPNL != 1.25 || summary.TodayRealizedPNL != 1.25 {
		t.Fatalf("unexpected pnl summary: %#v", summary)
	}
}
