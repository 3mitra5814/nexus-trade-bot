package hyperliquidex

import (
	"context"
	"fmt"
	"sync"
	"time"

	hl "github.com/sonirico/go-hyperliquid"

	"nexus-trade-bot/logger"
)

type WebSocketManager struct {
	address     string
	coin        string
	decodeCloid func(*string) string

	mu          sync.RWMutex
	orderWS     *hl.WebsocketClient
	orderSub    *hl.Subscription
	orderActive bool

	priceMu     sync.RWMutex
	priceWS     *hl.WebsocketClient
	latestPrice float64
}

func NewWebSocketManager(address, coin string, decodeCloid func(*string) string) *WebSocketManager {
	return &WebSocketManager{address: address, coin: coin, decodeCloid: decodeCloid}
}

func (w *WebSocketManager) StartOrderStream(ctx context.Context, callback OrderUpdateCallback) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.orderActive {
		return nil
	}

	ws := hl.NewWebsocketClient(hl.MainnetAPIURL)
	if err := ws.Connect(ctx); err != nil {
		return err
	}
	sub, err := ws.OrderUpdates(hl.OrderUpdatesSubscriptionParams{User: w.address}, func(orders []hl.WsOrder, err error) {
		if err != nil {
			logger.Warn("⚠️ [Hyperliquid] 订单流错误: %v", err)
			return
		}
		for _, item := range orders {
			if item.Order.Coin != "" && item.Order.Coin != w.coin {
				continue
			}
			origQty := parseFloat(item.Order.OrigSz)
			remainingQty := parseFloat(item.Order.Sz)
			executedQty := origQty - remainingQty
			if item.Status == hl.OrderStatusValueFilled && executedQty <= 0 {
				executedQty = origQty
			}
			if executedQty < 0 {
				executedQty = 0
			}
			callback(OrderUpdate{
				OrderID:       item.Order.Oid,
				ClientOrderID: w.decodeCloid(item.Order.Cloid),
				Symbol:        toDisplaySymbol(item.Order.Coin),
				Side:          mapSideFromHyperliquid(item.Order.Side),
				Type:          OrderTypeLimit,
				Status:        mapOrderStatusFromHyperliquid(item.Status),
				Price:         parseFloat(item.Order.LimitPx),
				Quantity:      origQty,
				ExecutedQty:   executedQty,
				UpdateTime:    item.StatusTimestamp,
			})
		}
	})
	if err != nil {
		_ = ws.Close()
		return err
	}

	w.orderWS = ws
	w.orderSub = sub
	w.orderActive = true
	return nil
}

func (w *WebSocketManager) StopOrderStream() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.orderSub != nil {
		w.orderSub.Close()
		w.orderSub = nil
	}
	if w.orderWS != nil {
		_ = w.orderWS.Close()
		w.orderWS = nil
	}
	w.orderActive = false
}

func (w *WebSocketManager) StartPriceStream(ctx context.Context, coin string, callback func(price float64)) error {
	firstPriceCh := make(chan struct{})
	var once sync.Once

	ws := hl.NewWebsocketClient(hl.MainnetAPIURL)
	if err := ws.Connect(ctx); err != nil {
		return err
	}
	w.priceMu.Lock()
	w.priceWS = ws
	w.priceMu.Unlock()

	_, err := ws.AllMids(hl.AllMidsSubscriptionParams{}, func(mids hl.AllMids, err error) {
		if err != nil {
			logger.Warn("⚠️ [Hyperliquid] 价格流错误: %v", err)
			return
		}
		price := parseFloat(mids.Mids[coin])
		if price <= 0 {
			return
		}
		w.priceMu.Lock()
		w.latestPrice = price
		w.priceMu.Unlock()
		once.Do(func() { close(firstPriceCh) })
		callback(price)
	})
	if err != nil {
		_ = ws.Close()
		return err
	}

	go func() {
		<-ctx.Done()
		w.priceMu.Lock()
		if w.priceWS != nil {
			_ = w.priceWS.Close()
			w.priceWS = nil
		}
		w.priceMu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("上下文已取消")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("等待 Hyperliquid 首个价格超时")
	case <-firstPriceCh:
		return nil
	}
}

func (w *WebSocketManager) GetLatestPrice() float64 {
	w.priceMu.RLock()
	defer w.priceMu.RUnlock()
	return w.latestPrice
}
