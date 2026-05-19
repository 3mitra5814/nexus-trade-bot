package safety

import (
	"context"
	"testing"
	"time"

	"nexus-trade-bot/config"
)

type fakeReconcileExchange struct {
	positions interface{}
	orders    interface{}
}

func (e fakeReconcileExchange) GetPositions(ctx context.Context, symbol string) (interface{}, error) {
	return e.positions, nil
}

func (e fakeReconcileExchange) GetOpenOrders(ctx context.Context, symbol string) (interface{}, error) {
	return e.orders, nil
}

func (e fakeReconcileExchange) GetBaseAsset() string {
	return "ETH"
}

type fakeReconcilePM struct {
	slots           []SlotInfo
	unrealizedMarks []float64
}

func (pm *fakeReconcilePM) IterateSlots(fn func(price float64, slot interface{}) bool) {
	for _, slot := range pm.slots {
		if !fn(slot.Price, slot) {
			return
		}
	}
}

func (pm *fakeReconcilePM) GetTotalBuyQty() float64             { return 0 }
func (pm *fakeReconcilePM) GetTotalSellQty() float64            { return 0 }
func (pm *fakeReconcilePM) GetReconcileCount() int64            { return 0 }
func (pm *fakeReconcilePM) IncrementReconcileCount()            {}
func (pm *fakeReconcilePM) UpdateLastReconcileTime(t time.Time) {}
func (pm *fakeReconcilePM) GetSymbol() string                   { return "ETHUSDT" }
func (pm *fakeReconcilePM) GetPriceInterval() float64           { return 1 }
func (pm *fakeReconcilePM) EstimateUnrealizedPNL(markPrice float64) float64 {
	pm.unrealizedMarks = append(pm.unrealizedMarks, markPrice)
	return markPrice
}

func TestReconcilerPrefersInjectedMarkPriceForUnrealizedPNL(t *testing.T) {
	cfg := &config.Config{}
	pm := &fakeReconcilePM{}
	ex := fakeReconcileExchange{positions: []struct {
		MarkPrice float64
	}{{MarkPrice: 101}}}
	reconciler := NewReconciler(cfg, ex, pm)
	reconciler.SetMarkPriceProvider(func() float64 { return 99 })

	if err := reconciler.Reconcile(); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(pm.unrealizedMarks) != 1 || pm.unrealizedMarks[0] != 99 {
		t.Fatalf("expected injected mark price 99, got %v", pm.unrealizedMarks)
	}
}

func TestReconcilerPrefersExchangeUnrealizedPNLWhenAvailable(t *testing.T) {
	cfg := &config.Config{}
	pm := &fakeReconcilePM{}
	ex := fakeReconcileExchange{positions: []struct {
		UnrealizedPNL    float64
		HasUnrealizedPNL bool
		MarkPrice        float64
	}{
		{UnrealizedPNL: 1.25, HasUnrealizedPNL: true, MarkPrice: 101},
		{UnrealizedPNL: -0.75, HasUnrealizedPNL: true, MarkPrice: 102},
	}}
	reconciler := NewReconciler(cfg, ex, pm)
	reconciler.SetMarkPriceProvider(func() float64 { return 99 })

	got := reconciler.reconciledUnrealizedPNL(ex.positions)
	if got != 0.5 {
		t.Fatalf("expected exchange unrealized PNL 0.5, got %v", got)
	}
	if len(pm.unrealizedMarks) != 0 {
		t.Fatalf("expected local estimator to be skipped, got marks %v", pm.unrealizedMarks)
	}
}
