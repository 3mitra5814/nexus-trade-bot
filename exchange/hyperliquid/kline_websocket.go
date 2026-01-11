package hyperliquidex

import (
	"context"
	"fmt"
	"sync"

	hl "github.com/sonirico/go-hyperliquid"

	"nexus-trade-bot/logger"
)

type KlineWebSocketManager struct {
	mu      sync.RWMutex
	running bool
	ws      *hl.WebsocketClient
	subs    []*hl.Subscription
}

func NewKlineWebSocketManager() *KlineWebSocketManager {
	return &KlineWebSocketManager{}
}

func (k *KlineWebSocketManager) Start(ctx context.Context, coins []string, interval string, callback func(candle interface{})) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.running {
		return fmt.Errorf("K线流已在运行")
	}

	ws := hl.NewWebsocketClient(hl.MainnetAPIURL)
	if err := ws.Connect(ctx); err != nil {
		return err
	}

	subs := make([]*hl.Subscription, 0, len(coins))
	for _, coin := range coins {
		sub, err := ws.Candles(hl.CandlesSubscriptionParams{Coin: coin, Interval: interval}, func(item hl.Candle, err error) {
			if err != nil {
				logger.Warn("⚠️ [Hyperliquid] K线流错误: %v", err)
				return
			}
			callback(&Candle{
				Symbol:    toDisplaySymbol(item.Symbol),
				Open:      parseFloat(item.Open),
				High:      parseFloat(item.High),
				Low:       parseFloat(item.Low),
				Close:     parseFloat(item.Close),
				Volume:    parseFloat(item.Volume),
				Timestamp: item.TimeOpen,
				IsClosed:  true,
			})
		})
		if err != nil {
			_ = ws.Close()
			return err
		}
		subs = append(subs, sub)
	}

	k.ws = ws
	k.subs = subs
	k.running = true
	go func() {
		<-ctx.Done()
		k.Stop()
	}()
	return nil
}

func (k *KlineWebSocketManager) Stop() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, sub := range k.subs {
		if sub != nil {
			sub.Close()
		}
	}
	k.subs = nil
	if k.ws != nil {
		_ = k.ws.Close()
		k.ws = nil
	}
	k.running = false
}
