package exchange

import (
	"context"

	hyperliquidex "nexus-trade-bot/exchange/hyperliquid"
)

type hyperliquidWrapper struct {
	adapter *hyperliquidex.HyperliquidAdapter
}

func (w *hyperliquidWrapper) GetName() string { return w.adapter.GetName() }

func (w *hyperliquidWrapper) PlaceOrder(ctx context.Context, req *OrderRequest) (*Order, error) {
	hlReq := &hyperliquidex.OrderRequest{
		Symbol:        req.Symbol,
		Side:          hyperliquidex.Side(req.Side),
		Type:          hyperliquidex.OrderType(req.Type),
		TimeInForce:   hyperliquidex.TimeInForce(req.TimeInForce),
		Quantity:      req.Quantity,
		Price:         req.Price,
		ReduceOnly:    req.ReduceOnly,
		PostOnly:      req.PostOnly,
		PriceDecimals: req.PriceDecimals,
		ClientOrderID: req.ClientOrderID,
	}
	order, err := w.adapter.PlaceOrder(ctx, hlReq)
	if err != nil {
		return nil, err
	}
	return convertHyperliquidOrder(order), nil
}

func (w *hyperliquidWrapper) BatchPlaceOrders(ctx context.Context, orders []*OrderRequest) ([]*Order, bool) {
	hlOrders := make([]*hyperliquidex.OrderRequest, len(orders))
	for i, req := range orders {
		hlOrders[i] = &hyperliquidex.OrderRequest{
			Symbol:        req.Symbol,
			Side:          hyperliquidex.Side(req.Side),
			Type:          hyperliquidex.OrderType(req.Type),
			TimeInForce:   hyperliquidex.TimeInForce(req.TimeInForce),
			Quantity:      req.Quantity,
			Price:         req.Price,
			ReduceOnly:    req.ReduceOnly,
			PostOnly:      req.PostOnly,
			PriceDecimals: req.PriceDecimals,
			ClientOrderID: req.ClientOrderID,
		}
	}
	result, hasMarginError := w.adapter.BatchPlaceOrders(ctx, hlOrders)
	ordersOut := make([]*Order, len(result))
	for i, order := range result {
		ordersOut[i] = convertHyperliquidOrder(order)
	}
	return ordersOut, hasMarginError
}

func (w *hyperliquidWrapper) CancelOrder(ctx context.Context, symbol string, orderID int64) error {
	return w.adapter.CancelOrder(ctx, symbol, orderID)
}

func (w *hyperliquidWrapper) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	return w.adapter.BatchCancelOrders(ctx, symbol, orderIDs)
}

func (w *hyperliquidWrapper) CancelAllOrders(ctx context.Context, symbol string) error {
	return w.adapter.CancelAllOrders(ctx, symbol)
}

func (w *hyperliquidWrapper) GetOrder(ctx context.Context, symbol string, orderID int64) (*Order, error) {
	order, err := w.adapter.GetOrder(ctx, symbol, orderID)
	if err != nil {
		return nil, err
	}
	return convertHyperliquidOrder(order), nil
}

func (w *hyperliquidWrapper) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	orders, err := w.adapter.GetOpenOrders(ctx, symbol)
	if err != nil {
		return nil, err
	}
	result := make([]*Order, len(orders))
	for i, order := range orders {
		result[i] = convertHyperliquidOrder(order)
	}
	return result, nil
}

func (w *hyperliquidWrapper) GetAccount(ctx context.Context) (*Account, error) {
	account, err := w.adapter.GetAccount(ctx)
	if err != nil {
		return nil, err
	}
	positions := make([]*Position, len(account.Positions))
	for i, pos := range account.Positions {
		positions[i] = convertHyperliquidPosition(pos)
	}
	return &Account{
		TotalWalletBalance: account.TotalWalletBalance,
		TotalMarginBalance: account.TotalMarginBalance,
		AvailableBalance:   account.AvailableBalance,
		Positions:          positions,
		AccountLeverage:    account.AccountLeverage,
	}, nil
}

func (w *hyperliquidWrapper) GetPositions(ctx context.Context, symbol string) ([]*Position, error) {
	positions, err := w.adapter.GetPositions(ctx, symbol)
	if err != nil {
		return nil, err
	}
	result := make([]*Position, len(positions))
	for i, pos := range positions {
		result[i] = convertHyperliquidPosition(pos)
	}
	return result, nil
}

func (w *hyperliquidWrapper) ValidatePositionMode(ctx context.Context, direction string) error {
	return w.adapter.ValidatePositionMode(ctx, direction)
}

func (w *hyperliquidWrapper) GetBalance(ctx context.Context, asset string) (float64, error) {
	return w.adapter.GetBalance(ctx, asset)
}

func (w *hyperliquidWrapper) StartOrderStream(ctx context.Context, callback func(interface{})) error {
	return w.adapter.StartOrderStream(ctx, callback)
}

func (w *hyperliquidWrapper) StopOrderStream() error { return w.adapter.StopOrderStream() }

func (w *hyperliquidWrapper) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	return w.adapter.GetLatestPrice(ctx, symbol)
}

func (w *hyperliquidWrapper) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	return w.adapter.StartPriceStream(ctx, symbol, callback)
}

func (w *hyperliquidWrapper) StartKlineStream(ctx context.Context, symbols []string, interval string, callback CandleUpdateCallback) error {
	return w.adapter.StartKlineStream(ctx, symbols, interval, func(candle interface{}) {
		if c, ok := candle.(*hyperliquidex.Candle); ok {
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

func (w *hyperliquidWrapper) StopKlineStream() error { return w.adapter.StopKlineStream() }

func (w *hyperliquidWrapper) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*Candle, error) {
	candles, err := w.adapter.GetHistoricalKlines(ctx, symbol, interval, limit)
	if err != nil {
		return nil, err
	}
	result := make([]*Candle, len(candles))
	for i, c := range candles {
		result[i] = &Candle{
			Symbol:    c.Symbol,
			Open:      c.Open,
			High:      c.High,
			Low:       c.Low,
			Close:     c.Close,
			Volume:    c.Volume,
			Timestamp: c.Timestamp,
			IsClosed:  c.IsClosed,
		}
	}
	return result, nil
}

func (w *hyperliquidWrapper) GetPriceDecimals() int    { return w.adapter.GetPriceDecimals() }
func (w *hyperliquidWrapper) GetQuantityDecimals() int { return w.adapter.GetQuantityDecimals() }
func (w *hyperliquidWrapper) GetBaseAsset() string     { return w.adapter.GetBaseAsset() }
func (w *hyperliquidWrapper) GetQuoteAsset() string    { return w.adapter.GetQuoteAsset() }

func convertHyperliquidOrder(order *hyperliquidex.Order) *Order {
	return &Order{
		OrderID:       order.OrderID,
		ClientOrderID: order.ClientOrderID,
		Symbol:        order.Symbol,
		Side:          Side(order.Side),
		Type:          OrderType(order.Type),
		Price:         order.Price,
		Quantity:      order.Quantity,
		ExecutedQty:   order.ExecutedQty,
		AvgPrice:      order.AvgPrice,
		Status:        OrderStatus(order.Status),
		CreatedAt:     order.CreatedAt,
		UpdateTime:    order.UpdateTime,
	}
}

func convertHyperliquidPosition(pos *hyperliquidex.Position) *Position {
	return &Position{
		Symbol:           pos.Symbol,
		Size:             pos.Size,
		EntryPrice:       pos.EntryPrice,
		MarkPrice:        pos.MarkPrice,
		UnrealizedPNL:    pos.UnrealizedPNL,
		HasUnrealizedPNL: pos.HasUnrealizedPNL,
		RealizedPNL:      pos.RealizedPNL,
		HasRealizedPNL:   pos.HasRealizedPNL,
		ClosedPNL:        pos.ClosedPNL,
		FundingFee:       pos.FundingFee,
		TradingFee:       pos.TradingFee,
		Leverage:         pos.Leverage,
		MarginType:       pos.MarginType,
		IsolatedMargin:   pos.IsolatedMargin,
	}
}
