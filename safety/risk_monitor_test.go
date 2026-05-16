package safety

import (
	"context"
	"fmt"
	"testing"
	"time"

	"nexus-trade-bot/config"
	"nexus-trade-bot/exchange"
)

func TestRiskMonitorShortDirectionTreatsDownMoveAsFavorable(t *testing.T) {
	cfg := riskMonitorTestConfig("short")
	monitor := NewRiskMonitor(cfg, nil)
	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = appendRiskTestCandles(20, 100, 100)
	data.candles = append(data.candles, &exchange.Candle{Symbol: "BTCUSDT", Close: 90, Volume: 1000, IsClosed: false})

	triggered, reason := monitor.checkSymbol("BTCUSDT")
	if triggered {
		t.Fatalf("short risk monitor should not trigger on favorable down move, reason=%s", reason)
	}
}

func TestRiskMonitorShortDirectionTriggersOnUpMove(t *testing.T) {
	cfg := riskMonitorTestConfig("short")
	monitor := NewRiskMonitor(cfg, nil)
	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = appendRiskTestCandles(20, 100, 100)
	data.candles = append(data.candles, &exchange.Candle{Symbol: "BTCUSDT", Close: 110, Volume: 1000, IsClosed: false})

	triggered, reason := monitor.checkSymbol("BTCUSDT")
	if !triggered {
		t.Fatalf("short risk monitor should trigger on adverse up move")
	}
	if reason == "" {
		t.Fatalf("expected trigger reason")
	}
}

func TestRiskMonitorLongDirectionTriggersOnDownMove(t *testing.T) {
	cfg := riskMonitorTestConfig("long")
	monitor := NewRiskMonitor(cfg, nil)
	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = appendRiskTestCandles(20, 100, 100)
	data.candles = append(data.candles, &exchange.Candle{Symbol: "BTCUSDT", Close: 90, Volume: 1000, IsClosed: false})

	triggered, reason := monitor.checkSymbol("BTCUSDT")
	if !triggered {
		t.Fatalf("long risk monitor should trigger on adverse down move")
	}
	if reason == "" {
		t.Fatalf("expected trigger reason")
	}
}

func TestRiskMonitorNeutralIgnoresSmallDeviation(t *testing.T) {
	cfg := riskMonitorTestConfig("neutral")
	cfg.Trading.PriceInterval = 1
	monitor := NewRiskMonitor(cfg, nil)
	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = appendRiskTestCandles(20, 100, 100)
	data.candles = append(data.candles, &exchange.Candle{Symbol: "BTCUSDT", Close: 100.5, Volume: 1000, IsClosed: false})

	triggered, reason := monitor.checkSymbol("BTCUSDT")
	if triggered {
		t.Fatalf("neutral risk monitor should ignore sub-threshold deviation, reason=%s", reason)
	}
}

func TestRiskMonitorNeutralTriggersOnLargeDeviation(t *testing.T) {
	cfg := riskMonitorTestConfig("neutral")
	cfg.Trading.PriceInterval = 1
	monitor := NewRiskMonitor(cfg, nil)
	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = appendRiskTestCandles(20, 100, 100)
	data.candles = append(data.candles, &exchange.Candle{Symbol: "BTCUSDT", Close: 101.5, Volume: 1000, IsClosed: false})

	triggered, reason := monitor.checkSymbol("BTCUSDT")
	if !triggered {
		t.Fatalf("neutral risk monitor should trigger on threshold-sized deviation")
	}
	if reason == "" {
		t.Fatalf("expected trigger reason")
	}
}

func TestRiskMonitorNeutralRecoveryRequiresPriceNearAverage(t *testing.T) {
	cfg := riskMonitorTestConfig("neutral")
	cfg.Trading.PriceInterval = 1
	monitor := NewRiskMonitor(cfg, nil)
	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = appendRiskTestCandles(20, 100, 100)
	data.candles = append(data.candles, &exchange.Candle{Symbol: "BTCUSDT", Close: 101.5, Volume: 100, IsClosed: true})

	recovered, reason := monitor.checkSymbolRecovery("BTCUSDT")
	if recovered {
		t.Fatalf("neutral recovery must require price back near average, reason=%s", reason)
	}

	data.candles[len(data.candles)-1] = &exchange.Candle{Symbol: "BTCUSDT", Close: 100.5, Volume: 100, IsClosed: true}
	recovered, reason = monitor.checkSymbolRecovery("BTCUSDT")
	if !recovered {
		t.Fatalf("expected neutral monitor to recover near average, reason=%s", reason)
	}
}

func TestRiskMonitorRefreshesStaleKlinesFromBackupAndRecovers(t *testing.T) {
	cfg := riskMonitorTestConfig("long")
	now := time.Now()
	monitor := NewRiskMonitor(cfg, &failingKlineExchange{})
	monitor.backupKlines = backupKlineProvider{
		candles: append(buildRiskTestCandlesAt("BTCUSDT", 20, 100, 100, now.Add(-21*time.Minute), time.Minute), &exchange.Candle{
			Symbol:    "BTCUSDT",
			Close:     101,
			Volume:    100,
			Timestamp: now.Add(-time.Minute).UnixMilli(),
			IsClosed:  true,
		}),
	}
	monitor.triggered = true

	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = buildRiskTestCandlesAt("BTCUSDT", 20, 100, 100, now.Add(-40*time.Minute), time.Minute)
	data.candles = append(data.candles, &exchange.Candle{
		Symbol:    "BTCUSDT",
		Close:     101,
		Volume:    100,
		Timestamp: now.Add(-10 * time.Minute).UnixMilli(),
		IsClosed:  true,
	})

	if !monitor.refreshStaleKlines(context.Background(), true) {
		t.Fatalf("expected stale K lines to be refreshed")
	}
	if monitor.IsTriggered() {
		t.Fatalf("expected backup K line refresh to clear risk trigger")
	}
}

func TestRiskMonitorDoesNotRefreshFreshKlines(t *testing.T) {
	cfg := riskMonitorTestConfig("long")
	now := time.Now()
	backup := &countingBackupKlineProvider{
		candles: buildRiskTestCandlesAt("BTCUSDT", 21, 100, 100, now.Add(-21*time.Minute), time.Minute),
	}
	monitor := NewRiskMonitor(cfg, &failingKlineExchange{})
	monitor.backupKlines = backup
	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = buildRiskTestCandlesAt("BTCUSDT", 20, 100, 100, now.Add(-20*time.Minute), time.Minute)
	data.candles = append(data.candles, &exchange.Candle{
		Symbol:    "BTCUSDT",
		Close:     99,
		Volume:    100,
		Timestamp: now.Add(-30 * time.Second).UnixMilli(),
		IsClosed:  true,
	})

	if monitor.refreshStaleKlines(context.Background(), true) {
		t.Fatalf("did not expect fresh K lines to be refreshed")
	}
	if backup.calls != 0 {
		t.Fatalf("backup provider should not be called for fresh data, calls=%d", backup.calls)
	}
}

func TestRiskMonitorKeepsTriggeredWhenKlinesRemainStale(t *testing.T) {
	cfg := riskMonitorTestConfig("long")
	now := time.Now()
	monitor := NewRiskMonitor(cfg, &failingKlineExchange{})
	monitor.backupKlines = backupKlineProvider{
		candles: buildRiskTestCandlesAt("BTCUSDT", 21, 101, 100, now.Add(-40*time.Minute), time.Minute),
	}
	monitor.triggered = false

	data := monitor.symbolDataMap["BTCUSDT"]
	data.candles = buildRiskTestCandlesAt("BTCUSDT", 21, 101, 100, now.Add(-60*time.Minute), time.Minute)

	monitor.reportStatus(context.Background())
	if !monitor.IsTriggered() {
		t.Fatalf("expected stale K lines to keep risk monitor in protection mode")
	}
}

func riskMonitorTestConfig(direction string) *config.Config {
	cfg := &config.Config{}
	cfg.Trading.Direction = direction
	cfg.RiskControl.Enabled = true
	cfg.RiskControl.MonitorSymbols = []string{"BTCUSDT"}
	cfg.RiskControl.VolumeMultiplier = 3
	cfg.RiskControl.AverageWindow = 20
	cfg.RiskControl.RecoveryThreshold = 1
	return cfg
}

func appendRiskTestCandles(count int, close, volume float64) []*exchange.Candle {
	candles := make([]*exchange.Candle, 0, count)
	for i := 0; i < count; i++ {
		candles = append(candles, &exchange.Candle{Symbol: "BTCUSDT", Close: close, Volume: volume, IsClosed: true})
	}
	return candles
}

func buildRiskTestCandlesAt(symbol string, count int, close, volume float64, start time.Time, step time.Duration) []*exchange.Candle {
	candles := make([]*exchange.Candle, 0, count)
	for i := 0; i < count; i++ {
		candles = append(candles, &exchange.Candle{
			Symbol:    symbol,
			Close:     close,
			Volume:    volume,
			Timestamp: start.Add(time.Duration(i) * step).UnixMilli(),
			IsClosed:  true,
		})
	}
	return candles
}

type failingKlineExchange struct {
	exchange.IExchange
}

func (f *failingKlineExchange) GetHistoricalKlines(context.Context, string, string, int) ([]*exchange.Candle, error) {
	return nil, fmt.Errorf("primary unavailable")
}

type backupKlineProvider struct {
	candles []*exchange.Candle
}

func (b backupKlineProvider) GetHistoricalKlines(context.Context, string, string, int) ([]*exchange.Candle, error) {
	return append([]*exchange.Candle(nil), b.candles...), nil
}

type countingBackupKlineProvider struct {
	candles []*exchange.Candle
	calls   int
}

func (b *countingBackupKlineProvider) GetHistoricalKlines(context.Context, string, string, int) ([]*exchange.Candle, error) {
	b.calls++
	return append([]*exchange.Candle(nil), b.candles...), nil
}
