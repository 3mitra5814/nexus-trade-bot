package okx

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

func (k *KlineWebSocketManager) Start(ctx context.Context, instIDs []string, interval string, callback func(candle interface{})) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.running {
		return fmt.Errorf("K线流已在运行")
	}

	k.running = true
	k.stopC = make(chan struct{})
	go k.connectLoop(ctx, k.stopC, instIDs, interval, callback)
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

func (k *KlineWebSocketManager) connectLoop(ctx context.Context, stopC chan struct{}, instIDs []string, interval string, callback func(candle interface{})) {
	args := make([]map[string]string, 0, len(instIDs))
	channel := "candle" + intervalToOKX(interval)
	for _, instID := range instIDs {
		args = append(args, map[string]string{
			"channel": channel,
			"instId":  instID,
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

		conn, _, err := websocket.DefaultDialer.Dial(okxPublicWS, nil)
		if err != nil {
			logger.Warn("⚠️ [OKX] K线流连接失败: %v", err)
			if !sleepOrStopped(ctx, stopC, k.reconnectGap) {
				return
			}
			continue
		}

		if err := conn.WriteJSON(map[string]any{"op": "subscribe", "args": args}); err != nil {
			conn.Close()
			logger.Warn("⚠️ [OKX] K线流订阅失败: %v", err)
			if !sleepOrStopped(ctx, stopC, k.reconnectGap) {
				return
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
				logger.Warn("⚠️ [OKX] K线流断开，准备重连: %v", err)
				break
			}

			var payload struct {
				Arg struct {
					Channel string `json:"channel"`
					InstID  string `json:"instId"`
				} `json:"arg"`
				Data [][]string `json:"data"`
			}
			if err := json.Unmarshal(message, &payload); err != nil || !strings.HasPrefix(payload.Arg.Channel, "candle") {
				continue
			}

			for _, item := range payload.Data {
				if len(item) < 6 {
					continue
				}
				isClosed := false
				if len(item) >= 9 {
					isClosed = item[8] == "1"
				}
				callback(&Candle{
					Symbol:    fromOKXInstrument(payload.Arg.InstID),
					Timestamp: parseInt64(item[0]),
					Open:      parseFloat(item[1]),
					High:      parseFloat(item[2]),
					Low:       parseFloat(item[3]),
					Close:     parseFloat(item[4]),
					Volume:    parseFloat(item[5]),
					IsClosed:  isClosed,
				})
			}
		}
	}
}
