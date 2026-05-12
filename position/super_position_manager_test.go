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

type seededExchange struct {
	noopExchange
	positions []*PositionInfo
}

func (e seededExchange) GetPositions(ctx context.Context, symbol string) (interface{}, error) {
	return e.positions, nil
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
		{direction: "long", bookSide: BookSideLong, entrySide: "BUY", exitSide: "SELL", entry: 99, exit: 100, current: 99},
		{direction: "short", bookSide: BookSideShort, entrySide: "SELL", exitSide: "BUY", entry: 101, exit: 100, current: 101},
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

func TestDirectionalEntryWindowBackfillsLatestPriceWithoutRebalance(t *testing.T) {
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

	wantSells := map[float64]bool{2309.00: false, 2310.00: false, 2311.00: false, 2312.00: false, 2313.00: false, 2314.00: false}
	for _, order := range executor.orders {
		if order.ReduceOnly {
			continue
		}
		if order.Side == "BUY" {
			t.Fatalf("short mode should not place BUY entry orders: %+v", order)
		}
		if order.Side == "SELL" {
			if _, ok := wantSells[order.Price]; ok {
				wantSells[order.Price] = true
			}
		}
	}
	for price, seen := range wantSells {
		if !seen {
			t.Fatalf("expected moved SELL window to include %.2f, orders=%v", price, executor.orders)
		}
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

func TestExitOrderMovesToMakerSafeGridWhenTargetWasCrossed(t *testing.T) {
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
			if order.Price != 102 {
				t.Fatalf("expected crossed exit target to move to maker-safe grid 102, got %.2f", order.Price)
			}
			return
		}
	}
	t.Fatalf("expected reduce-only sell exit order, orders=%v", executor.orders)
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

	maxQty := spm.maxConfiguredOrderQuantity(2999)
	if maxQty <= 0 {
		t.Fatalf("max configured quantity should be positive")
	}
	var reduceOnlyBuys int
	for _, order := range executor.orders {
		if order.Side != "BUY" || !order.ReduceOnly {
			continue
		}
		reduceOnlyBuys++
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

func TestPositionSnapshotDoesNotAdoptUnmanagedGrowthByDefault(t *testing.T) {
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

	for _, order := range executor.orders {
		if order.Side == "BUY" && order.ReduceOnly {
			t.Fatalf("unmanaged exchange short must not be adopted during reconciliation: %+v", order)
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

func TestDirectionalAdjustOrdersBackfillsCurrentWindowWithoutCancelingOldEntries(t *testing.T) {
	cfg := &config.Config{}
	cfg.Trading.Symbol = "ETHUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.PriceInterval = 1
	cfg.Trading.OrderQuantity = 30
	cfg.Trading.BuyWindowSize = 3
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 3
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

	has109 := false
	has108 := false
	for _, order := range executor.orders {
		if order.Side != "BUY" || order.ReduceOnly {
			continue
		}
		switch order.Price {
		case 109:
			has109 = true
		case 108:
			has108 = true
		}
	}
	if !has109 || !has108 {
		t.Fatalf("expected current entry window to be refilled at 109 and 108, got has109=%v has108=%v orders=%v",
			has109, has108, executor.orders)
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
	if err := spm.AdjustOrdersWithRebalance(95, true); err != nil {
		t.Fatalf("price shift AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) == 0 {
		t.Fatalf("expected price-grid shift to cancel stale far entry orders, got none")
	}

	wantSells := map[float64]bool{96: false, 97: false, 98: false}
	for _, order := range executor.orders {
		if order.Side != "SELL" || order.ReduceOnly {
			continue
		}
		if _, ok := wantSells[order.Price]; ok {
			wantSells[order.Price] = true
		}
	}
	for price, seen := range wantSells {
		if !seen {
			t.Fatalf("expected current short entry window to include %.2f, orders=%v", price, executor.orders)
		}
	}
}

func TestPriceGridShiftDoesNotChurnOnSubIntervalAnchoredTicks(t *testing.T) {
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

	if err := spm.AdjustOrdersWithRebalance(2265.30, true); err != nil {
		t.Fatalf("first grid shift AdjustOrders() error = %v", err)
	}
	firstCancelCount := len(executor.canceled)
	firstOrderCount := len(executor.orders)
	if firstCancelCount == 0 {
		t.Fatalf("expected full interval price shift to rebalance stale entry orders")
	}

	for _, price := range []float64{2265.55, 2265.44, 2265.37, 2265.24} {
		if err := spm.AdjustOrdersWithRebalance(price, true); err != nil {
			t.Fatalf("sub-interval AdjustOrders(%.2f) error = %v", price, err)
		}
	}
	if len(executor.canceled) != firstCancelCount {
		t.Fatalf("expected anchored sub-interval ticks not to trigger more cancels, got before=%d after=%d canceled=%v",
			firstCancelCount, len(executor.canceled), executor.canceled)
	}
	if len(executor.orders) != firstOrderCount {
		t.Fatalf("expected anchored sub-interval ticks not to repost entries, got before=%d after=%d",
			firstOrderCount, len(executor.orders))
	}
}

func TestPriceGridShiftFollowsNearestGridBeforeRebalancing(t *testing.T) {
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

	if err := spm.AdjustOrdersWithRebalance(2274.70, true); err != nil {
		t.Fatalf("full interval AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) == 0 {
		t.Fatalf("expected move from 2273.41 to 2274.41 to rebalance old window")
	}

	cancelCount := len(executor.canceled)
	orderCount := len(executor.orders)
	if err := spm.AdjustOrdersWithRebalance(2274.83, true); err != nil {
		t.Fatalf("same-grid AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) != cancelCount {
		t.Fatalf("expected same nearest grid not to cancel far entries, got before=%d after=%d canceled=%v",
			cancelCount, len(executor.canceled), executor.canceled)
	}
	if len(executor.orders) != orderCount {
		t.Fatalf("expected same nearest grid not to repost entries, got before=%d after=%d",
			orderCount, len(executor.orders))
	}

	if err := spm.AdjustOrdersWithRebalance(2274.92, true); err != nil {
		t.Fatalf("nearest-grid shift AdjustOrders() error = %v", err)
	}
	if len(executor.canceled) == cancelCount {
		t.Fatalf("expected nearest-grid shift to rebalance stale entries")
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
	assertFloatNear(t, spm.GetRealizedPNL(), 0.2)
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
