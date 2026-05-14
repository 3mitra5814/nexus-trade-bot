package tradestats

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestRecorderCountsVolumeAndLongRealizedPNLByDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot.yaml.stats.json")
	recorder := NewRecorder(path, 2, 1, 0.001)

	if err := recorder.Record(Update{
		ClientOrderID: "300000_B_L_1700000000001",
		Side:          "BUY",
		ExecutedQty:   0.01,
		AvgPrice:      3000,
		Status:        "FILLED",
		UpdateTime:    1700000000000,
	}); err != nil {
		t.Fatalf("record entry: %v", err)
	}
	if err := recorder.Record(Update{
		ClientOrderID: "300000_S_L_1700000000002",
		Side:          "SELL",
		ExecutedQty:   0.01,
		AvgPrice:      3001,
		Status:        "PARTIALLY_FILLED",
		UpdateTime:    1700000000000,
	}); err != nil {
		t.Fatalf("record exit: %v", err)
	}
	if err := recorder.Record(Update{
		ClientOrderID: "300000_S_L_1700000000002",
		Side:          "SELL",
		ExecutedQty:   0.02,
		AvgPrice:      3001,
		Status:        "FILLED",
		UpdateTime:    1700000000000,
	}); err != nil {
		t.Fatalf("record exit delta: %v", err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	assertNear(t, snap.TotalVolume, 90.02)
	assertNear(t, snap.TotalRealizedPNL, -0.10002)
	var daily DailyStat
	for _, item := range snap.Daily {
		daily = item
	}
	assertNear(t, daily.Volume, 90.02)
	assertNear(t, daily.RealizedPNL, -0.10002)
}

func TestRecorderCountsShortRealizedPNL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot.yaml.stats.json")
	recorder := NewRecorder(path, 2, 1, 0)

	if err := recorder.Record(Update{
		ClientOrderID: "300000_B_S_1700000000001",
		Side:          "BUY",
		ExecutedQty:   0.02,
		AvgPrice:      2999,
		Status:        "FILLED",
		UpdateTime:    1700000000000,
	}); err != nil {
		t.Fatalf("record short exit: %v", err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	assertNear(t, snap.TotalRealizedPNL, 0.02)
}

func TestRecorderRealizedPNLUsesActualEntryAveragePrice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot.yaml.stats.json")
	recorder := NewRecorder(path, 2, 1, 0.001)

	if err := recorder.Record(Update{
		ClientOrderID: "300000_B_L_1700000000001",
		Side:          "BUY",
		ExecutedQty:   0.01,
		AvgPrice:      2999.5,
		Status:        "FILLED",
		UpdateTime:    1700000000000,
	}); err != nil {
		t.Fatalf("record entry: %v", err)
	}
	if err := recorder.Record(Update{
		ClientOrderID: "300000_S_L_1700000000002",
		Side:          "SELL",
		ExecutedQty:   0.01,
		AvgPrice:      3001,
		Status:        "FILLED",
		UpdateTime:    1700000000000,
	}); err != nil {
		t.Fatalf("record exit: %v", err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	assertNear(t, snap.TotalRealizedPNL, realizedPNL(2999.5, 3001, 0.01, "SELL", "LONG", 0.001))
}

func TestRecorderCountsPartiallyFilledCanceledDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot.yaml.stats.json")
	recorder := NewRecorder(path, 2, 1, 0)

	if err := recorder.Record(Update{
		ClientOrderID: "300000_B_L_1700000000001",
		Side:          "BUY",
		ExecutedQty:   0.01,
		AvgPrice:      3000,
		Status:        "CANCELED",
		UpdateTime:    1700000000000,
	}); err != nil {
		t.Fatalf("record canceled fill: %v", err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	assertNear(t, snap.TotalVolume, 30)
}

func TestRecorderStoresRecentTradesByDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot.yaml.stats.json")
	recorder := NewRecorder(path, 2, 1, 0)

	if err := recorder.Record(Update{
		Symbol:        "ETHUSDT",
		ClientOrderID: "300000_S_L_1700000000001",
		ExecutedQty:   0.01,
		AvgPrice:      3001,
		Status:        "PARTIALLY_FILLED",
		UpdateTime:    1700000000000,
	}); err != nil {
		t.Fatalf("record partial: %v", err)
	}
	if err := recorder.Record(Update{
		ClientOrderID: "300000_S_L_1700000000001",
		ExecutedQty:   0.03,
		AvgPrice:      3001,
		Status:        "FILLED",
		UpdateTime:    1700000001000,
	}); err != nil {
		t.Fatalf("record fill: %v", err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.RecentTrades) != 2 {
		t.Fatalf("got %d recent trades want 2", len(snap.RecentTrades))
	}
	if snap.RecentTrades[0].Symbol != "ETHUSDT" {
		t.Fatalf("recent trade symbol was not stored: %+v", snap.RecentTrades[0])
	}
	assertNear(t, snap.RecentTrades[0].Quantity, 0.01)
	assertNear(t, snap.RecentTrades[1].Quantity, 0.02)
	assertNear(t, snap.RecentTrades[1].Price, 3001)
	assertNear(t, snap.RecentTrades[0].PositionDelta, -0.01)
	assertNear(t, snap.RecentTrades[0].PositionAfter, -0.02)
	assertNear(t, snap.RecentTrades[1].PositionDelta, -0.02)
	assertNear(t, snap.RecentTrades[1].PositionAfter, -0.04)
	if snap.RecentTrades[1].Side != "SELL" || snap.RecentTrades[1].BookSide != "LONG" {
		t.Fatalf("unexpected trade side: %+v", snap.RecentTrades[1])
	}
}

func TestFilterTradesBySymbolKeepsOnlyMatchingSymbol(t *testing.T) {
	trades := []TradeRecord{
		{Symbol: "ETHUSDT", Quantity: 1},
		{Symbol: "BTC_USDT", Quantity: 2},
		{Quantity: 3},
	}
	filtered := FilterTradesBySymbol(trades, "ETH/USDT")
	if len(filtered) != 2 {
		t.Fatalf("got %d filtered trades want 2: %+v", len(filtered), filtered)
	}
	assertNear(t, filtered[0].Quantity, 1)
	assertNear(t, filtered[1].Quantity, 3)
}

func TestRecorderTrimsRecentTradesToLatest100(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot.yaml.stats.json")
	recorder := NewRecorder(path, 2, 1, 0)

	for i := 0; i < 105; i++ {
		id := "300000_S_L_" + strconv.FormatInt(1700000000+int64(i), 10)
		if err := recorder.Record(Update{
			ClientOrderID: id,
			ExecutedQty:   0.01,
			AvgPrice:      3001 + float64(i),
			Status:        "FILLED",
			UpdateTime:    1700000000000 + int64(i),
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.RecentTrades) != 100 {
		t.Fatalf("got %d recent trades want 100", len(snap.RecentTrades))
	}
	assertNear(t, snap.RecentTrades[0].Price, 3006)
	assertNear(t, snap.RecentTrades[99].Price, 3105)
}

func TestLoadWithLogFallbackUsesLatestStatsLine(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "robot.log")
	if err := os.WriteFile(logPath, []byte(`
	2026/05/09 22:40:18 [INFO] 📊 [统计] 对账次数: 1, 累计买入: 12.50, 累计卖出: 3.50, 预计盈利: 1.75 USD
	2026/05/09 22:41:18 [INFO] 📊 [统计] 对账次数: 2, 累计买入: 258.60, 累计卖出: 10.00, 已实现盈亏: 5.00 USD, 未实现盈亏: -0.50 USD
`), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	modTime := time.Now()
	if err := os.Chtimes(logPath, modTime, modTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	snap, err := LoadWithLogFallback(filepath.Join(dir, "missing.stats.json"), logPath, 0.25)
	if err != nil {
		t.Fatalf("load fallback: %v", err)
	}
	assertNear(t, snap.TotalVolume, 67.15)
	assertNear(t, snap.TotalRealizedPNL, 5.00)
	daily := Today(snap)
	assertNear(t, daily.Volume, 67.15)
	assertNear(t, daily.RealizedPNL, 5.00)
}

func TestRecordTotalsPersistsWorkerSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot.yaml.stats.json")
	recorder := NewRecorder(path, 2, 1, 0.001)

	if err := recorder.RecordTotals(258.6029, 0, 0.25, 0, -1.25); err != nil {
		t.Fatalf("record totals: %v", err)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	assertNear(t, snap.TotalBuyQty, 258.6029)
	assertNear(t, snap.TotalVolume, 64.650725)
	assertNear(t, snap.LastMarkPrice, 0.25)
	assertNear(t, snap.UnrealizedPNL, -1.25)
	daily := Today(snap)
	assertNear(t, daily.BuyQty, 258.6029)
	assertNear(t, daily.Volume, 64.650725)
}

func TestTradingDayKeyUsesConfiguredTimezone(t *testing.T) {
	t.Setenv("NEXUS_TRADE_BOT_TIMEZONE", "Asia/Hong_Kong")
	tradingDayLocationOnce = sync.Once{}
	tradingDayLocation = nil
	t.Cleanup(func() {
		tradingDayLocationOnce = sync.Once{}
		tradingDayLocation = nil
	})

	utcTime := time.Date(2026, 5, 13, 16, 30, 0, 0, time.UTC)
	if got := TradingDayKey(utcTime); got != "2026-05-14" {
		t.Fatalf("got %s want 2026-05-14", got)
	}
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f want %.12f", got, want)
	}
}
