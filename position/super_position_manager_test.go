package position

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"nexus-trade-bot/config"
	"nexus-trade-bot/utils"
)

type noopExecutor struct{}

func (noopExecutor) PlaceOrder(req *OrderRequest) (*Order, error) { return nil, nil }
func (noopExecutor) BatchPlaceOrders(orders []*OrderRequest) ([]*Order, bool) {
	return nil, false
}
func (noopExecutor) BatchCancelOrders(orderIDs []int64) error { return nil }

type noopExchange struct{}

func (noopExchange) GetName() string { return "test" }
func (noopExchange) GetPositions(ctx context.Context, symbol string) (interface{}, error) {
	return []*PositionInfo{}, nil
}
func (noopExchange) GetOpenOrders(ctx context.Context, symbol string) (interface{}, error) {
	return nil, nil
}
func (noopExchange) GetOrder(ctx context.Context, symbol string, orderID int64) (interface{}, error) {
	return nil, nil
}
func (noopExchange) GetBaseAsset() string { return "ETH" }
func (noopExchange) CancelAllOrders(ctx context.Context, symbol string) error {
	return nil
}

type binanceNamedExchange struct {
	noopExchange
}

func (binanceNamedExchange) GetName() string { return "Binance" }

type seededExchange struct {
	noopExchange
	positions []*PositionInfo
}

func (e seededExchange) GetPositions(ctx context.Context, symbol string) (interface{}, error) {
	return e.positions, nil
}

type staleFillExchange struct {
	noopExchange
	positions []*PositionInfo
	order     interface{}
}

func (e staleFillExchange) GetPositions(ctx context.Context, symbol string) (interface{}, error) {
	return e.positions, nil
}

func (e staleFillExchange) GetOrder(ctx context.Context, symbol string, orderID int64) (interface{}, error) {
	return e.order, nil
}

type goneOrderExchange struct {
	noopExchange
}

func (goneOrderExchange) GetOrder(ctx context.Context, symbol string, orderID int64) (interface{}, error) {
	return nil, errors.New("Unknown order sent")
}

func TestNeutralBookSidesUseSeparateSlotsAtSamePrice(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "neutral"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)

	longSlot := spm.getOrCreateSlot(3000, BookSideLong)
	shortSlot := spm.getOrCreateSlot(3000, BookSideShort)

	if longSlot == shortSlot {
		t.Fatalf("long and short books must not share the same slot at one price")
	}
	if longSlot.BookSide != BookSideLong || shortSlot.BookSide != BookSideShort {
		t.Fatalf("book side was not preserved: long=%s short=%s", longSlot.BookSide, shortSlot.BookSide)
	}
}

func TestInitializeRestoresExistingLongAndAdjustPlacesEntryAndExit(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	exchange := seededExchange{positions: []*PositionInfo{{
		Symbol:     "ETHUSDT",
		Size:       0.5,
		EntryPrice: 99,
		MarkPrice:  100,
	}}}
	spm := NewSuperPositionManager(cfg, executor, exchange, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	var entryOrders, exitOrders int
	for _, order := range executor.orders {
		if order.Side == "BUY" && !order.ReduceOnly {
			entryOrders++
		}
		if order.Side == "SELL" && order.ReduceOnly {
			exitOrders++
		}
	}
	if entryOrders == 0 || exitOrders == 0 {
		t.Fatalf("expected both entry BUY and reduce-only exit SELL orders, got entry=%d exit=%d orders=%d",
			entryOrders, exitOrders, len(executor.orders))
	}
}

type captureExecutor struct {
	mu       sync.Mutex
	orders   []*OrderRequest
	canceled []int64
	nextID   int64
	blankOID bool
}

type hookExecutor struct {
	captureExecutor
	beforeReturn func(req *OrderRequest, orderID int64)
}

type cancelHookExecutor struct {
	captureExecutor
	onCancel func(orderIDs []int64)
}

func (e *captureExecutor) PlaceOrder(req *OrderRequest) (*Order, error) {
	orders, _ := e.BatchPlaceOrders([]*OrderRequest{req})
	if len(orders) == 0 {
		return nil, nil
	}
	return orders[0], nil
}

func (e *captureExecutor) BatchPlaceOrders(orders []*OrderRequest) ([]*Order, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	placed := make([]*Order, 0, len(orders))
	for _, req := range orders {
		e.nextID++
		copied := *req
		e.orders = append(e.orders, &copied)
		clientOID := req.ClientOrderID
		if e.blankOID {
			clientOID = ""
		}
		placed = append(placed, &Order{
			OrderID:       e.nextID,
			ClientOrderID: clientOID,
			Symbol:        req.Symbol,
			Side:          req.Side,
			Price:         req.Price,
			Quantity:      req.Quantity,
			Status:        OrderStatusPlaced,
			CreatedAt:     time.Now(),
		})
	}
	return placed, false
}

func (e *hookExecutor) BatchPlaceOrders(orders []*OrderRequest) ([]*Order, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	placed := make([]*Order, 0, len(orders))
	for _, req := range orders {
		e.nextID++
		copied := *req
		e.orders = append(e.orders, &copied)
		if e.beforeReturn != nil {
			e.beforeReturn(req, e.nextID)
		}
		placed = append(placed, &Order{
			OrderID:       e.nextID,
			ClientOrderID: req.ClientOrderID,
			Symbol:        req.Symbol,
			Side:          req.Side,
			Price:         req.Price,
			Quantity:      req.Quantity,
			Status:        OrderStatusPlaced,
			CreatedAt:     time.Now(),
		})
	}
	return placed, false
}

func (e *captureExecutor) BatchCancelOrders(orderIDs []int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.canceled = append(e.canceled, orderIDs...)
	return nil
}

func (e *cancelHookExecutor) BatchCancelOrders(orderIDs []int64) error {
	e.mu.Lock()
	e.canceled = append(e.canceled, orderIDs...)
	e.mu.Unlock()
	if e.onCancel != nil {
		e.onCancel(orderIDs)
	}
	return nil
}

func ageActiveEntryOrders(spm *SuperPositionManager, bookSide string) {
	oldCreatedAt := time.Now().Add(-entryWindowSyncMinAge - time.Second)
	spm.slots.Range(func(key, value interface{}) bool {
		slot := value.(*InventorySlot)
		slot.mu.Lock()
		if slot.BookSide == bookSide && spm.isEntryOrder(slot.OrderSide, slot.BookSide) && spm.slotHasActiveOrder(slot) {
			slot.OrderCreatedAt = oldCreatedAt
		}
		slot.mu.Unlock()
		return true
	})
}

func primeStableEntryWindowSync(t *testing.T, spm *SuperPositionManager, currentPrice float64) {
	primeEntryWindowSync(t, spm, currentPrice, entryWindowStableDuration)
}

func primeFarStableEntryWindowSync(t *testing.T, spm *SuperPositionManager, currentPrice float64) {
	primeEntryWindowSync(t, spm, currentPrice, entryWindowFarStableDuration)
}

func primeEntryWindowSync(t *testing.T, spm *SuperPositionManager, currentPrice float64, stableDuration time.Duration) {
	t.Helper()
	if err := spm.AdjustOrdersWithRebalance(currentPrice, true); err != nil {
		t.Fatalf("prime AdjustOrdersWithRebalance() error = %v", err)
	}
	spm.mu.Lock()
	if spm.pendingEntryWindowSyncKey == "" {
		spm.mu.Unlock()
		t.Fatalf("expected pending entry window sync key to be recorded")
	}
	spm.pendingEntryWindowSyncSeen = time.Now().Add(-stableDuration - time.Second)
	spm.mu.Unlock()
}

func TestTerminalCanceledUpdateAppliesFilledDelta(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)

	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "NEW",
		Side:          "BUY",
	})
	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "CANCELED",
		ExecutedQty:   0.5,
		Side:          "BUY",
	})

	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PositionStatus != PositionStatusFilled {
		t.Fatalf("expected partially filled canceled entry to leave a filled position, got %s", slot.PositionStatus)
	}
	if slot.PositionQty != 0.5 {
		t.Fatalf("expected position qty 0.5, got %.8f", slot.PositionQty)
	}
	if got := spm.GetTotalBuyQty(); got != 0.5 {
		t.Fatalf("expected total buy qty 0.5, got %.8f", got)
	}
}

func TestDuplicateTerminalEntryFillIsIgnored(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)

	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "NEW",
		Side:          "BUY",
	})
	if !spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "FILLED",
		ExecutedQty:   0.5,
		Side:          "BUY",
	}) {
		t.Fatalf("expected first terminal fill to request adjust")
	}
	if !spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "FILLED",
		ExecutedQty:   0.5,
		Side:          "BUY",
	}) {
		t.Fatalf("duplicate terminal fill may still request adjust, but must not duplicate qty")
	}

	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PositionQty != 0.5 {
		t.Fatalf("duplicate terminal fill changed position qty, got %.8f", slot.PositionQty)
	}
	if got := spm.GetTotalBuyQty(); got != 0.5 {
		t.Fatalf("duplicate terminal fill changed total buy qty, got %.8f", got)
	}
}

func TestDuplicatePartialThenTerminalExitFillIsIgnored(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "SELL", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.5
	slot.EntryPrice = 98.5
	slot.BookSide = BookSideLong
	slot.OrderID = 1
	slot.ClientOID = clientOID
	slot.OrderSide = "SELL"
	slot.OrderStatus = OrderStatusConfirmed
	slot.SlotStatus = SlotStatusLocked
	slot.mu.Unlock()

	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "PARTIALLY_FILLED",
		ExecutedQty:   0.2,
		Price:         100,
		Side:          "SELL",
	})
	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "FILLED",
		ExecutedQty:   0.5,
		Price:         100,
		Side:          "SELL",
	})
	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "FILLED",
		ExecutedQty:   0.5,
		Price:         100,
		Side:          "SELL",
	})

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PositionQty != 0 {
		t.Fatalf("duplicate terminal exit fill changed position qty, got %.8f", slot.PositionQty)
	}
	if got := spm.GetTotalSellQty(); got != 0.5 {
		t.Fatalf("duplicate terminal exit fill changed total sell qty, got %.8f", got)
	}
}

func TestRealizedPNLUsesActualEntryAveragePrice(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	entryOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	spm.OnOrderUpdate(OrderUpdate{OrderID: 1, ClientOrderID: entryOID, Status: "NEW", Side: "BUY"})
	spm.OnOrderUpdate(OrderUpdate{OrderID: 1, ClientOrderID: entryOID, Status: "FILLED", ExecutedQty: 0.2, AvgPrice: 98.8, Side: "BUY"})

	exitOID := spm.generateClientOrderID(99, "SELL", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.OrderID = 2
	slot.ClientOID = exitOID
	slot.OrderSide = "SELL"
	slot.OrderStatus = OrderStatusConfirmed
	slot.SlotStatus = SlotStatusLocked
	slot.mu.Unlock()

	spm.OnOrderUpdate(OrderUpdate{OrderID: 2, ClientOrderID: exitOID, Status: "FILLED", ExecutedQty: 0.2, AvgPrice: 100.1, Side: "SELL"})

	assertFloatNear(t, spm.GetRealizedPNL(), gridRealizedPNL(98.8, 100.1, 0.2, BookSideLong, config.DefaultFeeRate))
}

func TestUnrealizedPNLUsesActualEntryAveragePrice(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	entryOID := spm.generateClientOrderID(101, "SELL", BookSideShort)
	spm.OnOrderUpdate(OrderUpdate{OrderID: 1, ClientOrderID: entryOID, Status: "NEW", Side: "SELL"})
	spm.OnOrderUpdate(OrderUpdate{OrderID: 1, ClientOrderID: entryOID, Status: "FILLED", ExecutedQty: 0.3, AvgPrice: 101.2, Side: "SELL"})

	assertFloatNear(t, spm.EstimateUnrealizedPNL(100), (101.2-100)*0.3)
}

func TestOnOrderUpdateAcceptsBinanceBrokerPrefixedClientOID(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "SOLUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.02
	cfg.Trading.OrderQuantity = 29
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10

	spm := NewSuperPositionManager(cfg, noopExecutor{}, binanceNamedExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(91.36, "SELL", BookSideShort)
	slot := spm.getOrCreateSlot(91.36, BookSideShort)
	slot.mu.Lock()
	slot.OrderID = 123
	slot.ClientOID = clientOID
	slot.OrderSide = "SELL"
	slot.OrderStatus = OrderStatusConfirmed
	slot.SlotStatus = SlotStatusLocked
	slot.mu.Unlock()

	prefixedOID := utils.AddBrokerPrefix("binance", clientOID)
	if !spm.OnOrderUpdate(OrderUpdate{
		OrderID:       123,
		ClientOrderID: prefixedOID,
		Status:        "FILLED",
		Side:          "SELL",
		ExecutedQty:   0.32,
		AvgPrice:      91.36,
	}) {
		t.Fatal("expected prefixed Binance order update to be accepted")
	}

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PositionStatus != PositionStatusFilled || math.Abs(slot.PositionQty-0.32) > 1e-9 {
		t.Fatalf("expected filled short position from prefixed update, status=%s qty=%.8f", slot.PositionStatus, slot.PositionQty)
	}
	if slot.ClientOID != "" || slot.OrderID != 0 {
		t.Fatalf("filled order should clear active tracking, orderID=%d clientOID=%q", slot.OrderID, slot.ClientOID)
	}
}

func TestSnapshotRefreshesStaleFillBeforePositionReconcile(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "SOLUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.02
	cfg.Trading.OrderQuantity = 29
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10

	clientOID := utils.GenerateOrderIDWithTag(91.16, "SELL", BookSideShort, 2, "")
	exchange := staleFillExchange{
		positions: []*PositionInfo{{Symbol: "SOLUSDT", Size: -0.96, EntryPrice: 91.2, MarkPrice: 91.3}},
		order: struct {
			OrderID       int64
			ClientOrderID string
			Side          string
			Status        string
			Price         float64
			Quantity      float64
			ExecutedQty   float64
			AvgPrice      float64
		}{
			OrderID:       99,
			ClientOrderID: utils.AddBrokerPrefix("binance", clientOID),
			Side:          "SELL",
			Status:        "FILLED",
			Price:         91.16,
			Quantity:      0.32,
			ExecutedQty:   0.32,
			AvgPrice:      91.16,
		},
	}
	spm := NewSuperPositionManager(cfg, noopExecutor{}, exchange, 2, 3)
	for _, price := range []float64{91.20, 91.18} {
		slot := spm.getOrCreateSlot(price, BookSideShort)
		slot.mu.Lock()
		slot.PositionStatus = PositionStatusFilled
		slot.PositionQty = 0.32
		slot.SlotStatus = SlotStatusFree
		slot.BookSide = BookSideShort
		slot.mu.Unlock()
	}
	pendingSlot := spm.getOrCreateSlot(91.16, BookSideShort)
	pendingSlot.mu.Lock()
	pendingSlot.OrderID = 99
	pendingSlot.ClientOID = clientOID
	pendingSlot.OrderSide = "SELL"
	pendingSlot.OrderStatus = OrderStatusConfirmed
	pendingSlot.SlotStatus = SlotStatusLocked
	pendingSlot.BookSide = BookSideShort
	pendingSlot.mu.Unlock()

	spm.ApplyExchangeSnapshot(exchange.positions, nil)

	_, localShort := spm.localPositionTotals()
	if math.Abs(localShort-0.96) > 1e-9 {
		t.Fatalf("stale fill and position reconcile should not double count, localShort=%.8f", localShort)
	}
}

func TestLateNewAfterTerminalFillDoesNotRelockSlot(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)

	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "NEW",
		Side:          "BUY",
	})
	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "FILLED",
		ExecutedQty:   0.5,
		Side:          "BUY",
	})
	if spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "NEW",
		Side:          "BUY",
	}) {
		t.Fatalf("late NEW after terminal fill must not request adjust")
	}

	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.SlotStatus != SlotStatusFree || slot.OrderID != 0 || slot.ClientOID != "" {
		t.Fatalf("late NEW relocked terminal slot, slot=%s orderID=%d clientOID=%q",
			slot.SlotStatus, slot.OrderID, slot.ClientOID)
	}
}

func TestPartiallyFilledCanceledStatusIsTerminal(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "NEW",
		Side:          "BUY",
	})

	shouldAdjust := spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1,
		ClientOrderID: clientOID,
		Status:        "PARTIALLY_FILLED_CANCELED",
		ExecutedQty:   0.5,
		Side:          "BUY",
	})
	if !shouldAdjust {
		t.Fatalf("expected terminal partial-cancel update to request immediate adjust")
	}

	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PositionStatus != PositionStatusFilled || slot.OrderID != 0 || slot.ClientOID != "" {
		t.Fatalf("expected terminal partial-cancel to free slot with filled position, status=%s orderID=%d clientOID=%q",
			slot.PositionStatus, slot.OrderID, slot.ClientOID)
	}
}

func TestRemotePartiallyFilledCanceledStatusIsTerminal(t *testing.T) {
	if got := normalizeRemoteOrderStatus("PARTIALLY_FILLED_CANCELED"); got != OrderStatusCanceled {
		t.Fatalf("remote partially-filled-canceled status = %s, want %s", got, OrderStatusCanceled)
	}
	if got := normalizeRemoteOrderStatus("PARTIAL_FILLED_CANCELLED"); got != OrderStatusCanceled {
		t.Fatalf("remote partial-filled-cancelled status = %s, want %s", got, OrderStatusCanceled)
	}
}

func TestNewOrderUpdateBeforePlaceResponseLocksPendingSlot(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.SlotStatus = SlotStatusPending
	slot.mu.Unlock()

	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       7,
		ClientOrderID: clientOID,
		Status:        "NEW",
		Side:          "BUY",
		Price:         99,
	})

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.OrderStatus != OrderStatusConfirmed || slot.SlotStatus != SlotStatusLocked || slot.OrderID != 7 {
		t.Fatalf("expected pending slot to become confirmed/locked, status=%s slot=%s orderID=%d",
			slot.OrderStatus, slot.SlotStatus, slot.OrderID)
	}
}

func TestOrderUpdateWithoutOrderIDPreservesExistingOrderID(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.OrderID = 7
	slot.ClientOID = clientOID
	slot.OrderSide = "BUY"
	slot.OrderStatus = OrderStatusPlaced
	slot.SlotStatus = SlotStatusLocked
	slot.mu.Unlock()

	spm.OnOrderUpdate(OrderUpdate{
		ClientOrderID: clientOID,
		Status:        "NEW",
		Side:          "BUY",
		Price:         99,
	})

	slot.mu.RLock()
	gotOrderID := slot.OrderID
	slot.mu.RUnlock()
	if gotOrderID != 7 {
		t.Fatalf("expected zero-order-id update to preserve existing order id 7, got %d", gotOrderID)
	}
}

func TestNeutralAdjustOrdersCreatesEntryQuotaPerBook(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "neutral"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 4
	cfg.Trading.SellWindowSize = 4
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	var buys, sells int
	for _, order := range executor.orders {
		switch order.Side {
		case "BUY":
			buys++
		case "SELL":
			sells++
		}
	}
	if buys != 4 || sells != 4 {
		t.Fatalf("expected neutral mode to backfill 4 long and 4 short entries, got buys=%d sells=%d", buys, sells)
	}
}

func TestInitializeShortUsesSellWindowSize(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	sellEntries := 0
	for _, order := range executor.orders {
		if order.Side == "SELL" && !order.ReduceOnly {
			sellEntries++
		}
	}
	if sellEntries != cfg.Trading.SellWindowSize {
		t.Fatalf("short entry window must use sell_window_size, got %d want %d orders=%v",
			sellEntries, cfg.Trading.SellWindowSize, executor.orders)
	}
}

func TestNeutralEntryCapacityNotBlockedByOppositeBookExits(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "neutral"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2
	cfg.Trading.OrderCleanupThreshold = 8

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	for _, price := range []float64{98, 99} {
		slot := spm.getOrCreateSlot(price, BookSideLong)
		slot.mu.Lock()
		slot.BookSide = BookSideLong
		slot.PositionStatus = PositionStatusFilled
		slot.PositionQty = 0.3
		slot.SlotStatus = SlotStatusFree
		slot.mu.Unlock()
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	var longExitSells, shortEntrySells int
	for _, order := range executor.orders {
		if order.Side != "SELL" {
			continue
		}
		if order.ReduceOnly {
			longExitSells++
		} else {
			shortEntrySells++
		}
	}
	if longExitSells != 1 || shortEntrySells != cfg.Trading.SellWindowSize {
		t.Fatalf("neutral SELL capacity must keep safe fixed-target exits separate from short entries, got long exits=%d short entries=%d orders=%v",
			longExitSells, shortEntrySells, executor.orders)
	}
}

func TestDirectionalInventoryGridPlacesExitOnlyAfterEntryFill(t *testing.T) {
	tests := []struct {
		direction string
		bookSide  string
		entrySide string
		exitSide  string
		entry     float64
		exit      float64
		current   float64
	}{
		{direction: "long", bookSide: BookSideLong, entrySide: "BUY", exitSide: "SELL", entry: 99, exit: 101, current: 99},
		{direction: "short", bookSide: BookSideShort, entrySide: "SELL", exitSide: "BUY", entry: 101, exit: 99, current: 101},
	}
	for _, tt := range tests {
		t.Run(tt.direction, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.App.MarketType = "futures"
			cfg.Trading.Symbol = "ETHUSDT"
			cfg.Trading.Direction = tt.direction
			cfg.Trading.PriceInterval = 1
			cfg.Trading.OrderQuantity = 30
			cfg.Trading.BuyWindowSize = 2
			cfg.Trading.SellWindowSize = 2
			cfg.Trading.OrderCleanupThreshold = 10

			executor := &captureExecutor{}
			spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
			if err := spm.Initialize(100, "100.00"); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			if err := spm.AdjustOrders(100); err != nil {
				t.Fatalf("AdjustOrders() error = %v", err)
			}
			var entryReq *OrderRequest
			for _, order := range executor.orders {
				if order.Side != tt.entrySide || order.ReduceOnly {
					t.Fatalf("before fill expected only %s entries, got %+v", tt.entrySide, order)
				}
				if order.Price == tt.entry && entryReq == nil {
					entryReq = order
				}
			}
			if entryReq == nil {
				t.Fatalf("expected entry order at %.2f, orders=%v", tt.entry, executor.orders)
			}

			spm.OnOrderUpdate(OrderUpdate{OrderID: 99, ClientOrderID: entryReq.ClientOrderID, Status: "NEW", Side: tt.entrySide})
			spm.OnOrderUpdate(OrderUpdate{OrderID: 99, ClientOrderID: entryReq.ClientOrderID, Status: "FILLED", ExecutedQty: entryReq.Quantity, Side: tt.entrySide})
			if err := spm.AdjustOrders(tt.current); err != nil {
				t.Fatalf("post-fill AdjustOrders() error = %v", err)
			}

			var exitSeen bool
			for _, order := range executor.orders {
				if order.Side == tt.exitSide && order.ReduceOnly && order.Price == tt.exit {
					exitSeen = true
				}
				if order.Side == tt.exitSide && !order.ReduceOnly {
					t.Fatalf("directional mode should not place opposite entry order: %+v", order)
				}
			}
			if !exitSeen {
				t.Fatalf("expected reduce-only %s exit at %.2f after %s fill, orders=%v", tt.exitSide, tt.exit, tt.entrySide, executor.orders)
			}
		})
	}
}

func TestShortGridPlacesReduceOnlyBuyAfterShortEntryFill(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "SOLUSDC"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.01
	cfg.Trading.OrderQuantity = 20
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 2)
	if err := spm.Initialize(86.79, "86.79"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(86.79); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}

	var shortEntry *OrderRequest
	for _, order := range executor.orders {
		if order.Side == "BUY" {
			t.Fatalf("short mode must not place BUY exits before a short entry fills: %+v", order)
		}
		if order.Side == "SELL" && !order.ReduceOnly && order.Price == 86.80 {
			shortEntry = order
		}
	}
	if shortEntry == nil {
		t.Fatalf("expected initial short entry SELL at 86.80, orders=%v", executor.orders)
	}

	spm.OnOrderUpdate(OrderUpdate{OrderID: 1001, ClientOrderID: shortEntry.ClientOrderID, Status: "NEW", Side: "SELL", Price: 86.80})
	spm.OnOrderUpdate(OrderUpdate{OrderID: 1001, ClientOrderID: shortEntry.ClientOrderID, Status: "FILLED", ExecutedQty: shortEntry.Quantity, AvgPrice: 86.80, Side: "SELL"})

	if err := spm.AdjustOrders(86.79); err != nil {
		t.Fatalf("post-fill AdjustOrders() error = %v", err)
	}

	var matchingExit *OrderRequest
	for _, order := range executor.orders {
		if order.Side == "BUY" && order.ReduceOnly && order.Price == 86.78 {
			matchingExit = order
		}
	}
	if matchingExit == nil {
		t.Fatalf("expected filled short slot 86.80 to place reduce-only BUY at 86.78, orders=%v", executor.orders)
	}
	if matchingExit.Quantity != shortEntry.Quantity {
		t.Fatalf("short exit qty must match the filled slot qty, got %.8f want %.8f", matchingExit.Quantity, shortEntry.Quantity)
	}
}

func TestShortExitKeepsFixedTargetAndWaitsWhenTargetWouldCross(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "SOLUSDC"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.01
	cfg.Trading.OrderQuantity = 20
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 2)
	if err := spm.Initialize(86.86, "86.86"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	slot := spm.getOrCreateSlot(86.87, BookSideShort)
	slot.mu.Lock()
	slot.BookSide = BookSideShort
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.23
	slot.EntryPrice = 86.87
	slot.SlotStatus = SlotStatusFree
	slot.mu.Unlock()

	if err := spm.AdjustOrders(86.84); err != nil {
		t.Fatalf("unsafe AdjustOrders() error = %v", err)
	}
	for _, order := range executor.orders {
		if order.Side == "BUY" && order.ReduceOnly {
			t.Fatalf("unsafe fixed target should wait instead of moving farther, got BUY %.2f orders=%v", order.Price, executor.orders)
		}
	}

	if err := spm.AdjustOrders(86.86); err != nil {
		t.Fatalf("safe AdjustOrders() error = %v", err)
	}
	var matchingExit *OrderRequest
	for _, order := range executor.orders {
		if order.Side == "BUY" && order.ReduceOnly {
			if order.Price != 86.85 {
				t.Fatalf("expected fixed short exit target 86.85, got %.2f orders=%v", order.Price, executor.orders)
			}
			matchingExit = order
		}
	}
	if matchingExit == nil {
		t.Fatalf("expected fixed short exit BUY at 86.85 after target is maker-safe, orders=%v", executor.orders)
	}
}

func TestShortExitQuotaProtectsEveryFilledSlot(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "SOLUSDC"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.01
	cfg.Trading.OrderQuantity = 20
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 2)
	if err := spm.Initialize(86.74, "86.74"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	for i := 0; i < 10; i++ {
		price := roundPrice(86.74-float64(i)*cfg.Trading.PriceInterval, 2)
		slot := spm.getOrCreateSlot(price, BookSideShort)
		slot.mu.Lock()
		slot.BookSide = BookSideShort
		slot.PositionStatus = PositionStatusFilled
		slot.PositionQty = 0.23
		slot.EntryPrice = price
		slot.SlotStatus = SlotStatusFree
		slot.mu.Unlock()
	}

	if err := spm.AdjustOrders(86.74); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}

	var filledEntries []*OrderRequest
	for _, order := range executor.orders {
		if order.Side == "SELL" && !order.ReduceOnly && order.Price >= 86.75 && order.Price <= 86.78 {
			filledEntries = append(filledEntries, order)
		}
	}
	if len(filledEntries) != 4 {
		t.Fatalf("expected four short entries to fill in the test setup, got %d orders=%v", len(filledEntries), executor.orders)
	}
	for i, entry := range filledEntries {
		orderID := int64(1000 + i)
		spm.OnOrderUpdate(OrderUpdate{OrderID: orderID, ClientOrderID: entry.ClientOrderID, Status: "NEW", Side: "SELL", Price: entry.Price})
		spm.OnOrderUpdate(OrderUpdate{OrderID: orderID, ClientOrderID: entry.ClientOrderID, Status: "FILLED", ExecutedQty: entry.Quantity, AvgPrice: entry.Price, Side: "SELL"})
	}

	if err := spm.AdjustOrders(86.79); err != nil {
		t.Fatalf("post-fill AdjustOrders() error = %v", err)
	}

	var filledSlots int
	var protectedSlots int
	var missing []float64
	spm.slots.Range(func(key, value interface{}) bool {
		slot := value.(*InventorySlot)
		slot.mu.RLock()
		defer slot.mu.RUnlock()
		if slot.BookSide != BookSideShort || slot.PositionStatus != PositionStatusFilled || slot.PositionQty <= 0 {
			return true
		}
		filledSlots++
		if spm.slotHasActiveOrder(slot) && slot.OrderSide == "BUY" && !spm.isEntryOrder(slot.OrderSide, slot.BookSide) && slot.OrderPrice < spm.slotEntryPrice(slot) {
			protectedSlots++
			return true
		}
		missing = append(missing, slot.Price)
		return true
	})
	if filledSlots != 14 {
		t.Fatalf("expected 14 filled short slots after four entry fills, got %d", filledSlots)
	}
	if protectedSlots != filledSlots {
		t.Fatalf("every filled short slot needs its own profitable reduce-only BUY, protected=%d filled=%d missing=%v orders=%v",
			protectedSlots, filledSlots, missing, executor.orders)
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("protective short exits must not be canceled to fit a fixed window, canceled=%v", executor.canceled)
	}
}

func TestShortEntryWindowBackfillsPastFilledSlots(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "SOLUSDC"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.01
	cfg.Trading.OrderQuantity = 20
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 50

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 2)
	if err := spm.Initialize(86.68, "86.68"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	for _, price := range []float64{86.69, 86.70} {
		slot := spm.getOrCreateSlot(price, BookSideShort)
		slot.mu.Lock()
		slot.BookSide = BookSideShort
		slot.PositionStatus = PositionStatusFilled
		slot.PositionQty = 0.23
		slot.EntryPrice = price
		slot.SlotStatus = SlotStatusFree
		slot.mu.Unlock()
	}

	if err := spm.AdjustOrders(86.68); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	wantSells := map[float64]bool{}
	for price := 86.71; price <= 86.80+1e-9; price += 0.01 {
		wantSells[roundPrice(price, 2)] = false
	}
	for _, order := range executor.orders {
		if order.Side != "SELL" || order.ReduceOnly {
			continue
		}
		if order.Price == 86.69 || order.Price == 86.70 {
			t.Fatalf("filled short slots must not receive new entry SELL orders, got %+v", order)
		}
		if _, ok := wantSells[order.Price]; ok {
			wantSells[order.Price] = true
		}
	}
	for price, seen := range wantSells {
		if !seen {
			t.Fatalf("short entry window should extend past filled slots and include %.2f, got orders=%v", price, executor.orders)
		}
	}
}

func TestEntryWindowSyncKeepsExtendedShortWindow(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "SOLUSDC"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.01
	cfg.Trading.OrderQuantity = 20
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 50
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 2)
	if err := spm.Initialize(86.68, "86.68"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	for _, price := range []float64{86.69, 86.70} {
		slot := spm.getOrCreateSlot(price, BookSideShort)
		slot.mu.Lock()
		slot.BookSide = BookSideShort
		slot.PositionStatus = PositionStatusFilled
		slot.PositionQty = 0.23
		slot.EntryPrice = price
		slot.SlotStatus = SlotStatusFree
		slot.mu.Unlock()
	}
	var orderID int64 = 100
	for price := 86.71; price <= 86.80+1e-9; price += 0.01 {
		price = roundPrice(price, 2)
		orderID++
		slot := spm.getOrCreateSlot(price, BookSideShort)
		slot.mu.Lock()
		slot.BookSide = BookSideShort
		slot.PositionStatus = PositionStatusEmpty
		slot.OrderID = orderID
		slot.ClientOID = spm.generateClientOrderID(price, "SELL", BookSideShort)
		slot.OrderSide = "SELL"
		slot.OrderStatus = OrderStatusConfirmed
		slot.OrderPrice = price
		slot.SlotStatus = SlotStatusLocked
		slot.mu.Unlock()
	}

	if err := spm.AdjustOrdersWithRebalance(86.68, true); err != nil {
		t.Fatalf("AdjustOrdersWithRebalance() error = %v", err)
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("extended short entry window should be kept by the main window sync, canceled=%v", executor.canceled)
	}
}

func TestLongGridPlacesReduceOnlySellAfterLongEntryFill(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "ANYUSDC"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 0.01
	cfg.Trading.OrderQuantity = 20
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 2)
	if err := spm.Initialize(86.79, "86.79"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(86.79); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}

	var longEntry *OrderRequest
	for _, order := range executor.orders {
		if order.Side == "SELL" {
			t.Fatalf("long mode must not place SELL exits before a long entry fills: %+v", order)
		}
		if order.Side == "BUY" && !order.ReduceOnly && order.Price == 86.78 {
			longEntry = order
		}
	}
	if longEntry == nil {
		t.Fatalf("expected initial long entry BUY at 86.78, orders=%v", executor.orders)
	}

	spm.OnOrderUpdate(OrderUpdate{OrderID: 1001, ClientOrderID: longEntry.ClientOrderID, Status: "NEW", Side: "BUY", Price: 86.78})
	spm.OnOrderUpdate(OrderUpdate{OrderID: 1001, ClientOrderID: longEntry.ClientOrderID, Status: "FILLED", ExecutedQty: longEntry.Quantity, AvgPrice: 86.78, Side: "BUY"})

	if err := spm.AdjustOrders(86.79); err != nil {
		t.Fatalf("post-fill AdjustOrders() error = %v", err)
	}

	var matchingExit *OrderRequest
	for _, order := range executor.orders {
		if order.Side == "SELL" && order.ReduceOnly && order.Price == 86.80 {
			matchingExit = order
		}
	}
	if matchingExit == nil {
		t.Fatalf("expected filled long slot 86.78 to place reduce-only SELL at maker-safe 86.80, orders=%v", executor.orders)
	}
	if matchingExit.Quantity != longEntry.Quantity {
		t.Fatalf("long exit qty must match the filled slot qty, got %.8f want %.8f", matchingExit.Quantity, longEntry.Quantity)
	}
}

func TestDirectionalFuturesPlacesOnlyDirectionalEntryOrders(t *testing.T) {
	for _, direction := range []string{"long", "short"} {
		t.Run(direction, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.App.MarketType = "futures"
			cfg.Trading.Symbol = "ETHUSDT"
			cfg.Trading.Direction = direction
			cfg.Trading.PriceInterval = 1
			cfg.Trading.OrderQuantity = 30
			cfg.Trading.BuyWindowSize = 5
			cfg.Trading.SellWindowSize = 5
			cfg.Trading.OrderCleanupThreshold = 10

			executor := &captureExecutor{}
			spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
			if err := spm.Initialize(2308, "2308.00"); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			if err := spm.AdjustOrders(2308.50); err != nil {
				t.Fatalf("AdjustOrders() error = %v", err)
			}

			wantSide := "BUY"
			wantPrices := map[float64]bool{2308.00: false, 2307.00: false, 2306.00: false, 2305.00: false, 2304.00: false}
			if direction == "short" {
				wantSide = "SELL"
				wantPrices = map[float64]bool{2310.00: false, 2311.00: false, 2312.00: false, 2313.00: false, 2314.00: false}
			}
			for _, order := range executor.orders {
				if order.ReduceOnly {
					continue
				}
				if order.Side != wantSide {
					t.Fatalf("%s should not place opposite entry order: %+v", direction, order)
				}
				if _, ok := wantPrices[order.Price]; ok {
					wantPrices[order.Price] = true
				}
			}
			for price, seen := range wantPrices {
				if !seen {
					t.Fatalf("%s expected %s at %.2f, orders=%v", direction, wantSide, price, executor.orders)
				}
			}
		})
	}
}

func TestDirectionalEntryWindowDoesNotOverfillWithoutRebalance(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(2308, "2308.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(2308); err != nil {
		t.Fatalf("first AdjustOrders() error = %v", err)
	}
	if err := spm.AdjustOrdersWithRebalance(2309.25, false); err != nil {
		t.Fatalf("second AdjustOrders() error = %v", err)
	}

	sellEntries := 0
	for _, order := range executor.orders {
		if order.ReduceOnly {
			continue
		}
		if order.Side == "BUY" {
			t.Fatalf("short mode should not place BUY entry orders: %+v", order)
		}
		if order.Side == "SELL" {
			sellEntries++
			if order.Price == 2314.00 {
				t.Fatalf("sell window is already full; expected no over-window backfill at %.2f without rebalance, orders=%v", order.Price, executor.orders)
			}
		}
	}
	if sellEntries != cfg.Trading.SellWindowSize {
		t.Fatalf("expected sell entries to remain capped at %d, got %d orders=%v", cfg.Trading.SellWindowSize, sellEntries, executor.orders)
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("directional inventory grid should not cancel stale entries on window move, got %v", executor.canceled)
	}
}

func TestAdjustOrdersBackfillsGhostSellSlots(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "neutral"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(2286.19, "2286.19"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	for _, price := range []float64{2287.19, 2288.19, 2289.19, 2290.19, 2291.19} {
		slot := spm.getOrCreateSlot(price, BookSideShort)
		slot.mu.Lock()
		slot.BookSide = BookSideShort
		slot.PositionStatus = PositionStatusEmpty
		slot.SlotStatus = SlotStatusFree
		slot.OrderStatus = OrderStatusPlaced
		slot.OrderSide = "SELL"
		slot.OrderPrice = price
		slot.OrderID = 0
		slot.ClientOID = ""
		slot.mu.Unlock()
	}

	if err := spm.AdjustOrders(2285.79); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	wantSells := map[float64]bool{2287.19: false, 2288.19: false, 2289.19: false, 2290.19: false, 2291.19: false}
	for _, order := range executor.orders {
		if order.ReduceOnly || order.Side != "SELL" {
			continue
		}
		if _, ok := wantSells[order.Price]; ok {
			wantSells[order.Price] = true
		}
	}
	for price, seen := range wantSells {
		if !seen {
			t.Fatalf("expected ghost SELL slot to be refilled at %.2f, orders=%v", price, executor.orders)
		}
	}
}

func TestRealtimeGridUsesLatestPriceWithoutAnchorGap(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "neutral"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(2282.22, "2282.22"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(2282.86); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	var bestBuy, bestSell float64
	for _, order := range executor.orders {
		if order.ReduceOnly {
			continue
		}
		switch order.Side {
		case "BUY":
			if bestBuy == 0 || order.Price > bestBuy {
				bestBuy = order.Price
			}
		case "SELL":
			if bestSell == 0 || order.Price < bestSell {
				bestSell = order.Price
			}
		}
	}
	if bestBuy != 2282.22 || bestSell != 2284.22 {
		t.Fatalf("expected closest orders to stay on anchor-aligned grid at BUY 2282.22 / SELL 2284.22, got BUY %.2f SELL %.2f orders=%v",
			bestBuy, bestSell, executor.orders)
	}
	if bestSell-bestBuy != 2 {
		t.Fatalf("expected first BUY/SELL gap to be exactly 2 intervals, got %.2f", bestSell-bestBuy)
	}
}

func TestAdjustOrdersDoesNotRebalanceOnSubIntervalPriceNoise(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(2292.61, "2292.61"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(2292.61); err != nil {
		t.Fatalf("first AdjustOrders() error = %v", err)
	}
	initialOrderCount := len(executor.orders)
	if initialOrderCount != 5 {
		t.Fatalf("expected initial 5 directional entry orders, got %d", initialOrderCount)
	}

	if err := spm.AdjustOrders(2292.84); err != nil {
		t.Fatalf("second AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("expected no rebalance cancel on sub-interval price noise, got %v", executor.canceled)
	}
	if len(executor.orders) != initialOrderCount {
		t.Fatalf("expected no replacement orders on sub-interval price noise, got %d orders", len(executor.orders))
	}
}

func TestAdjustOrdersDoesNotCancelProtectedExitOnPriceNoise(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.MarketType = "futures"
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 1
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(2298.57, "2298.57"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	slot := spm.getOrCreateSlot(2245.57, BookSideShort)
	clientOID := spm.generateClientOrderID(2245.57, "BUY", BookSideShort)
	slot.mu.Lock()
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.01
	slot.SlotStatus = SlotStatusLocked
	slot.OrderStatus = OrderStatusConfirmed
	slot.OrderSide = "BUY"
	slot.OrderPrice = 2244.57
	slot.OrderID = 1001
	slot.ClientOID = clientOID
	slot.mu.Unlock()

	if err := spm.AdjustOrders(2298.73); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("expected protected exit order to survive sub-interval price noise, canceled=%v", executor.canceled)
	}
}

func TestCanceledExitDoesNotIncrementPostOnlyFailCount(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 1

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(2245.57, "BUY", BookSideShort)
	slot := spm.getOrCreateSlot(2245.57, BookSideShort)
	slot.mu.Lock()
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.01
	slot.SlotStatus = SlotStatusLocked
	slot.OrderStatus = OrderStatusConfirmed
	slot.OrderSide = "BUY"
	slot.OrderPrice = 2244.57
	slot.OrderID = 1001
	slot.ClientOID = clientOID
	slot.mu.Unlock()

	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1001,
		ClientOrderID: clientOID,
		Status:        "CANCELED",
		Side:          "BUY",
	})

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PostOnlyFailCount != 0 {
		t.Fatalf("expected normal cancel not to increment PostOnlyFailCount, got %d", slot.PostOnlyFailCount)
	}
	if slot.PositionStatus != PositionStatusFilled || slot.PositionQty != 0.01 {
		t.Fatalf("expected canceled exit to preserve position, status=%s qty=%.8f", slot.PositionStatus, slot.PositionQty)
	}
}

func TestRejectedExitIncrementsPostOnlyFailCount(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 1

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(2245.57, "BUY", BookSideShort)
	slot := spm.getOrCreateSlot(2245.57, BookSideShort)
	slot.mu.Lock()
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.01
	slot.SlotStatus = SlotStatusLocked
	slot.OrderStatus = OrderStatusConfirmed
	slot.OrderSide = "BUY"
	slot.OrderPrice = 2244.57
	slot.OrderID = 1001
	slot.ClientOID = clientOID
	slot.mu.Unlock()

	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       1001,
		ClientOrderID: clientOID,
		Status:        "REJECTED",
		Side:          "BUY",
	})

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PostOnlyFailCount != 1 {
		t.Fatalf("expected rejected exit to increment PostOnlyFailCount, got %d", slot.PostOnlyFailCount)
	}
}

func TestAdjustOrdersBackfillsEntryWindowAfterSkippingMarketableGrid(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(99.95); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	want := map[float64]bool{99.00: false, 98.00: false, 97.00: false}
	for _, order := range executor.orders {
		if order.Side == "BUY" && !order.ReduceOnly {
			if _, ok := want[order.Price]; ok {
				want[order.Price] = true
			}
		}
	}
	for price, seen := range want {
		if !seen {
			t.Fatalf("expected entry window to be backfilled through %.2f, orders=%v", price, executor.orders)
		}
	}
}

func TestShortEntryBackfillSkipsMarketableGridSlots(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(2275.22, "2275.22"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(2277.23); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	for _, order := range executor.orders {
		if order.ReduceOnly || order.Side != "SELL" {
			continue
		}
		if order.Price <= 2277.23 {
			t.Fatalf("short entry SELL must stay above current market, got order %.2f at current 2277.23 orders=%v",
				order.Price, executor.orders)
		}
	}

	wantSells := map[float64]bool{2278.22: false, 2279.22: false, 2280.22: false, 2281.22: false, 2282.22: false}
	for _, order := range executor.orders {
		if order.ReduceOnly || order.Side != "SELL" {
			continue
		}
		if _, ok := wantSells[order.Price]; ok {
			wantSells[order.Price] = true
		}
	}
	for price, seen := range wantSells {
		if !seen {
			t.Fatalf("expected maker-safe short entry at %.2f, orders=%v", price, executor.orders)
		}
	}
}

func TestTaggedManagerRejectsOtherRobotOrderUpdates(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2
	cfg.Trading.OrderTag = "robot1"

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	otherClientOID := utils.GenerateOrderIDWithTag(99, "BUY", BookSideLong, 2, "robot2")
	if spm.OnOrderUpdate(OrderUpdate{
		OrderID:       77,
		ClientOrderID: otherClientOID,
		Status:        "NEW",
		Side:          "BUY",
	}) {
		t.Fatalf("other robot order update must not trigger adjustment")
	}

	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.RLock()
	orderID := slot.OrderID
	clientOID := slot.ClientOID
	orderStatus := slot.OrderStatus
	slot.mu.RUnlock()
	if orderID != 0 || clientOID != "" || orderStatus != OrderStatusNotPlaced {
		t.Fatalf("other robot update polluted local slot: orderID=%d clientOID=%q status=%s",
			orderID, clientOID, orderStatus)
	}

	legacyClientOID := utils.GenerateOrderIDWithTag(98, "BUY", BookSideLong, 2, "")
	spm.OnOrderUpdate(OrderUpdate{
		OrderID:       78,
		ClientOrderID: legacyClientOID,
		Status:        "NEW",
		Side:          "BUY",
	})
	legacySlot := spm.getOrCreateSlot(98, BookSideLong)
	legacySlot.mu.RLock()
	defer legacySlot.mu.RUnlock()
	if legacySlot.OrderID != 78 || legacySlot.ClientOID != legacyClientOID || legacySlot.OrderStatus != OrderStatusConfirmed {
		t.Fatalf("legacy untagged order should remain compatible, orderID=%d clientOID=%q status=%s",
			legacySlot.OrderID, legacySlot.ClientOID, legacySlot.OrderStatus)
	}
}

func TestAdjustOrdersSkipsEntryWhenQuantityRoundsToZero(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 0.01
	cfg.Trading.MinOrderValue = 0.001
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(10000, "10000.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(10000); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	if len(executor.orders) != 0 {
		t.Fatalf("expected zero-quantity entries to be skipped, got %d orders", len(executor.orders))
	}
}

func TestAdjustOrdersSkipsEntryBelowMinOrderValue(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 4
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 4)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	if len(executor.orders) != 0 {
		t.Fatalf("expected below-min entries to be skipped, got %d orders", len(executor.orders))
	}
}

func TestAdjustOrdersFloorsEntryQuantityToAvoidOversizing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 100
	cfg.Trading.MinOrderValue = 1
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 1
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 2)
	if err := spm.Initialize(30.97, "30.97"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(30.97); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	var order *OrderRequest
	for _, candidate := range executor.orders {
		if candidate.Side == "BUY" {
			order = candidate
			break
		}
	}
	if order == nil {
		t.Fatalf("expected one BUY entry order, got %v", executor.orders)
	}
	if order.Price != 29.97 {
		t.Fatalf("expected entry at 29.97, got %.8f", order.Price)
	}
	if order.Quantity != 3.33 {
		t.Fatalf("expected quantity to floor to 3.33, got %.8f", order.Quantity)
	}
	if order.Price*order.Quantity > cfg.Trading.OrderQuantity {
		t.Fatalf("entry notional exceeded configured amount: %.8f > %.8f", order.Price*order.Quantity, cfg.Trading.OrderQuantity)
	}
}

func TestAdjustOrdersDoesNotPlaceNonPositiveEntryPrices(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(1, "1.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(1); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	for _, order := range executor.orders {
		if order.Price <= 0 {
			t.Fatalf("non-positive order price was placed: %.8f", order.Price)
		}
	}
}

func TestExitOrderKeepsFixedTargetAndWaitsWhenCrossed(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.3
	slot.BookSide = BookSideLong
	slot.SlotStatus = SlotStatusFree
	slot.mu.Unlock()

	if err := spm.AdjustOrders(101); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	for _, order := range executor.orders {
		if order.Side == "SELL" && order.ReduceOnly {
			t.Fatalf("crossed fixed target should wait instead of moving farther, got exit %.2f orders=%v", order.Price, executor.orders)
		}
	}

	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("retry AdjustOrders() error = %v", err)
	}
	for _, order := range executor.orders {
		if order.Side == "SELL" && order.ReduceOnly {
			if order.Price != 101 {
				t.Fatalf("expected fixed exit target 101 after it becomes maker-safe, got %.2f orders=%v", order.Price, executor.orders)
			}
			return
		}
	}
	t.Fatalf("expected reduce-only sell exit order at fixed target, orders=%v", executor.orders)
}

func TestLargeRestoredShortPositionIsSplitAndExitOrdersAreCapped(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 100
	cfg.Trading.MinOrderValue = 20
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	exchange := seededExchange{positions: []*PositionInfo{{
		Symbol:     "ETHUSDT",
		Size:       -1.0,
		EntryPrice: 3000,
		MarkPrice:  3000,
	}}}
	spm := NewSuperPositionManager(cfg, executor, exchange, 2, 4)
	if err := spm.Initialize(3000, "3000.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(3000); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	var reduceOnlyBuys int
	for _, order := range executor.orders {
		if order.Side != "BUY" || !order.ReduceOnly {
			continue
		}
		reduceOnlyBuys++
		maxQty := spm.maxConfiguredOrderQuantity(order.Price)
		if maxQty <= 0 {
			t.Fatalf("max configured quantity should be positive for order price %.2f", order.Price)
		}
		if order.Quantity > maxQty {
			t.Fatalf("restored short exit order was oversized: qty=%.8f max=%.8f order=%+v", order.Quantity, maxQty, order)
		}
		if order.Price*order.Quantity > cfg.Trading.OrderQuantity+0.01 {
			t.Fatalf("restored short exit notional exceeded configured amount: %.8f > %.8f", order.Price*order.Quantity, cfg.Trading.OrderQuantity)
		}
	}
	if reduceOnlyBuys == 0 {
		t.Fatalf("expected capped reduce-only buy orders for restored short, orders=%v", executor.orders)
	}
}

func TestExistingShortPositionIsRestoredAtStartup(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 100
	cfg.Trading.MinOrderValue = 20
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	exchange := seededExchange{positions: []*PositionInfo{{
		Symbol:     "ETHUSDT",
		Size:       -1.0,
		EntryPrice: 3000,
		MarkPrice:  3000,
	}}}
	spm := NewSuperPositionManager(cfg, executor, exchange, 2, 4)
	if err := spm.Initialize(3000, "3000.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(3000); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	foundExit := false
	for _, order := range executor.orders {
		if order.Side == "BUY" && order.ReduceOnly {
			foundExit = true
			break
		}
	}
	if !foundExit {
		t.Fatalf("expected startup short position to be restored into reduce-only BUY exit, orders=%+v", executor.orders)
	}
}

func TestRestoredShortPositionUsesAnchorAlignedGrid(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 50

	executor := &captureExecutor{}
	exchange := seededExchange{positions: []*PositionInfo{{
		Symbol:     "ETHUSDT",
		Size:       -1.19,
		EntryPrice: 2282.66,
		MarkPrice:  2282.60,
	}}}
	spm := NewSuperPositionManager(cfg, executor, exchange, 2, 2)
	if err := spm.Initialize(2282.60, "2282.60"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(2282.60); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	if _, ok := spm.slots.Load(spm.slotKey(2282.66, BookSideShort)); ok {
		t.Fatalf("restored short slot should align to anchor grid, not exchange entry price 2282.66")
	}
	if _, ok := spm.slots.Load(spm.slotKey(2282.60, BookSideShort)); !ok {
		t.Fatalf("expected restored short slot at anchor-aligned grid 2282.60")
	}

	gotExitPrices := make(map[float64]bool)
	for _, order := range executor.orders {
		if order.Side != "BUY" || !order.ReduceOnly {
			continue
		}
		gotExitPrices[order.Price] = true
	}
	for _, price := range []float64{2280.60, 2279.60, 2278.60} {
		if !gotExitPrices[price] {
			t.Fatalf("expected restored short exits to spread across aligned grid prices, missing %.2f got=%v orders=%+v",
				price, gotExitPrices, executor.orders)
		}
	}
}

func TestRestoredLongPositionUsesAnchorAlignedGrid(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 50

	executor := &captureExecutor{}
	exchange := seededExchange{positions: []*PositionInfo{{
		Symbol:     "ETHUSDT",
		Size:       1.19,
		EntryPrice: 2282.66,
		MarkPrice:  2282.60,
	}}}
	spm := NewSuperPositionManager(cfg, executor, exchange, 2, 2)
	if err := spm.Initialize(2282.60, "2282.60"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(2282.60); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	if _, ok := spm.slots.Load(spm.slotKey(2282.66, BookSideLong)); ok {
		t.Fatalf("restored long slot should align to anchor grid, not exchange entry price 2282.66")
	}
	if _, ok := spm.slots.Load(spm.slotKey(2282.60, BookSideLong)); !ok {
		t.Fatalf("expected restored long slot at anchor-aligned grid 2282.60")
	}

	gotExitPrices := make(map[float64]bool)
	for _, order := range executor.orders {
		if order.Side != "SELL" || !order.ReduceOnly {
			continue
		}
		gotExitPrices[order.Price] = true
	}
	for _, price := range []float64{2284.60, 2285.60, 2286.60} {
		if !gotExitPrices[price] {
			t.Fatalf("expected restored long exits to spread across aligned grid prices, missing %.2f got=%v orders=%+v",
				price, gotExitPrices, executor.orders)
		}
	}
}

func TestPositionSnapshotAdoptsRemoteShortByDefault(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 100
	cfg.Trading.MinOrderValue = 20
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 4)
	if err := spm.Initialize(3000, "3000.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	spm.ApplyExchangeSnapshot([]*PositionInfo{{
		Symbol:     "ETHUSDT",
		Size:       -1.0,
		EntryPrice: 3000,
		MarkPrice:  3000,
	}}, nil)
	if err := spm.AdjustOrders(3000); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	foundExit := false
	for _, order := range executor.orders {
		if order.Side == "BUY" && order.ReduceOnly {
			foundExit = true
			break
		}
	}
	if !foundExit {
		t.Fatalf("remote exchange short should be adopted during reconciliation and protected with reduce-only BUY, orders=%+v", executor.orders)
	}
	localLong, localShort := spm.localPositionTotals()
	if localLong != 0 || math.Abs(localShort-1.0) > 0.000001 {
		t.Fatalf("expected adopted local short position 1.0, got long=%.8f short=%.8f", localLong, localShort)
	}
}

func TestPositionSnapshotAddsRemoteShortDeltaNearCurrentGrid(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "HYPEUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.05
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.MinOrderValue = 5
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 50

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 3, 2)
	if err := spm.Initialize(45.277, "45.277"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	oldSlot := spm.getOrCreateSlot(43.777, BookSideShort)
	oldSlot.mu.Lock()
	oldSlot.BookSide = BookSideShort
	oldSlot.PositionStatus = PositionStatusFilled
	oldSlot.PositionQty = 0.69
	oldSlot.EntryPrice = 43.777
	oldSlot.SlotStatus = SlotStatusFree
	oldSlot.mu.Unlock()

	spm.ApplyExchangeSnapshot([]*PositionInfo{{
		Symbol:     "HYPEUSDT",
		Size:       -1.35,
		EntryPrice: 43.777,
		MarkPrice:  45.277,
	}}, nil)

	localLong, localShort := spm.localPositionTotals()
	if localLong != 0 || math.Abs(localShort-1.35) > 0.000001 {
		t.Fatalf("expected local short to be topped up to 1.35, got long=%.8f short=%.8f", localLong, localShort)
	}
	newSlot := spm.getOrCreateSlot(45.277, BookSideShort)
	newSlot.mu.RLock()
	newQty := newSlot.PositionQty
	newStatus := newSlot.PositionStatus
	newSlot.mu.RUnlock()
	if newStatus != PositionStatusFilled || math.Abs(newQty-0.66) > 0.000001 {
		t.Fatalf("expected missing short delta to be restored near current grid at 45.277 with qty 0.66, status=%s qty=%.8f",
			newStatus, newQty)
	}

	if err := spm.AdjustOrders(45.277); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	foundNearExit := false
	for _, order := range executor.orders {
		if order.Side == "BUY" && order.ReduceOnly && order.Price == 45.177 {
			foundNearExit = true
			break
		}
	}
	if !foundNearExit {
		t.Fatalf("expected newly adopted short delta to get a profitable BUY exit two intervals below entry at 45.177, orders=%+v", executor.orders)
	}
}

func TestSnapshotOpenShortExitAdoptsSlotBeforePositionRedistribution(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 100
	cfg.Trading.MinOrderValue = 20
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	exchange := seededExchange{positions: []*PositionInfo{{
		Symbol:     "ETHUSDT",
		Size:       -2,
		EntryPrice: 100,
		MarkPrice:  100,
	}}}
	spm := NewSuperPositionManager(cfg, executor, exchange, 2, 2)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	exitOID := spm.generateClientOrderID(101, "BUY", BookSideShort)
	spm.ApplyExchangeSnapshot([]*PositionInfo{{
		Symbol:     "ETHUSDT",
		Size:       -2,
		EntryPrice: 100,
		MarkPrice:  100,
	}}, []*Order{{
		OrderID:       501,
		ClientOrderID: exitOID,
		Symbol:        "ETHUSDT",
		Side:          "BUY",
		Price:         99,
		Quantity:      1,
		Status:        OrderStatusConfirmed,
	}})

	localLong, localShort := spm.localPositionTotals()
	if localLong != 0 || math.Abs(localShort-2) > 0.000001 {
		t.Fatalf("expected snapshot reconcile to keep total short at remote size 2, got long=%.8f short=%.8f", localLong, localShort)
	}

	slot := spm.getOrCreateSlot(101, BookSideShort)
	slot.mu.RLock()
	status := slot.PositionStatus
	qty := slot.PositionQty
	orderID := slot.OrderID
	orderSide := slot.OrderSide
	slot.mu.RUnlock()
	if status != PositionStatusFilled || math.Abs(qty-1) > 0.000001 || orderID != 501 || orderSide != "BUY" {
		t.Fatalf("expected open reduce-only BUY snapshot to restore its encoded short slot, status=%s qty=%.8f orderID=%d side=%s",
			status, qty, orderID, orderSide)
	}

	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	for _, order := range executor.orders {
		if order.ClientOrderID == exitOID {
			t.Fatalf("existing snapshot exit must not be duplicated, orders=%+v", executor.orders)
		}
	}
}

func TestAdjustOrdersPrioritizesExitOrdersOverEntries(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 4
	cfg.Trading.SellWindowSize = 4
	cfg.Trading.OrderCleanupThreshold = 1

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.3
	slot.BookSide = BookSideLong
	slot.mu.Unlock()

	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	var exitSeen bool
	for _, order := range executor.orders {
		if order.Side == "SELL" && order.ReduceOnly {
			exitSeen = true
		}
	}
	if !exitSeen {
		t.Fatalf("expected exit order to be prioritized, orders=%v", executor.orders)
	}
}

func TestAdjustOrdersPlacesExitEvenWhenEntryThresholdIsFull(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 4
	cfg.Trading.SellWindowSize = 4
	cfg.Trading.OrderCleanupThreshold = 1

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	entrySlot := spm.getOrCreateSlot(98, BookSideLong)
	entrySlot.mu.Lock()
	entrySlot.OrderID = 42
	entrySlot.ClientOID = spm.generateClientOrderID(98, "BUY", BookSideLong)
	entrySlot.OrderSide = "BUY"
	entrySlot.OrderStatus = OrderStatusConfirmed
	entrySlot.SlotStatus = SlotStatusLocked
	entrySlot.BookSide = BookSideLong
	entrySlot.mu.Unlock()

	filledSlot := spm.getOrCreateSlot(99, BookSideLong)
	filledSlot.mu.Lock()
	filledSlot.PositionStatus = PositionStatusFilled
	filledSlot.PositionQty = 0.3
	filledSlot.BookSide = BookSideLong
	filledSlot.SlotStatus = SlotStatusFree
	filledSlot.mu.Unlock()

	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	var exitSeen bool
	for _, order := range executor.orders {
		if order.Side == "SELL" && order.ReduceOnly {
			exitSeen = true
		}
	}
	if !exitSeen {
		t.Fatalf("expected protective exit order, orders=%v", executor.orders)
	}
}

func TestAdjustOrdersCancelsOrphanExitBeforeUsingExitQuota(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 100
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 1
	cfg.Trading.OrderCleanupThreshold = 100

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 2)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	orphan := spm.getOrCreateSlot(98, BookSideShort)
	orphan.mu.Lock()
	orphan.BookSide = BookSideShort
	orphan.PositionStatus = PositionStatusEmpty
	orphan.OrderID = 77
	orphan.ClientOID = spm.generateClientOrderID(98, "BUY", BookSideShort)
	orphan.OrderSide = "BUY"
	orphan.OrderPrice = 96
	orphan.OrderStatus = OrderStatusConfirmed
	orphan.SlotStatus = SlotStatusLocked
	orphan.mu.Unlock()

	filled := spm.getOrCreateSlot(101, BookSideShort)
	filled.mu.Lock()
	filled.BookSide = BookSideShort
	filled.PositionStatus = PositionStatusFilled
	filled.PositionQty = 1
	filled.SlotStatus = SlotStatusFree
	filled.mu.Unlock()

	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 1 || executor.canceled[0] != 77 {
		t.Fatalf("expected orphan exit order 77 to be canceled before consuming quota, canceled=%v", executor.canceled)
	}
	foundProtectiveExit := false
	for _, order := range executor.orders {
		if order.Side == "BUY" && order.ReduceOnly && order.ClientOrderID != orphan.ClientOID {
			foundProtectiveExit = true
			break
		}
	}
	if !foundProtectiveExit {
		t.Fatalf("expected real filled short slot to get a reduce-only BUY after orphan cleanup, orders=%+v", executor.orders)
	}
}

func TestDirectionalAdjustOrdersWaitsForEntryCapacityBeforeBackfillingCurrentWindow(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 50
	cfg.Trading.CleanupBatchSize = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("first AdjustOrders() error = %v", err)
	}

	if err := spm.AdjustOrdersWithRebalance(110, false); err != nil {
		t.Fatalf("second AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("directional inventory grid should keep old entry orders until fill/cleanup, got %v", executor.canceled)
	}

	unexpectedBackfill := false
	for _, order := range executor.orders {
		if order.Side != "BUY" || order.ReduceOnly {
			continue
		}
		switch order.Price {
		case 109, 108, 107:
			unexpectedBackfill = true
		}
	}
	if unexpectedBackfill {
		t.Fatalf("entry side is already full; expected no over-window backfill before rebalance, orders=%v", executor.orders)
	}
}

func TestDirectionalShortDoesNotBackfillWhenSellWindowFull(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 50
	cfg.Trading.CleanupBatchSize = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("first AdjustOrders() error = %v", err)
	}
	if err := spm.AdjustOrdersWithRebalance(99, false); err != nil {
		t.Fatalf("second AdjustOrders() error = %v", err)
	}

	activeShortEntries := 0
	spm.slots.Range(func(key, value interface{}) bool {
		slot := value.(*InventorySlot)
		slot.mu.RLock()
		defer slot.mu.RUnlock()
		if slot.BookSide == BookSideShort && slot.OrderSide == "SELL" && spm.slotHasActiveOrder(slot) {
			activeShortEntries++
		}
		return true
	})
	if activeShortEntries != cfg.Trading.SellWindowSize {
		t.Fatalf("short entry orders must stay capped at sell window size, got %d want %d", activeShortEntries, cfg.Trading.SellWindowSize)
	}
	for _, order := range executor.orders {
		if order.Side == "SELL" && !order.ReduceOnly && order.Price == 100 {
			t.Fatalf("sell window was full; expected no backfill at 100 before stale entries are canceled, orders=%v", executor.orders)
		}
	}
}

func TestDirectionalPriceGridShiftRebalancesEntryWindow(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 6
	cfg.Trading.CleanupBatchSize = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("first AdjustOrders() error = %v", err)
	}
	ageActiveEntryOrders(spm, BookSideShort)
	primeStableEntryWindowSync(t, spm, 95)
	ordersBefore := len(executor.orders)
	if err := spm.AdjustOrdersWithRebalance(95, true); err != nil {
		t.Fatalf("price shift AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 3 {
		t.Fatalf("expected price-grid shift to cancel far entry orders, canceled=%v", executor.canceled)
	}

	wantSells := map[float64]bool{96: false, 97: false, 98: false}
	for _, order := range executor.orders[ordersBefore:] {
		if order.Side != "SELL" || order.ReduceOnly {
			continue
		}
		if _, ok := wantSells[order.Price]; ok {
			wantSells[order.Price] = true
		}
	}
	for price, seen := range wantSells {
		if !seen {
			t.Fatalf("expected shifted short entry order %.2f to be posted, new orders=%v", price, executor.orders[ordersBefore:])
		}
	}
}

func TestEntryWindowHoleCancelsOldFarEntryAndBackfillsNearSlot(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 6
	cfg.Trading.CleanupBatchSize = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}

	slot101 := spm.getOrCreateSlot(101, BookSideShort)
	slot101.mu.Lock()
	slot101.PositionStatus = PositionStatusFilled
	slot101.PositionQty = 0.3
	slot101.SlotStatus = SlotStatusFree
	clearSlotOrderTracking(slot101, OrderStatusFilled)
	slot101.mu.Unlock()

	if err := spm.AdjustOrdersWithRebalance(100, false); err != nil {
		t.Fatalf("post-fill AdjustOrders() error = %v", err)
	}

	slot101.mu.Lock()
	slot101.PositionStatus = PositionStatusEmpty
	slot101.PositionQty = 0
	slot101.SlotStatus = SlotStatusFree
	clearSlotOrderTracking(slot101, OrderStatusFilled)
	slot101.mu.Unlock()

	slot104 := spm.getOrCreateSlot(104, BookSideShort)
	slot104.mu.Lock()
	farOrderID := slot104.OrderID
	slot104.OrderCreatedAt = time.Now().Add(-entryWindowSyncMinAge - time.Second)
	slot104.mu.Unlock()
	if farOrderID == 0 {
		t.Fatalf("expected far entry order at 104, orders=%v", executor.orders)
	}

	primeStableEntryWindowSync(t, spm, 100)
	ordersBefore := len(executor.orders)
	if err := spm.AdjustOrdersWithRebalance(100, true); err != nil {
		t.Fatalf("hole repair AdjustOrders() error = %v", err)
	}

	foundCancel := false
	for _, orderID := range executor.canceled {
		if orderID == farOrderID {
			foundCancel = true
			break
		}
	}
	if !foundCancel {
		t.Fatalf("expected far entry order %d to be canceled, canceled=%v", farOrderID, executor.canceled)
	}
	foundBackfill := false
	for _, order := range executor.orders[ordersBefore:] {
		if order.Side == "SELL" && !order.ReduceOnly && order.Price == 101 {
			foundBackfill = true
			break
		}
	}
	if !foundBackfill {
		t.Fatalf("expected near entry hole at 101 to be backfilled, new orders=%v", executor.orders[ordersBefore:])
	}
}

func TestEntryWindowHoleRepairCancelsAllAvailableFarEntriesInOnePass(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 1

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}

	farOrderIDs := make(map[int64]bool)
	for _, price := range []float64{104, 105} {
		slot := spm.getOrCreateSlot(price, BookSideShort)
		slot.mu.Lock()
		if slot.OrderID == 0 {
			slot.mu.Unlock()
			t.Fatalf("expected far entry order at %.2f, orders=%v", price, executor.orders)
		}
		farOrderIDs[slot.OrderID] = false
		slot.OrderCreatedAt = time.Now().Add(-entryWindowSyncMinAge - time.Second)
		slot.mu.Unlock()
	}

	primeStableEntryWindowSync(t, spm, 98)
	ordersBefore := len(executor.orders)
	if err := spm.AdjustOrdersWithRebalance(98, true); err != nil {
		t.Fatalf("hole repair AdjustOrders() error = %v", err)
	}

	if len(executor.canceled) != len(farOrderIDs) {
		t.Fatalf("expected all far entries to be canceled in one pass despite cleanup batch size, canceled=%v", executor.canceled)
	}
	for _, orderID := range executor.canceled {
		if _, ok := farOrderIDs[orderID]; ok {
			farOrderIDs[orderID] = true
		}
	}
	for orderID, seen := range farOrderIDs {
		if !seen {
			t.Fatalf("expected far entry order %d to be canceled, canceled=%v", orderID, executor.canceled)
		}
	}

	backfilled := map[float64]bool{99: false, 100: false}
	for _, order := range executor.orders[ordersBefore:] {
		if order.Side == "SELL" && !order.ReduceOnly {
			if _, ok := backfilled[order.Price]; ok {
				backfilled[order.Price] = true
			}
		}
	}
	for price, seen := range backfilled {
		if !seen {
			t.Fatalf("expected near entry hole %.2f to be backfilled, new orders=%v", price, executor.orders[ordersBefore:])
		}
	}
}

func TestPriceGridShiftBackfillsNearestEntrySlot(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(2266.32, "2266.32"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(2266.32); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}
	ageActiveEntryOrders(spm, BookSideShort)

	primeStableEntryWindowSync(t, spm, 2265.30)
	ordersBefore := len(executor.orders)
	if err := spm.AdjustOrdersWithRebalance(2265.30, true); err != nil {
		t.Fatalf("first grid shift AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 1 {
		t.Fatalf("expected one far entry to be canceled on grid shift, canceled=%v", executor.canceled)
	}
	found := false
	for _, order := range executor.orders[ordersBefore:] {
		if order.Side == "SELL" && !order.ReduceOnly && order.Price == 2266.32 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected short entry window to backfill nearest slot 2266.32, new orders=%v", executor.orders[ordersBefore:])
	}
}

func TestEntryWindowSyncWaitsForStableTargetBeforeCanceling(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}
	ageActiveEntryOrders(spm, BookSideShort)

	if err := spm.AdjustOrdersWithRebalance(110, true); err != nil {
		t.Fatalf("first upward shift AdjustOrders() error = %v", err)
	}
	if err := spm.AdjustOrdersWithRebalance(100, true); err != nil {
		t.Fatalf("oscillating back AdjustOrders() error = %v", err)
	}
	if err := spm.AdjustOrdersWithRebalance(110, true); err != nil {
		t.Fatalf("second upward shift AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("unstable entry window must not churn cancel/repost, canceled=%v", executor.canceled)
	}
}

func TestShortEntryWindowDoesNotCancelCloserOrderForFarEdgeHole(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "SOLUSDC"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.01
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 4, 2)
	if err := spm.Initialize(86.25, "86.2500"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(86.25); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}
	ageActiveEntryOrders(spm, BookSideShort)

	for i := 0; i < 3; i++ {
		if err := spm.AdjustOrdersWithRebalance(86.26, true); err != nil {
			t.Fatalf("up one tick AdjustOrdersWithRebalance() error = %v", err)
		}
		spm.mu.Lock()
		spm.pendingEntryWindowSyncSeen = time.Now().Add(-entryWindowStableDuration - time.Second)
		spm.mu.Unlock()
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("one-tick short rise must not cancel closer 86.26 order to fill far edge, canceled=%v", executor.canceled)
	}
}

func TestShortEntryWindowSingleTickRollBackWaitsForFarStability(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "SOLUSDC"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 0.01
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 20
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 4, 2)
	if err := spm.Initialize(86.46, "86.4600"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(86.46); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}
	ageActiveEntryOrders(spm, BookSideShort)

	primeFarStableEntryWindowSync(t, spm, 86.47)
	if err := spm.AdjustOrdersWithRebalance(86.47, true); err != nil {
		t.Fatalf("up one tick AdjustOrdersWithRebalance() error = %v", err)
	}
	if len(executor.canceled) != 1 {
		t.Fatalf("expected one old edge entry to be canceled after far-stable upward roll, canceled=%v", executor.canceled)
	}
	firstCancelCount := len(executor.canceled)
	ageActiveEntryOrders(spm, BookSideShort)
	spm.mu.Lock()
	spm.lastEntryWindowSync = time.Now().Add(-entryWindowSyncCooldown - time.Second)
	spm.mu.Unlock()

	if err := spm.AdjustOrdersWithRebalance(86.46, true); err != nil {
		t.Fatalf("first roll-back AdjustOrdersWithRebalance() error = %v", err)
	}
	spm.mu.Lock()
	spm.pendingEntryWindowSyncSeen = time.Now().Add(-entryWindowStableDuration - time.Second)
	spm.mu.Unlock()
	if err := spm.AdjustOrdersWithRebalance(86.46, true); err != nil {
		t.Fatalf("second roll-back AdjustOrdersWithRebalance() error = %v", err)
	}
	if len(executor.canceled) != firstCancelCount {
		t.Fatalf("single-tick rollback should wait for far stability before reversing edge order, canceled=%v", executor.canceled)
	}
}

func TestEntryWindowStableRepairRunsDuringRegularAdjust(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}
	ageActiveEntryOrders(spm, BookSideShort)

	primeStableEntryWindowSync(t, spm, 98)
	ordersBefore := len(executor.orders)
	if err := spm.AdjustOrdersWithRebalance(98, false); err != nil {
		t.Fatalf("regular AdjustOrdersWithRebalance() error = %v", err)
	}

	if len(executor.canceled) == 0 {
		t.Fatalf("stable entry repair should run even on regular adjust")
	}
	foundBackfill := false
	for _, order := range executor.orders[ordersBefore:] {
		if order.Side == "SELL" && !order.ReduceOnly && order.Price == 99 {
			foundBackfill = true
			break
		}
	}
	if !foundBackfill {
		t.Fatalf("expected regular adjust to backfill nearest short entry slot, new orders=%v", executor.orders[ordersBefore:])
	}
}

func TestPriceGridShiftTracksNearestEntrySlotUpward(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 5
	cfg.Trading.SellWindowSize = 5
	cfg.Trading.OrderCleanupThreshold = 10
	cfg.Trading.CleanupBatchSize = 20

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(2273.41, "2273.41"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(2273.41); err != nil {
		t.Fatalf("initial AdjustOrders() error = %v", err)
	}
	ageActiveEntryOrders(spm, BookSideShort)

	primeFarStableEntryWindowSync(t, spm, 2274.70)
	ordersBefore := len(executor.orders)
	if err := spm.AdjustOrdersWithRebalance(2274.70, true); err != nil {
		t.Fatalf("full interval AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 1 {
		t.Fatalf("expected one old near entry to be canceled when grid moves upward, canceled=%v", executor.canceled)
	}
	found := false
	for _, order := range executor.orders[ordersBefore:] {
		if order.Side == "SELL" && !order.ReduceOnly && order.Price == 2279.41 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected short entry window to extend to 2279.41, new orders=%v", executor.orders[ordersBefore:])
	}
}

func TestAggressiveModeKeepsDirectionalEntryOrdersOnGridShift(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Mode = "aggressive"
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 6
	cfg.Trading.CleanupBatchSize = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("first AdjustOrders() error = %v", err)
	}
	if err := spm.AdjustOrdersWithRebalance(95, true); err != nil {
		t.Fatalf("price shift AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != 0 {
		t.Fatalf("aggressive mode should keep old directional entry orders, got canceled=%v", executor.canceled)
	}
}

func TestAggressiveModePrioritizesEntryOrdersBeforeExitOrders(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Mode = "aggressive"
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 1
	cfg.Trading.OrderCleanupThreshold = 3

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)

	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	slot := spm.getOrCreateSlot(101, BookSideLong)
	slot.mu.Lock()
	slot.BookSide = BookSideLong
	slot.Price = 101
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.3
	slot.EntryPrice = 101
	slot.SlotStatus = SlotStatusFree
	slot.mu.Unlock()

	if err := spm.AdjustOrders(100.4); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	entryCount := 0
	exitCount := 0
	for i, order := range executor.orders {
		if order.ReduceOnly {
			exitCount++
			if i < 2 {
				t.Fatalf("aggressive mode should place entry orders before exits, got early exit order at index %d: %+v", i, order)
			}
			continue
		}
		if order.Side == "BUY" {
			entryCount++
		}
	}
	if entryCount != 2 {
		t.Fatalf("aggressive mode should fill entry quota first, got entry=%d orders=%v", entryCount, executor.orders)
	}
	if exitCount != 1 {
		t.Fatalf("aggressive mode should place the remaining exit after filling entry quota, got exit=%d orders=%v", exitCount, executor.orders)
	}
}

func TestAdjustOrdersDoesNotDuplicateWhenExchangeReturnsBlankClientOID(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{blankOID: true}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("first AdjustOrders() error = %v", err)
	}
	firstCount := len(executor.orders)
	if firstCount == 0 {
		t.Fatalf("expected first adjust to place orders")
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("second AdjustOrders() error = %v", err)
	}
	if len(executor.orders) != firstCount {
		t.Fatalf("expected second adjust not to duplicate blank-clientOID orders, first=%d total=%d",
			firstCount, len(executor.orders))
	}
	for _, req := range executor.orders {
		_, _, bookSide, valid := spm.parseClientOrderID(req.ClientOrderID)
		if !valid {
			t.Fatalf("invalid request clientOID %q", req.ClientOrderID)
		}
		slot := spm.getOrCreateSlot(req.Price, bookSide)
		slot.mu.RLock()
		gotOID := slot.ClientOID
		gotStatus := slot.SlotStatus
		slot.mu.RUnlock()
		if gotOID != req.ClientOrderID || gotStatus != SlotStatusLocked {
			t.Fatalf("expected slot %v locked with request clientOID, got oid=%q status=%s want oid=%q",
				req.Price, gotOID, gotStatus, req.ClientOrderID)
		}
	}
}

func TestPendingOrderWithoutClientOIDBlocksDuplicateExitPrice(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 1000
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	pendingSlot := spm.getOrCreateSlot(103, BookSideShort)
	pendingSlot.mu.Lock()
	pendingSlot.BookSide = BookSideShort
	pendingSlot.PositionStatus = PositionStatusFilled
	pendingSlot.PositionQty = 1
	pendingSlot.SlotStatus = SlotStatusPending
	pendingSlot.OrderStatus = OrderStatusPlaced
	pendingSlot.OrderSide = "BUY"
	pendingSlot.OrderPrice = 99.4
	pendingSlot.OrderCreatedAt = time.Now()
	pendingSlot.mu.Unlock()

	candidateSlot := spm.getOrCreateSlot(102, BookSideShort)
	candidateSlot.mu.Lock()
	candidateSlot.BookSide = BookSideShort
	candidateSlot.PositionStatus = PositionStatusFilled
	candidateSlot.PositionQty = 1
	candidateSlot.SlotStatus = SlotStatusFree
	candidateSlot.mu.Unlock()

	if err := spm.AdjustOrders(100.4); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	for _, order := range executor.orders {
		if order.ReduceOnly && order.Side == "BUY" && order.Price == 99.4 {
			t.Fatalf("pending unconfirmed order must block duplicate BUY exit at 99.40, orders=%v", executor.orders)
		}
	}
}

func TestCleanupGhostOrderStatesClearsStalePendingWithoutIdentity(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.BookSide = BookSideLong
	slot.PositionStatus = PositionStatusEmpty
	slot.SlotStatus = SlotStatusPending
	slot.OrderStatus = OrderStatusPlaced
	slot.OrderSide = "BUY"
	slot.OrderPrice = 99
	slot.OrderCreatedAt = time.Now().Add(-pendingOrderIdentityGrace - time.Second)
	slot.mu.Unlock()

	spm.cleanupGhostOrderStates()

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.SlotStatus != SlotStatusFree || slot.OrderStatus != OrderStatusNotPlaced || slot.OrderSide != "" || slot.OrderPrice != 0 {
		t.Fatalf("expected stale pending order state to be cleared, slot=%s status=%s side=%q price=%.2f",
			slot.SlotStatus, slot.OrderStatus, slot.OrderSide, slot.OrderPrice)
	}
}

func TestAdjustOrdersWaitsForUnsafeFixedExitTargets(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 1000
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	for _, price := range []float64{98, 99, 100} {
		slot := spm.getOrCreateSlot(price, BookSideLong)
		slot.mu.Lock()
		slot.BookSide = BookSideLong
		slot.PositionStatus = PositionStatusFilled
		slot.PositionQty = 1
		slot.EntryPrice = price
		slot.SlotStatus = SlotStatusFree
		slot.mu.Unlock()
	}

	if err := spm.AdjustOrders(101); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	var exitCountAtTarget int
	for _, order := range executor.orders {
		if order.ReduceOnly && order.Side == "SELL" {
			if order.Price != 102 {
				t.Fatalf("expected only maker-safe fixed target 102.00, got %.2f orders=%v", order.Price, executor.orders)
			}
			exitCountAtTarget++
		}
	}
	if exitCountAtTarget != 1 {
		t.Fatalf("expected unsafe fixed targets to wait, got %d reduce-only exits orders=%v", exitCountAtTarget, executor.orders)
	}
}

func TestApplyExchangeSnapshotAllowsDuplicateSamePriceExitOrders(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 1000
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	keepOID := spm.generateClientOrderID(100, "SELL", BookSideLong)
	duplicateOID := spm.generateClientOrderID(99, "SELL", BookSideLong)
	keepSlot := spm.getOrCreateSlot(100, BookSideLong)
	keepSlot.mu.Lock()
	keepSlot.BookSide = BookSideLong
	keepSlot.PositionStatus = PositionStatusFilled
	keepSlot.PositionQty = 1
	keepSlot.OrderID = 11
	keepSlot.ClientOID = keepOID
	keepSlot.OrderSide = "SELL"
	keepSlot.OrderStatus = OrderStatusConfirmed
	keepSlot.OrderPrice = 101
	keepSlot.SlotStatus = SlotStatusLocked
	keepSlot.mu.Unlock()

	spm.ApplyExchangeSnapshot(nil, []*Order{
		{OrderID: 11, ClientOrderID: keepOID, Side: "SELL", Status: OrderStatusConfirmed, Price: 101, Quantity: 1},
		{OrderID: 12, ClientOrderID: duplicateOID, Side: "SELL", Status: OrderStatusConfirmed, Price: 101, Quantity: 1},
	})

	if len(executor.canceled) != 0 {
		t.Fatalf("same-price reduce-only exits should be kept, got canceled %v", executor.canceled)
	}
	duplicateSlot := spm.getOrCreateSlot(99, BookSideLong)
	duplicateSlot.mu.RLock()
	defer duplicateSlot.mu.RUnlock()
	if duplicateSlot.OrderID != 12 || duplicateSlot.ClientOID != duplicateOID {
		t.Fatalf("duplicate same-price exit order should be adopted locally, orderID=%d clientOID=%q",
			duplicateSlot.OrderID, duplicateSlot.ClientOID)
	}
}

func TestAdjustOrdersCancelsConflictingSamePriceOrder(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "short"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 1
	cfg.Trading.OrderCleanupThreshold = 10

	executor := &hookExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	oldOID := int64(111)
	oldClientOID := "old-client-oid"
	executor.beforeReturn = func(req *OrderRequest, orderID int64) {
		price, _, bookSide, valid := spm.parseClientOrderID(req.ClientOrderID)
		if !valid {
			t.Fatalf("invalid clientOID generated: %s", req.ClientOrderID)
		}
		slot := spm.getOrCreateSlot(price, bookSide)
		slot.mu.Lock()
		slot.BookSide = bookSide
		slot.PositionStatus = PositionStatusEmpty
		slot.OrderID = oldOID
		slot.ClientOID = oldClientOID
		slot.OrderSide = req.Side
		slot.OrderStatus = OrderStatusConfirmed
		slot.OrderPrice = req.Price
		slot.SlotStatus = SlotStatusLocked
		slot.mu.Unlock()
	}

	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) == 0 {
		t.Fatalf("expected conflicting new order to be canceled, got %v", executor.canceled)
	}
	for _, orderID := range executor.canceled {
		if orderID == oldOID {
			t.Fatalf("must cancel the new conflicting order, not the original tracked order")
		}
	}

	slot := spm.getOrCreateSlot(101, BookSideShort)
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.OrderID != oldOID || slot.ClientOID != oldClientOID {
		t.Fatalf("slot was overwritten by conflicting order: orderID=%d clientOID=%q",
			slot.OrderID, slot.ClientOID)
	}
}

func TestApplyExchangeSnapshotAppliesMissedOpenEntryFill(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)

	spm.ApplyExchangeSnapshot(nil, []struct {
		OrderID       int64
		ClientOrderID string
		Side          string
		Status        string
		Price         float64
		Quantity      float64
		ExecutedQty   float64
	}{
		{OrderID: 1, ClientOrderID: clientOID, Side: "BUY", Status: "PARTIALLY_FILLED", Price: 99, Quantity: 0.5, ExecutedQty: 0.2},
	})

	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PositionStatus != PositionStatusFilled || slot.PositionQty != 0.2 || slot.OrderFilledQty != 0.2 {
		t.Fatalf("expected snapshot partial entry fill to update position, status=%s pos=%.8f filled=%.8f",
			slot.PositionStatus, slot.PositionQty, slot.OrderFilledQty)
	}
	if got := spm.GetTotalBuyQty(); got != 0.2 {
		t.Fatalf("expected total buy qty 0.2, got %.8f", got)
	}
}

func TestApplyExchangeSnapshotAppliesMissedOpenExitFill(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "SELL", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.PositionStatus = PositionStatusFilled
	slot.PositionQty = 0.5
	slot.EntryPrice = 98.5
	slot.BookSide = BookSideLong
	slot.OrderID = 1
	slot.ClientOID = clientOID
	slot.OrderSide = "SELL"
	slot.OrderStatus = OrderStatusConfirmed
	slot.SlotStatus = SlotStatusLocked
	slot.mu.Unlock()

	spm.ApplyExchangeSnapshot(nil, []struct {
		OrderID       int64
		ClientOrderID string
		Side          string
		Status        string
		Price         float64
		Quantity      float64
		ExecutedQty   float64
	}{
		{OrderID: 1, ClientOrderID: clientOID, Side: "SELL", Status: "PARTIALLY_FILLED", Price: 100, Quantity: 0.5, ExecutedQty: 0.2},
	})

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.PositionStatus != PositionStatusFilled || slot.PositionQty != 0.3 || slot.OrderFilledQty != 0.2 {
		t.Fatalf("expected snapshot partial exit fill to reduce position, status=%s pos=%.8f filled=%.8f",
			slot.PositionStatus, slot.PositionQty, slot.OrderFilledQty)
	}
	if got := spm.GetTotalSellQty(); got != 0.2 {
		t.Fatalf("expected total sell qty 0.2, got %.8f", got)
	}
	assertFloatNear(t, spm.GetRealizedPNL(), gridRealizedPNL(98.5, 100, 0.2, BookSideLong, config.DefaultFeeRate))
}

func TestApplyExchangeSnapshotClearsGoneStaleOrder(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, goneOrderExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.OrderID = 11
	slot.ClientOID = clientOID
	slot.OrderSide = "BUY"
	slot.OrderStatus = OrderStatusConfirmed
	slot.SlotStatus = SlotStatusLocked
	slot.BookSide = BookSideLong
	slot.mu.Unlock()

	spm.ApplyExchangeSnapshot(nil, []*Order{})

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.OrderID != 0 || slot.ClientOID != "" || slot.SlotStatus != SlotStatusFree || slot.OrderStatus != OrderStatusCanceled {
		t.Fatalf("expected stale gone order to be cleared, orderID=%d clientOID=%q slot=%s status=%s",
			slot.OrderID, slot.ClientOID, slot.SlotStatus, slot.OrderStatus)
	}
}

func TestApplyExchangeSnapshotClearsGoneClientOnlyOrder(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.ClientOID = clientOID
	slot.OrderSide = "BUY"
	slot.OrderStatus = OrderStatusPlaced
	slot.OrderPrice = 99
	slot.OrderCreatedAt = time.Now().Add(-pendingOrderIdentityGrace - time.Second)
	slot.SlotStatus = SlotStatusLocked
	slot.BookSide = BookSideLong
	slot.mu.Unlock()

	spm.ApplyExchangeSnapshot(nil, []*Order{})

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.OrderID != 0 || slot.ClientOID != "" || slot.OrderSide != "" || slot.OrderPrice != 0 ||
		!slot.OrderCreatedAt.IsZero() || slot.SlotStatus != SlotStatusFree || slot.OrderStatus != OrderStatusCanceled {
		t.Fatalf("expected client-only stale order to be fully cleared, orderID=%d clientOID=%q side=%q price=%.2f created=%v slot=%s status=%s",
			slot.OrderID, slot.ClientOID, slot.OrderSide, slot.OrderPrice, slot.OrderCreatedAt, slot.SlotStatus, slot.OrderStatus)
	}
}

func TestApplyExchangeSnapshotKeepsRecentClientOnlyOrder(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.ClientOID = clientOID
	slot.OrderSide = "BUY"
	slot.OrderStatus = OrderStatusPlaced
	slot.OrderPrice = 99
	slot.OrderCreatedAt = time.Now()
	slot.SlotStatus = SlotStatusLocked
	slot.BookSide = BookSideLong
	slot.mu.Unlock()

	spm.ApplyExchangeSnapshot(nil, []*Order{})

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.ClientOID != clientOID || slot.OrderSide != "BUY" || slot.OrderPrice != 99 ||
		slot.SlotStatus != SlotStatusLocked || slot.OrderStatus != OrderStatusPlaced {
		t.Fatalf("recent client-only order should be kept during identity grace, clientOID=%q side=%q price=%.2f slot=%s status=%s",
			slot.ClientOID, slot.OrderSide, slot.OrderPrice, slot.SlotStatus, slot.OrderStatus)
	}
}

func TestFailedPlacementRollbackClearsOrderCreatedAt(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 1
	cfg.Trading.SellWindowSize = 1
	cfg.Trading.OrderCleanupThreshold = 10

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	if err := spm.Initialize(100, "100.00"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := spm.AdjustOrders(100); err != nil {
		t.Fatalf("AdjustOrders() error = %v", err)
	}

	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.SlotStatus != SlotStatusFree || slot.OrderStatus != OrderStatusNotPlaced || slot.ClientOID != "" ||
		slot.OrderSide != "" || slot.OrderPrice != 0 || !slot.OrderCreatedAt.IsZero() {
		t.Fatalf("failed placement rollback left dirty order state: slot=%s status=%s clientOID=%q side=%q price=%.2f created=%v",
			slot.SlotStatus, slot.OrderStatus, slot.ClientOID, slot.OrderSide, slot.OrderPrice, slot.OrderCreatedAt)
	}
}

func TestUpdateSlotOrderStatusClearsFullOrderTracking(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1

	spm := NewSuperPositionManager(cfg, noopExecutor{}, noopExchange{}, 2, 3)
	clientOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.OrderID = 11
	slot.ClientOID = clientOID
	slot.OrderSide = "BUY"
	slot.OrderStatus = OrderStatusConfirmed
	slot.OrderPrice = 99
	slot.OrderFilledQty = 0.1
	slot.OrderCreatedAt = time.Now()
	slot.SlotStatus = SlotStatusLocked
	slot.BookSide = BookSideLong
	slot.mu.Unlock()

	spm.UpdateSlotOrderStatusIfCurrent(99, BookSideLong, OrderStatusCanceled, 11, clientOID)

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.OrderID != 0 || slot.ClientOID != "" || slot.OrderSide != "" || slot.OrderPrice != 0 ||
		slot.OrderFilledQty != 0 || !slot.OrderCreatedAt.IsZero() || slot.SlotStatus != SlotStatusFree {
		t.Fatalf("expected canceled status update to clear all tracking, orderID=%d clientOID=%q side=%q price=%.2f filled=%.8f created=%v slot=%s",
			slot.OrderID, slot.ClientOID, slot.OrderSide, slot.OrderPrice, slot.OrderFilledQty, slot.OrderCreatedAt, slot.SlotStatus)
	}
}

func TestApplyExchangeSnapshotDoesNotOverwriteActiveSlotWithConflictingOrder(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 2
	cfg.Trading.SellWindowSize = 2

	executor := &captureExecutor{}
	spm := NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)
	currentOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	conflictOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.OrderID = 11
	slot.ClientOID = currentOID
	slot.OrderSide = "BUY"
	slot.OrderStatus = OrderStatusConfirmed
	slot.OrderPrice = 99
	slot.SlotStatus = SlotStatusLocked
	slot.BookSide = BookSideLong
	slot.mu.Unlock()

	spm.ApplyExchangeSnapshot(nil, []*Order{
		{OrderID: 11, ClientOrderID: currentOID, Side: "BUY", Status: OrderStatusConfirmed, Price: 99, Quantity: 0.3},
		{OrderID: 12, ClientOrderID: conflictOID, Side: "BUY", Status: OrderStatusConfirmed, Price: 99, Quantity: 0.3},
	})

	slot.mu.RLock()
	gotOrderID := slot.OrderID
	gotClientOID := slot.ClientOID
	slot.mu.RUnlock()
	if gotOrderID != 11 || gotClientOID != currentOID {
		t.Fatalf("snapshot conflict must not overwrite current tracked order, got orderID=%d clientOID=%q",
			gotOrderID, gotClientOID)
	}
	if len(executor.canceled) != 1 || executor.canceled[0] != 12 {
		t.Fatalf("expected conflicting snapshot order 12 to be canceled, got %v", executor.canceled)
	}
}

func TestCancelEntryOrdersDoesNotClobberReplacementSlotOrder(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30

	var spm *SuperPositionManager
	executor := &cancelHookExecutor{}
	spm = NewSuperPositionManager(cfg, executor, noopExchange{}, 2, 3)

	oldEntryOID := spm.generateClientOrderID(99, "BUY", BookSideLong)
	replacementExitOID := spm.generateClientOrderID(99, "SELL", BookSideLong)
	slot := spm.getOrCreateSlot(99, BookSideLong)
	slot.mu.Lock()
	slot.BookSide = BookSideLong
	slot.OrderID = 11
	slot.ClientOID = oldEntryOID
	slot.OrderSide = "BUY"
	slot.OrderStatus = OrderStatusConfirmed
	slot.OrderPrice = 99
	slot.SlotStatus = SlotStatusLocked
	slot.PositionStatus = PositionStatusEmpty
	slot.mu.Unlock()

	executor.onCancel = func(orderIDs []int64) {
		slot.mu.Lock()
		defer slot.mu.Unlock()
		slot.OrderID = 22
		slot.ClientOID = replacementExitOID
		slot.OrderSide = "SELL"
		slot.OrderStatus = OrderStatusConfirmed
		slot.OrderPrice = 100
		slot.SlotStatus = SlotStatusLocked
		slot.PositionStatus = PositionStatusFilled
		slot.PositionQty = 0.3
	}

	spm.CancelEntryOrders()

	slot.mu.RLock()
	defer slot.mu.RUnlock()
	if slot.OrderID != 22 || slot.ClientOID != replacementExitOID || slot.OrderStatus != OrderStatusConfirmed || slot.OrderSide != "SELL" {
		t.Fatalf("old entry cancel must not clobber replacement exit order, orderID=%d clientOID=%q side=%s status=%s",
			slot.OrderID, slot.ClientOID, slot.OrderSide, slot.OrderStatus)
	}
}

func TestGridRealizedPNLUsesActualExitPrice(t *testing.T) {
	assertFloatNear(t, gridRealizedPNL(3000, 3002, 0.5, BookSideLong, 0), 1)
	assertFloatNear(t, gridRealizedPNL(3000, 2997, 0.5, BookSideShort, 0), 1.5)
}

func TestGridRealizedPNLSubtractsRoundTripFees(t *testing.T) {
	got := gridRealizedPNL(3000, 3001, 0.01, BookSideLong, 0.001)
	assertFloatNear(t, got, -0.05001)
}

func assertFloatNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f want %.12f", got, want)
	}
}
