package safety

import (
	"testing"

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
