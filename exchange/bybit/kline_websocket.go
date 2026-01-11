package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"nexus-trade-bot/logger"

	"github.com/gorilla/websocket"
)

type KlineWebSocketManager struct {
	mu           sync.RWMutex
	running      bool
	stopC        chan struct{}
	reconnectGap time.Duration
}

func NewKlineWebSocketManager() *KlineWebSocketManager {
	return &KlineWebSocketManager{reconnectGap: 5 * time.Second}
}

func (k *KlineWebSocketManager) Start(ctx context.Context, symbols []string, interval string, callback func(candle interface{})) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.running {
		return fmt.Errorf("K线流已在运行")
	}

	k.running = true
	k.stopC = make(chan struct{})
	go k.connectLoop(ctx, k.stopC, symbols, interval, callback)
	return nil
}

func (k *KlineWebSocketManager) Stop() {
	k.mu.Lock()
	defer k.mu.Unlock()

	if !k.running {
		return
	}

	k.running = false
	close(k.stopC)
}

func (k *KlineWebSocketManager) connectLoop(ctx context.Context, stopC chan struct{}, symbols []string, interval string, callback func(candle interface{})) {
	bybitInterval, err := intervalToBybit(interval)
	if err != nil {
		logger.Error("❌ [Bybit] 不支持的K线周期: %v", err)
		return
	}

	args := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, fmt.Sprintf("kline.%s.%s", bybitInterval, symbol))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopC:
			return
		default:
		}

		conn, _, err := websocket.DefaultDialer.Dial(bybitPublicLinear, nil)
		if err != nil {
			logger.Warn("⚠️ [Bybit] K线流连接失败: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-stopC:
				return
			case <-time.After(k.reconnectGap):
			}
			continue
		}

		subscribe := map[string]any{
			"op":   "subscribe",
			"args": args,
		}
		if err := conn.WriteJSON(subscribe); err != nil {
			conn.Close()
			logger.Warn("⚠️ [Bybit] K线流订阅失败: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-stopC:
				return
			case <-time.After(k.reconnectGap):
			}
			continue
		}

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
				logger.Warn("⚠️ [Bybit] K线流断开，准备重连: %v", err)
				break
			}

			var payload struct {
				Topic string `json:"topic"`
				Data  []struct {
					Start     int64  `json:"start"`
					End       int64  `json:"end"`
					Open      string `json:"open"`
					Close     string `json:"close"`
					High      string `json:"high"`
					Low       string `json:"low"`
					Volume    string `json:"volume"`
					Confirm   bool   `json:"confirm"`
					Timestamp int64  `json:"timestamp"`
				} `json:"data"`
			}
			if err := json.Unmarshal(message, &payload); err != nil || len(payload.Data) == 0 {
				continue
			}

			symbol := topicSymbol(payload.Topic)
			for _, item := range payload.Data {
				callback(&Candle{
					Symbol:    symbol,
					Open:      parseFloat(item.Open),
					High:      parseFloat(item.High),
					Low:       parseFloat(item.Low),
					Close:     parseFloat(item.Close),
					Volume:    parseFloat(item.Volume),
					Timestamp: item.Start,
					IsClosed:  item.Confirm,
				})
			}
		}
	}
}

func topicSymbol(topic string) string {
	parts := strings.Split(topic, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
