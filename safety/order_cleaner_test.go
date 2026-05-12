package safety

import (
	"testing"

	"nexus-trade-bot/config"
)

type cleanerSlot struct {
	Price          float64
	BookSide       string
	OrderID        int64
	OrderSide      string
	OrderStatus    string
	PositionStatus string
	PositionQty    float64
}

type fakeCleanerPM struct {
	slots   []cleanerSlot
	updates []struct {
		price     float64
		bookSide  string
		status    string
		orderID   int64
		clientOID string
	}
}

func (pm *fakeCleanerPM) IterateSlots(fn func(price float64, slot interface{}) bool) {
	for _, slot := range pm.slots {
		if !fn(slot.Price, slot) {
			return
		}
	}
}

func (pm *fakeCleanerPM) UpdateSlotOrderStatus(price float64, bookSide, status string) {
	pm.updates = append(pm.updates, struct {
		price     float64
		bookSide  string
		status    string
		orderID   int64
		clientOID string
	}{price: price, bookSide: bookSide, status: status})
}

func (pm *fakeCleanerPM) UpdateSlotOrderStatusIfCurrent(price float64, bookSide, status string, orderID int64, clientOID string) {
	pm.updates = append(pm.updates, struct {
		price     float64
		bookSide  string
		status    string
		orderID   int64
		clientOID string
	}{price: price, bookSide: bookSide, status: status, orderID: orderID, clientOID: clientOID})
}

type fakeCancelExecutor struct {
	canceled []int64
}

func (e *fakeCancelExecutor) BatchCancelOrders(orderIDs []int64) error {
	e.canceled = append(e.canceled, orderIDs...)
	return nil
}

func TestCleanupOrdersDoesNotCancelExitOrders(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.OrderCleanupThreshold = 1
	cfg.Trading.CleanupBatchSize = 10

	pm := &fakeCleanerPM{slots: []cleanerSlot{
		{Price: 90, BookSide: "LONG", OrderID: 1, OrderSide: "BUY", OrderStatus: "PLACED"},
		{Price: 110, BookSide: "LONG", OrderID: 2, OrderSide: "SELL", OrderStatus: "PLACED", PositionStatus: "FILLED", PositionQty: 0.2},
	}}
	executor := &fakeCancelExecutor{}

	NewOrderCleaner(cfg, executor, pm).CleanupOrders()

	if len(executor.canceled) != 1 || executor.canceled[0] != 1 {
		t.Fatalf("expected only the long entry order to be canceled, got %v", executor.canceled)
	}
}
