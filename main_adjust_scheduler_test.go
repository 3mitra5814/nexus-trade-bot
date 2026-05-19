package main

import (
	"context"
	"testing"
	"time"

	"nexus-trade-bot/exchange"
	"nexus-trade-bot/position"
)

func TestAdjustRequestSchedulerPreservesRebalanceWhenQueueIsFull(t *testing.T) {
	scheduler := newAdjustRequestScheduler()

	scheduler.Schedule("order_update", false)
	scheduler.Schedule("price-grid-shift", true)

	select {
	case <-scheduler.Signals():
	default:
		t.Fatal("expected pending adjust signal")
	}

	req, ok := scheduler.Pop()
	if !ok {
		t.Fatal("expected pending adjust request")
	}
	if !req.allowWindowRebalance {
		t.Fatalf("merged adjust request lost rebalance=true: %+v", req)
	}
	if req.reason != "order_update+price-grid-shift" {
		t.Fatalf("unexpected merged reason %q", req.reason)
	}
}

func TestAdjustRequestSchedulerSignalsAgainAfterPop(t *testing.T) {
	scheduler := newAdjustRequestScheduler()

	scheduler.Schedule("initial", true)
	<-scheduler.Signals()
	if _, ok := scheduler.Pop(); !ok {
		t.Fatal("expected first request")
	}

	scheduler.Schedule("order_update", false)
	select {
	case <-scheduler.Signals():
	default:
		t.Fatal("expected second signal after first request was popped")
	}
	req, ok := scheduler.Pop()
	if !ok {
		t.Fatal("expected second request")
	}
	if req.reason != "order_update" || req.allowWindowRebalance {
		t.Fatalf("unexpected second request: %+v", req)
	}
}

func TestNearestGridPriceTriggersOnRoundedGridBoundary(t *testing.T) {
	anchor := 86.95
	interval := 0.01

	got := nearestGridPrice(86.9401, anchor, interval, 4)
	if got != 86.94 {
		t.Fatalf("expected rounded grid 86.94, got %.4f", got)
	}

	got = nearestGridPrice(86.9499, anchor, interval, 4)
	if got != 86.95 {
		t.Fatalf("expected rounded grid 86.95, got %.4f", got)
	}
}

func TestMaxTradePriceAgeIsPositive(t *testing.T) {
	if maxTradePriceAge <= 0 || maxTradePriceAge > time.Minute {
		t.Fatalf("unexpected maxTradePriceAge: %s", maxTradePriceAge)
	}
}

type fakePositionExchange struct {
	positions []*exchange.Position
}

func (e fakePositionExchange) GetName() string { return "Fake" }
func (e fakePositionExchange) PlaceOrder(ctx context.Context, req *exchange.OrderRequest) (*exchange.Order, error) {
	return nil, nil
}
func (e fakePositionExchange) BatchPlaceOrders(ctx context.Context, orders []*exchange.OrderRequest) ([]*exchange.Order, bool) {
	return nil, false
}
func (e fakePositionExchange) CancelOrder(ctx context.Context, symbol string, orderID int64) error {
	return nil
}
func (e fakePositionExchange) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	return nil
}
func (e fakePositionExchange) CancelAllOrders(ctx context.Context, symbol string) error { return nil }
func (e fakePositionExchange) GetOrder(ctx context.Context, symbol string, orderID int64) (*exchange.Order, error) {
	return nil, nil
}
func (e fakePositionExchange) GetOpenOrders(ctx context.Context, symbol string) ([]*exchange.Order, error) {
	return nil, nil
}
func (e fakePositionExchange) GetAccount(ctx context.Context) (*exchange.Account, error) {
	return &exchange.Account{}, nil
}
func (e fakePositionExchange) GetPositions(ctx context.Context, symbol string) ([]*exchange.Position, error) {
	return e.positions, nil
}
func (e fakePositionExchange) GetBalance(ctx context.Context, asset string) (float64, error) {
	return 0, nil
}
func (e fakePositionExchange) StartOrderStream(ctx context.Context, callback func(interface{})) error {
	return nil
}
func (e fakePositionExchange) StopOrderStream() error { return nil }
func (e fakePositionExchange) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	return 0, nil
}
func (e fakePositionExchange) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	return nil
}
func (e fakePositionExchange) StartKlineStream(ctx context.Context, symbols []string, interval string, callback exchange.CandleUpdateCallback) error {
	return nil
}
func (e fakePositionExchange) StopKlineStream() error { return nil }
func (e fakePositionExchange) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*exchange.Candle, error) {
	return nil, nil
}
func (e fakePositionExchange) GetPriceDecimals() int    { return 2 }
func (e fakePositionExchange) GetQuantityDecimals() int { return 4 }
func (e fakePositionExchange) GetBaseAsset() string     { return "ETH" }
func (e fakePositionExchange) GetQuoteAsset() string    { return "USDT" }

func TestPositionExchangeAdapterPreservesExchangeUnrealizedPNLFlag(t *testing.T) {
	adapter := &positionExchangeAdapter{exchange: fakePositionExchange{positions: []*exchange.Position{{
		Symbol:           "ETHUSDT",
		Size:             1.5,
		EntryPrice:       100,
		MarkPrice:        101,
		UnrealizedPNL:    1.5,
		HasUnrealizedPNL: true,
	}}}}

	raw, err := adapter.GetPositions(context.Background(), "ETHUSDT")
	if err != nil {
		t.Fatalf("GetPositions() error = %v", err)
	}
	positions, ok := raw.([]*position.PositionInfo)
	if !ok || len(positions) != 1 {
		t.Fatalf("unexpected positions payload: %#v", raw)
	}
	if !positions[0].HasUnrealizedPNL || positions[0].UnrealizedPNL != 1.5 {
		t.Fatalf("exchange unrealized pnl flag was not preserved: %#v", positions[0])
	}
}
