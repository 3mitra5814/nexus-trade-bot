package order

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nexus-trade-bot/exchange"
)

type cancelFailExchange struct{}

func (cancelFailExchange) GetName() string { return "test" }
func (cancelFailExchange) PlaceOrder(context.Context, *exchange.OrderRequest) (*exchange.Order, error) {
	return nil, nil
}
func (cancelFailExchange) BatchPlaceOrders(context.Context, []*exchange.OrderRequest) ([]*exchange.Order, bool) {
	return nil, false
}
func (cancelFailExchange) CancelOrder(context.Context, string, int64) error {
	return errors.New("still open")
}
func (cancelFailExchange) BatchCancelOrders(context.Context, string, []int64) error {
	return errors.New("batch failed")
}
func (cancelFailExchange) CancelAllOrders(context.Context, string) error { return nil }
func (cancelFailExchange) GetOrder(context.Context, string, int64) (*exchange.Order, error) {
	return nil, nil
}
func (cancelFailExchange) GetOpenOrders(context.Context, string) ([]*exchange.Order, error) {
	return nil, nil
}
func (cancelFailExchange) GetAccount(context.Context) (*exchange.Account, error) { return nil, nil }
func (cancelFailExchange) GetPositions(context.Context, string) ([]*exchange.Position, error) {
	return nil, nil
}
func (cancelFailExchange) GetBalance(context.Context, string) (float64, error) { return 0, nil }
func (cancelFailExchange) StartOrderStream(context.Context, func(interface{})) error {
	return nil
}
func (cancelFailExchange) StopOrderStream() error { return nil }
func (cancelFailExchange) GetLatestPrice(context.Context, string) (float64, error) {
	return 0, nil
}
func (cancelFailExchange) StartPriceStream(context.Context, string, func(price float64)) error {
	return nil
}
func (cancelFailExchange) StartKlineStream(context.Context, []string, string, exchange.CandleUpdateCallback) error {
	return nil
}
func (cancelFailExchange) StopKlineStream() error { return nil }
func (cancelFailExchange) GetHistoricalKlines(context.Context, string, string, int) ([]*exchange.Candle, error) {
	return nil, nil
}
func (cancelFailExchange) GetPriceDecimals() int    { return 2 }
func (cancelFailExchange) GetQuantityDecimals() int { return 4 }
func (cancelFailExchange) GetBaseAsset() string     { return "ETH" }
func (cancelFailExchange) GetQuoteAsset() string    { return "USDT" }

func TestBatchCancelOrdersReturnsFallbackFailures(t *testing.T) {
	executor := NewExchangeOrderExecutor(cancelFailExchange{}, "ETHUSDT", 1, 1)

	err := executor.BatchCancelOrders([]int64{1, 2})
	if err == nil {
		t.Fatal("expected BatchCancelOrders to return fallback failures")
	}
	if !strings.Contains(err.Error(), "单独撤销失败") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type recoveringPlaceExchange struct {
	cancelFailExchange
	placeCalls int
	openOrders []*exchange.Order
}

func (e *recoveringPlaceExchange) PlaceOrder(context.Context, *exchange.OrderRequest) (*exchange.Order, error) {
	e.placeCalls++
	return nil, context.DeadlineExceeded
}

func (e *recoveringPlaceExchange) GetOpenOrders(context.Context, string) ([]*exchange.Order, error) {
	return e.openOrders, nil
}

func TestPlaceOrderRecoversOpenOrderAfterAmbiguousError(t *testing.T) {
	ex := &recoveringPlaceExchange{
		openOrders: []*exchange.Order{{
			OrderID:       42,
			ClientOrderID: "9900_B_abc",
			Symbol:        "ETHUSDT",
			Side:          exchange.SideBuy,
			Price:         99,
			Quantity:      0.3,
			Status:        exchange.OrderStatusNew,
		}},
	}
	executor := NewExchangeOrderExecutor(ex, "ETHUSDT", 1, 1)

	order, err := executor.PlaceOrder(&OrderRequest{
		Symbol:        "ETHUSDT",
		Side:          "BUY",
		Price:         99,
		Quantity:      0.3,
		PriceDecimals: 2,
		ClientOrderID: "9900_B_abc",
	})
	if err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}
	if order == nil || order.OrderID != 42 {
		t.Fatalf("expected recovered order 42, got %#v", order)
	}
	if ex.placeCalls != 1 {
		t.Fatalf("expected no retry after recovering open order, got %d place attempts", ex.placeCalls)
	}
}

type recordingCancelExchange struct {
	cancelFailExchange
	batchIDs []int64
}

func (e *recordingCancelExchange) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	e.batchIDs = append(e.batchIDs, orderIDs...)
	return nil
}

func TestBatchCancelOrdersSkipsInvalidOrderIDs(t *testing.T) {
	ex := &recordingCancelExchange{}
	executor := NewExchangeOrderExecutor(ex, "ETHUSDT", 1, 1)

	err := executor.BatchCancelOrders([]int64{0, -1, 42})
	if err == nil {
		t.Fatal("expected invalid order IDs to be reported")
	}
	if len(ex.batchIDs) != 1 || ex.batchIDs[0] != 42 {
		t.Fatalf("expected only valid order ID to reach exchange, got %v", ex.batchIDs)
	}
}

func TestBatchCancelOrdersRejectsOnlyInvalidOrderIDs(t *testing.T) {
	ex := &recordingCancelExchange{}
	executor := NewExchangeOrderExecutor(ex, "ETHUSDT", 1, 1)

	err := executor.BatchCancelOrders([]int64{0})
	if err == nil {
		t.Fatal("expected all-invalid order IDs to fail")
	}
	if len(ex.batchIDs) != 0 {
		t.Fatalf("expected exchange not to be called, got %v", ex.batchIDs)
	}
}

type blankClientIDPlaceExchange struct {
	cancelFailExchange
}

func (blankClientIDPlaceExchange) PlaceOrder(context.Context, *exchange.OrderRequest) (*exchange.Order, error) {
	return &exchange.Order{
		OrderID: 42,
		Status:  exchange.OrderStatusNew,
	}, nil
}

func TestPlaceOrderKeepsRequestedClientOrderIDWhenExchangeReturnsBlank(t *testing.T) {
	executor := NewExchangeOrderExecutor(blankClientIDPlaceExchange{}, "ETHUSDT", 1, 1)

	order, err := executor.PlaceOrder(&OrderRequest{
		Symbol:        "ETHUSDT",
		Side:          "BUY",
		Price:         99,
		Quantity:      0.3,
		PriceDecimals: 2,
		ClientOrderID: "9900_B_L_abc01",
	})
	if err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}
	if order.ClientOrderID != "9900_B_L_abc01" {
		t.Fatalf("expected requested ClientOrderID to be preserved, got %q", order.ClientOrderID)
	}
}

func TestIsRateLimitErrorDetectsBitget429(t *testing.T) {
	if !isRateLimitError(errors.New("bitget API 错误: code=429, msg=Too Many Requests")) {
		t.Fatal("expected Bitget 429 to be treated as rate limit")
	}
}

func TestExchangeTradeRateLimitProfileUsesKnownExchangeDefaults(t *testing.T) {
	tests := map[string]int{
		"Bitget":      9,
		"Binance":     9,
		"Bybit":       9,
		"OKX":         27,
		"Gate.io":     9,
		"Hyperliquid": 18,
	}
	for name, want := range tests {
		profile := exchangeTradeRateLimitProfile(name)
		if profile.DefaultQPS != want {
			t.Fatalf("%s default qps = %d, want %d", name, profile.DefaultQPS, want)
		}
	}
}
