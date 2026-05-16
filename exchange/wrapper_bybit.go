package exchange

import (
	"context"
	"nexus-trade-bot/exchange/bybit"
)

type bybitWrapper struct {
	adapter *bybit.BybitAdapter
}

func (w *bybitWrapper) GetName() string {
	return w.adapter.GetName()
}

func (w *bybitWrapper) PlaceOrder(ctx context.Context, req *OrderRequest) (*Order, error) {
	bybitReq := &bybit.OrderRequest{
		Symbol:        req.Symbol,
		Side:          bybit.Side(req.Side),
		Type:          bybit.OrderType(req.Type),
		TimeInForce:   bybit.TimeInForce(req.TimeInForce),
		Quantity:      req.Quantity,
		Price:         req.Price,
		ReduceOnly:    req.ReduceOnly,
		PostOnly:      req.PostOnly,
		PriceDecimals: req.PriceDecimals,
		ClientOrderID: req.ClientOrderID,
	}

	placed, err := w.adapter.PlaceOrder(ctx, bybitReq)
	if err != nil {
		return nil, err
	}

	return &Order{
		OrderID:       placed.OrderID,
		ClientOrderID: placed.ClientOrderID,
		Symbol:        placed.Symbol,
		Side:          Side(placed.Side),
		Type:          OrderType(placed.Type),
		Price:         placed.Price,
		Quantity:      placed.Quantity,
		ExecutedQty:   placed.ExecutedQty,
		AvgPrice:      placed.AvgPrice,
		Status:        OrderStatus(placed.Status),
		CreatedAt:     placed.CreatedAt,
		UpdateTime:    placed.UpdateTime,
	}, nil
}

func (w *bybitWrapper) BatchPlaceOrders(ctx context.Context, orders []*OrderRequest) ([]*Order, bool) {
	requests := make([]*bybit.OrderRequest, len(orders))
	for i, req := range orders {
		requests[i] = &bybit.OrderRequest{
			Symbol:        req.Symbol,
			Side:          bybit.Side(req.Side),
			Type:          bybit.OrderType(req.Type),
			TimeInForce:   bybit.TimeInForce(req.TimeInForce),
			Quantity:      req.Quantity,
			Price:         req.Price,
			ReduceOnly:    req.ReduceOnly,
			PostOnly:      req.PostOnly,
			PriceDecimals: req.PriceDecimals,
			ClientOrderID: req.ClientOrderID,
		}
	}

	placed, hasMarginError := w.adapter.BatchPlaceOrders(ctx, requests)
	result := make([]*Order, len(placed))
	for i, item := range placed {
		result[i] = &Order{
			OrderID:       item.OrderID,
			ClientOrderID: item.ClientOrderID,
			Symbol:        item.Symbol,
			Side:          Side(item.Side),
			Type:          OrderType(item.Type),
			Price:         item.Price,
			Quantity:      item.Quantity,
			ExecutedQty:   item.ExecutedQty,
			AvgPrice:      item.AvgPrice,
			Status:        OrderStatus(item.Status),
			CreatedAt:     item.CreatedAt,
			UpdateTime:    item.UpdateTime,
		}
	}

	return result, hasMarginError
}

func (w *bybitWrapper) CancelOrder(ctx context.Context, symbol string, orderID int64) error {
	return w.adapter.CancelOrder(ctx, symbol, orderID)
}

func (w *bybitWrapper) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	return w.adapter.BatchCancelOrders(ctx, symbol, orderIDs)
}

func (w *bybitWrapper) CancelAllOrders(ctx context.Context, symbol string) error {
	return w.adapter.CancelAllOrders(ctx, symbol)
}

func (w *bybitWrapper) GetOrder(ctx context.Context, symbol string, orderID int64) (*Order, error) {
	placed, err := w.adapter.GetOrder(ctx, symbol, orderID)
	if err != nil {
		return nil, err
	}

	return &Order{
		OrderID:       placed.OrderID,
		ClientOrderID: placed.ClientOrderID,
		Symbol:        placed.Symbol,
		Side:          Side(placed.Side),
		Type:          OrderType(placed.Type),
		Price:         placed.Price,
		Quantity:      placed.Quantity,
		ExecutedQty:   placed.ExecutedQty,
		AvgPrice:      placed.AvgPrice,
		Status:        OrderStatus(placed.Status),
		CreatedAt:     placed.CreatedAt,
		UpdateTime:    placed.UpdateTime,
	}, nil
}

func (w *bybitWrapper) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	placed, err := w.adapter.GetOpenOrders(ctx, symbol)
	if err != nil {
		return nil, err
	}

	result := make([]*Order, len(placed))
	for i, item := range placed {
		result[i] = &Order{
			OrderID:       item.OrderID,
			ClientOrderID: item.ClientOrderID,
			Symbol:        item.Symbol,
			Side:          Side(item.Side),
			Type:          OrderType(item.Type),
			Price:         item.Price,
			Quantity:      item.Quantity,
			ExecutedQty:   item.ExecutedQty,
			AvgPrice:      item.AvgPrice,
			Status:        OrderStatus(item.Status),
			CreatedAt:     item.CreatedAt,
			UpdateTime:    item.UpdateTime,
		}
	}

	return result, nil
}

func (w *bybitWrapper) GetAccount(ctx context.Context) (*Account, error) {
	account, err := w.adapter.GetAccount(ctx)
	if err != nil {
		return nil, err
	}

	positions := make([]*Position, len(account.Positions))
	for i, item := range account.Positions {
		positions[i] = &Position{
			Symbol:         item.Symbol,
			Size:           item.Size,
			EntryPrice:     item.EntryPrice,
			MarkPrice:      item.MarkPrice,
			UnrealizedPNL:  item.UnrealizedPNL,
			Leverage:       item.Leverage,
			MarginType:     item.MarginType,
			IsolatedMargin: item.IsolatedMargin,
		}
	}

	return &Account{
		TotalWalletBalance: account.TotalWalletBalance,
		TotalMarginBalance: account.TotalMarginBalance,
		AvailableBalance:   account.AvailableBalance,
		Positions:          positions,
		AccountLeverage:    account.AccountLeverage,
	}, nil
}

func (w *bybitWrapper) GetPositions(ctx context.Context, symbol string) ([]*Position, error) {
	positions, err := w.adapter.GetPositions(ctx, symbol)
	if err != nil {
		return nil, err
	}

	result := make([]*Position, len(positions))
	for i, item := range positions {
		result[i] = &Position{
			Symbol:         item.Symbol,
			Size:           item.Size,
			EntryPrice:     item.EntryPrice,
			MarkPrice:      item.MarkPrice,
			UnrealizedPNL:  item.UnrealizedPNL,
			Leverage:       item.Leverage,
			MarginType:     item.MarginType,
			IsolatedMargin: item.IsolatedMargin,
		}
	}

	return result, nil
}

func (w *bybitWrapper) ValidatePositionMode(ctx context.Context, direction string) error {
	return w.adapter.ValidatePositionMode(ctx, direction)
}

func (w *bybitWrapper) GetBalance(ctx context.Context, asset string) (float64, error) {
	return w.adapter.GetBalance(ctx, asset)
}

func (w *bybitWrapper) StartOrderStream(ctx context.Context, callback func(interface{})) error {
	return w.adapter.StartOrderStream(ctx, callback)
}

func (w *bybitWrapper) StopOrderStream() error {
	return w.adapter.StopOrderStream()
}

func (w *bybitWrapper) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	return w.adapter.GetLatestPrice(ctx, symbol)
}

func (w *bybitWrapper) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	return w.adapter.StartPriceStream(ctx, symbol, callback)
}

func (w *bybitWrapper) StartKlineStream(ctx context.Context, symbols []string, interval string, callback CandleUpdateCallback) error {
	return w.adapter.StartKlineStream(ctx, symbols, interval, func(candle interface{}) {
		if c, ok := candle.(*bybit.Candle); ok {
			callback(&Candle{
				Symbol:    c.Symbol,
				Open:      c.Open,
				High:      c.High,
				Low:       c.Low,
				Close:     c.Close,
				Volume:    c.Volume,
				Timestamp: c.Timestamp,
				IsClosed:  c.IsClosed,
			})
		}
	})
}

func (w *bybitWrapper) StopKlineStream() error {
	return w.adapter.StopKlineStream()
}

func (w *bybitWrapper) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*Candle, error) {
	candles, err := w.adapter.GetHistoricalKlines(ctx, symbol, interval, limit)
	if err != nil {
		return nil, err
	}

	result := make([]*Candle, len(candles))
	for i, item := range candles {
		result[i] = &Candle{
			Symbol:    item.Symbol,
			Open:      item.Open,
			High:      item.High,
			Low:       item.Low,
			Close:     item.Close,
			Volume:    item.Volume,
			Timestamp: item.Timestamp,
			IsClosed:  item.IsClosed,
		}
	}

	return result, nil
}

func (w *bybitWrapper) GetPriceDecimals() int {
	return w.adapter.GetPriceDecimals()
}

func (w *bybitWrapper) GetQuantityDecimals() int {
	return w.adapter.GetQuantityDecimals()
}

func (w *bybitWrapper) GetBaseAsset() string {
	return w.adapter.GetBaseAsset()
}

func (w *bybitWrapper) GetQuoteAsset() string {
	return w.adapter.GetQuoteAsset()
}
