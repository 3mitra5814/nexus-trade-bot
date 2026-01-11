package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"nexus-trade-bot/logger"

	"github.com/gorilla/websocket"
)

type WebSocketManager struct {
	client        *Client
	marketSymbol  string
	displaySymbol string
	orderIDs      *orderIDMapper

	mu          sync.RWMutex
	callbacks   []OrderUpdateCallback
	orderStopC  chan struct{}
	orderDoneC  chan struct{}
	orderActive bool

	priceMu      sync.RWMutex
	latestPrice  float64
	reconnectGap time.Duration
}

func NewWebSocketManager(client *Client, marketSymbol, displaySymbol string, orderIDs *orderIDMapper) *WebSocketManager {
	return &WebSocketManager{
		client:        client,
		marketSymbol:  marketSymbol,
		displaySymbol: displaySymbol,
		orderIDs:      orderIDs,
		reconnectGap:  5 * time.Second,
	}
}

func (w *WebSocketManager) Start(ctx context.Context, callback OrderUpdateCallback) error {
	w.mu.Lock()

	if w.orderActive {
		w.callbacks = append(w.callbacks, callback)
		w.mu.Unlock()
		return nil
	}

	w.callbacks = append(w.callbacks, callback)
	w.orderStopC = make(chan struct{})
	w.orderDoneC = make(chan struct{})
	w.orderActive = true
	readyCh := make(chan error, 1)

	go w.runOrderLoop(ctx, w.orderStopC, w.orderDoneC, readyCh)
	w.mu.Unlock()

	select {
	case err := <-readyCh:
		if err != nil {
			w.Stop()
			return err
		}
		return nil
	case <-ctx.Done():
		w.Stop()
		return fmt.Errorf("上下文已取消")
	case <-time.After(10 * time.Second):
		w.Stop()
		return fmt.Errorf("等待 Bybit 订单流连接超时")
	}
}

func (w *WebSocketManager) Stop() {
	w.mu.Lock()
	if !w.orderActive {
		w.mu.Unlock()
		return
	}
	stopC := w.orderStopC
	doneC := w.orderDoneC
	w.orderActive = false
	w.mu.Unlock()

	close(stopC)
	select {
	case <-doneC:
	case <-time.After(10 * time.Second):
		logger.Warn("⚠️ [Bybit] 停止订单流超时")
	}
}

func (w *WebSocketManager) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	firstPriceCh := make(chan struct{})
	var once sync.Once

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			conn, _, err := websocket.DefaultDialer.Dial(bybitPublicLinear, nil)
			if err != nil {
				logger.Warn("⚠️ [Bybit] 价格流连接失败: %v", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(w.reconnectGap):
				}
				continue
			}

			subscribe := map[string]any{
				"op":   "subscribe",
				"args": []string{fmt.Sprintf("tickers.%s", symbol)},
			}
			if err := conn.WriteJSON(subscribe); err != nil {
				conn.Close()
				logger.Warn("⚠️ [Bybit] 价格流订阅失败: %v", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(w.reconnectGap):
				}
				continue
			}

			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					conn.Close()
					logger.Warn("⚠️ [Bybit] 价格流断开，准备重连: %v", err)
					break
				}

				var payload struct {
					Topic string `json:"topic"`
					Data  struct {
						LastPrice string `json:"lastPrice"`
						MarkPrice string `json:"markPrice"`
					} `json:"data"`
				}
				if err := json.Unmarshal(message, &payload); err != nil {
					continue
				}

				price := parseFloat(payload.Data.LastPrice)
				if price <= 0 {
					price = parseFloat(payload.Data.MarkPrice)
				}
				if price <= 0 {
					continue
				}

				w.priceMu.Lock()
				w.latestPrice = price
				w.priceMu.Unlock()

				once.Do(func() { close(firstPriceCh) })
				callback(price)
			}
		}
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("上下文已取消")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("等待 Bybit 首个价格超时")
	case <-firstPriceCh:
		return nil
	}
}

func (w *WebSocketManager) GetLatestPrice() float64 {
	w.priceMu.RLock()
	defer w.priceMu.RUnlock()
	return w.latestPrice
}

func (w *WebSocketManager) runOrderLoop(ctx context.Context, stopC, doneC chan struct{}, readyCh chan<- error) {
	defer close(doneC)
	var readyOnce sync.Once
	reportReady := func(err error) {
		readyOnce.Do(func() {
			select {
			case readyCh <- err:
			default:
			}
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopC:
			return
		default:
		}

		conn, _, err := websocket.DefaultDialer.Dial(bybitPrivateWS, nil)
		if err != nil {
			logger.Warn("⚠️ [Bybit] 订单流连接失败: %v", err)
			reportReady(fmt.Errorf("Bybit 订单流连接失败: %w", err))
			select {
			case <-ctx.Done():
				return
			case <-stopC:
				return
			case <-time.After(w.reconnectGap):
			}
			continue
		}

		auth := map[string]any{
			"op":   "auth",
			"args": w.client.WebSocketAuthArgs(),
		}
		if err := conn.WriteJSON(auth); err != nil {
			conn.Close()
			logger.Warn("⚠️ [Bybit] 订单流鉴权失败: %v", err)
			reportReady(fmt.Errorf("Bybit 订单流鉴权失败: %w", err))
			select {
			case <-ctx.Done():
				return
			case <-stopC:
				return
			case <-time.After(w.reconnectGap):
			}
			continue
		}

		subscribe := map[string]any{
			"op":   "subscribe",
			"args": []string{"order"},
		}
		if err := conn.WriteJSON(subscribe); err != nil {
			conn.Close()
			logger.Warn("⚠️ [Bybit] 订单流订阅失败: %v", err)
			reportReady(fmt.Errorf("Bybit 订单流订阅失败: %w", err))
			select {
			case <-ctx.Done():
				return
			case <-stopC:
				return
			case <-time.After(w.reconnectGap):
			}
			continue
		}
		reportReady(nil)

		for {
			select {
			case <-ctx.Done():
				conn.Close()
				return
			case <-stopC:
				conn.Close()
				return
			default:
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				conn.Close()
				logger.Warn("⚠️ [Bybit] 订单流断开，准备重连: %v", err)
				break
			}

			w.handleOrderMessage(message)
		}
	}
}

func (w *WebSocketManager) handleOrderMessage(message []byte) {
	var payload struct {
		Topic string `json:"topic"`
		Data  []struct {
			Category    string `json:"category"`
			OrderID     string `json:"orderId"`
			OrderLinkID string `json:"orderLinkId"`
			Symbol      string `json:"symbol"`
			Side        string `json:"side"`
			OrderType   string `json:"orderType"`
			OrderStatus string `json:"orderStatus"`
			Price       string `json:"price"`
			Qty         string `json:"qty"`
			CumExecQty  string `json:"cumExecQty"`
			AvgPrice    string `json:"avgPrice"`
			UpdatedTime string `json:"updatedTime"`
		} `json:"data"`
	}

	if err := json.Unmarshal(message, &payload); err != nil || payload.Topic != "order" {
		return
	}

	for _, item := range payload.Data {
		if item.Category != "linear" {
			continue
		}
		if item.Symbol != "" && item.Symbol != w.marketSymbol {
			continue
		}

		update := OrderUpdate{
			OrderID:       w.orderIDs.encode(item.OrderID),
			ClientOrderID: item.OrderLinkID,
			Symbol:        w.displaySymbol,
			Side:          mapSideFromBybit(item.Side),
			Type:          mapOrderTypeFromBybit(item.OrderType),
			Status:        mapStatusFromBybit(item.OrderStatus),
			Price:         parseFloat(item.Price),
			Quantity:      parseFloat(item.Qty),
			ExecutedQty:   parseFloat(item.CumExecQty),
			AvgPrice:      parseFloat(item.AvgPrice),
			UpdateTime:    parseInt64(item.UpdatedTime),
		}

		w.mu.RLock()
		callbacks := append([]OrderUpdateCallback(nil), w.callbacks...)
		w.mu.RUnlock()

		for _, callback := range callbacks {
			callback(update)
		}
	}
}
