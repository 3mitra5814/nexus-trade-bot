package okx

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
	instID        string
	displaySymbol string
	orderIDs      *orderIDMapper
	baseQuantity  func(float64) float64

	mu          sync.RWMutex
	callbacks   []OrderUpdateCallback
	orderStopC  chan struct{}
	orderDoneC  chan struct{}
	orderActive bool

	priceMu      sync.RWMutex
	latestPrice  float64
	reconnectGap time.Duration
}

func NewWebSocketManager(client *Client, instID, displaySymbol string, orderIDs *orderIDMapper, baseQuantity func(float64) float64) *WebSocketManager {
	return &WebSocketManager{
		client:        client,
		instID:        instID,
		displaySymbol: displaySymbol,
		orderIDs:      orderIDs,
		baseQuantity:  baseQuantity,
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
		return fmt.Errorf("等待 OKX 订单流连接超时")
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
		logger.Warn("⚠️ [OKX] 停止订单流超时")
	}
}

func (w *WebSocketManager) StartPriceStream(ctx context.Context, instID string, callback func(price float64)) error {
	firstPriceCh := make(chan struct{})
	var once sync.Once

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			conn, _, err := websocket.DefaultDialer.Dial(okxPublicWS, nil)
			if err != nil {
				logger.Warn("⚠️ [OKX] 价格流连接失败: %v", err)
				if !sleepOrDone(ctx, w.reconnectGap) {
					return
				}
				continue
			}

			subscribe := map[string]any{
				"op": "subscribe",
				"args": []map[string]string{{
					"channel": "tickers",
					"instId":  instID,
				}},
			}
			if err := conn.WriteJSON(subscribe); err != nil {
				conn.Close()
				logger.Warn("⚠️ [OKX] 价格流订阅失败: %v", err)
				if !sleepOrDone(ctx, w.reconnectGap) {
					return
				}
				continue
			}

			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					conn.Close()
					logger.Warn("⚠️ [OKX] 价格流断开，准备重连: %v", err)
					break
				}

				var payload struct {
					Arg struct {
						Channel string `json:"channel"`
					} `json:"arg"`
					Data []struct {
						Last string `json:"last"`
					} `json:"data"`
				}
				if err := json.Unmarshal(message, &payload); err != nil || payload.Arg.Channel != "tickers" || len(payload.Data) == 0 {
					continue
				}

				price := parseFloat(payload.Data[0].Last)
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
		return fmt.Errorf("等待 OKX 首个价格超时")
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

		conn, _, err := websocket.DefaultDialer.Dial(okxPrivateWS, nil)
		if err != nil {
			logger.Warn("⚠️ [OKX] 订单流连接失败: %v", err)
			reportReady(fmt.Errorf("OKX 订单流连接失败: %w", err))
			if !sleepOrStopped(ctx, stopC, w.reconnectGap) {
				return
			}
			continue
		}

		if err := conn.WriteJSON(map[string]any{"op": "login", "args": w.client.WebSocketLoginArgs()}); err != nil {
			conn.Close()
			logger.Warn("⚠️ [OKX] 订单流鉴权失败: %v", err)
			reportReady(fmt.Errorf("OKX 订单流鉴权失败: %w", err))
			if !sleepOrStopped(ctx, stopC, w.reconnectGap) {
				return
			}
			continue
		}

		subscribe := map[string]any{
			"op": "subscribe",
			"args": []map[string]string{{
				"channel":  "orders",
				"instType": "SWAP",
			}},
		}
		if err := conn.WriteJSON(subscribe); err != nil {
			conn.Close()
			logger.Warn("⚠️ [OKX] 订单流订阅失败: %v", err)
			reportReady(fmt.Errorf("OKX 订单流订阅失败: %w", err))
			if !sleepOrStopped(ctx, stopC, w.reconnectGap) {
				return
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
				logger.Warn("⚠️ [OKX] 订单流断开，准备重连: %v", err)
				break
			}
			w.handleOrderMessage(message)
		}
	}
}

func (w *WebSocketManager) handleOrderMessage(message []byte) {
	var payload struct {
		Arg struct {
			Channel string `json:"channel"`
		} `json:"arg"`
		Data []struct {
			InstID    string `json:"instId"`
			OrdID     string `json:"ordId"`
			ClOrdID   string `json:"clOrdId"`
			Side      string `json:"side"`
			OrdType   string `json:"ordType"`
			State     string `json:"state"`
			Px        string `json:"px"`
			Sz        string `json:"sz"`
			AccFillSz string `json:"accFillSz"`
			AvgPx     string `json:"avgPx"`
			UTime     string `json:"uTime"`
		} `json:"data"`
	}
	if err := json.Unmarshal(message, &payload); err != nil || payload.Arg.Channel != "orders" {
		return
	}

	for _, item := range payload.Data {
		if item.InstID != "" && item.InstID != w.instID {
			continue
		}
		update := OrderUpdate{
			OrderID:       w.orderIDs.encode(item.OrdID),
			ClientOrderID: decodeClientOrderID(item.ClOrdID),
			Symbol:        w.displaySymbol,
			Side:          mapSideFromOKX(item.Side),
			Type:          mapOrderTypeFromOKX(item.OrdType),
			Status:        mapStatusFromOKX(item.State),
			Price:         parseFloat(item.Px),
			Quantity:      w.baseQuantity(parseFloat(item.Sz)),
			ExecutedQty:   w.baseQuantity(parseFloat(item.AccFillSz)),
			AvgPrice:      parseFloat(item.AvgPx),
			UpdateTime:    parseInt64(item.UTime),
		}

		w.mu.RLock()
		callbacks := append([]OrderUpdateCallback(nil), w.callbacks...)
		w.mu.RUnlock()
		for _, callback := range callbacks {
			callback(update)
		}
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func sleepOrStopped(ctx context.Context, stopC <-chan struct{}, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-stopC:
		return false
	case <-time.After(d):
		return true
	}
}
