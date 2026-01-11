package tradestats

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexus-trade-bot/utils"
)

type Snapshot struct {
	TotalRealizedPNL float64              `json:"total_realized_pnl"`
	UnrealizedPNL    float64              `json:"unrealized_pnl"`
	TotalVolume      float64              `json:"total_volume"`
	TotalBuyQty      float64              `json:"total_buy_qty,omitempty"`
	TotalSellQty     float64              `json:"total_sell_qty,omitempty"`
	LastMarkPrice    float64              `json:"last_mark_price,omitempty"`
	Daily            map[string]DailyStat `json:"daily,omitempty"`
	Orders           map[string]OrderStat `json:"orders,omitempty"`
	RecentTrades     []TradeRecord        `json:"recent_trades,omitempty"`
	UpdatedAt        time.Time            `json:"updated_at,omitempty"`
}

type DailyStat struct {
	RealizedPNL float64 `json:"realized_pnl"`
	Volume      float64 `json:"volume"`
	BuyQty      float64 `json:"buy_qty,omitempty"`
	SellQty     float64 `json:"sell_qty,omitempty"`
}

type OrderStat struct {
	ExecutedQty float64 `json:"executed_qty"`
}

type TradeRecord struct {
	Time          time.Time `json:"time"`
	Symbol        string    `json:"symbol,omitempty"`
	ClientOrderID string    `json:"client_order_id,omitempty"`
	Side          string    `json:"side"`
	BookSide      string    `json:"book_side,omitempty"`
	Quantity      float64   `json:"quantity"`
	Price         float64   `json:"price"`
	RealizedPNL   float64   `json:"realized_pnl,omitempty"`
}

type LogTotals struct {
	BuyQty          float64
	SellQty         float64
	EstimatedProfit float64
	Time            time.Time
}

type Update struct {
	Symbol        string
	ClientOrderID string
	Side          string
	ExecutedQty   float64
	AvgPrice      float64
	Price         float64
	Status        string
	UpdateTime    int64
}

type Recorder struct {
	path           string
	priceDecimals  int
	priceInterval  float64
	feeRate        float64
	mu             sync.Mutex
	snapshot       Snapshot
	snapshotLoaded bool
}

func PathForConfig(configPath string) string {
	return configPath + ".stats.json"
}

func NewRecorder(path string, priceDecimals int, priceInterval, feeRate float64) *Recorder {
	return &Recorder{
		path:          path,
		priceDecimals: priceDecimals,
		priceInterval: priceInterval,
		feeRate:       feeRate,
	}
}

func Load(path string) (Snapshot, error) {
	var snap Snapshot
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return normalizeSnapshot(snap), nil
		}
		return normalizeSnapshot(snap), err
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return normalizeSnapshot(Snapshot{}), err
	}
	return normalizeSnapshot(snap), nil
}

func LoadWithLogFallback(statsPath string, logPath string, markPrice float64) (Snapshot, error) {
	snap, err := Load(statsPath)
	if markPrice <= 0 && snap.LastMarkPrice > 0 {
		markPrice = snap.LastMarkPrice
	}
	if logPath == "" {
		return snap, err
	}
	logTotals, ok := ParseLatestLogTotals(logPath)
	if !ok {
		return snap, err
	}
	fallback := snapshotFromLogTotals(logTotals, markPrice)
	if fallback.TotalVolume > snap.TotalVolume {
		snap.TotalVolume = fallback.TotalVolume
	}
	if fallback.TotalBuyQty > snap.TotalBuyQty {
		snap.TotalBuyQty = fallback.TotalBuyQty
	}
	if fallback.TotalSellQty > snap.TotalSellQty {
		snap.TotalSellQty = fallback.TotalSellQty
	}
	if fallback.LastMarkPrice > 0 {
		snap.LastMarkPrice = fallback.LastMarkPrice
	}
	if snap.UnrealizedPNL == 0 {
		snap.UnrealizedPNL = fallback.UnrealizedPNL
	}
	if math.Abs(fallback.TotalRealizedPNL) > math.Abs(snap.TotalRealizedPNL) {
		snap.TotalRealizedPNL = fallback.TotalRealizedPNL
	}
	for day, value := range fallback.Daily {
		current := snap.Daily[day]
		if value.Volume > current.Volume {
			current.Volume = value.Volume
		}
		if value.BuyQty > current.BuyQty {
			current.BuyQty = value.BuyQty
		}
		if value.SellQty > current.SellQty {
			current.SellQty = value.SellQty
		}
		if math.Abs(value.RealizedPNL) > math.Abs(current.RealizedPNL) {
			current.RealizedPNL = value.RealizedPNL
		}
		snap.Daily[day] = current
	}
	if fallback.UpdatedAt.After(snap.UpdatedAt) {
		snap.UpdatedAt = fallback.UpdatedAt
	}
	return snap, err
}

func ParseLatestLogTotals(logPath string) (LogTotals, bool) {
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return LogTotals{}, false
	}
	if len(data) > 512*1024 {
		data = data[len(data)-512*1024:]
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if totals, ok := parseLogTotalsLine(lines[i]); ok {
			if info, err := os.Stat(logPath); err == nil {
				totals.Time = info.ModTime()
			}
			if totals.Time.IsZero() {
				totals.Time = time.Now()
			}
			return totals, true
		}
	}
	return LogTotals{}, false
}

func (r *Recorder) Record(update Update) error {
	return r.RecordBatch([]Update{update})
}

func (r *Recorder) RecordBatch(updates []Update) error {
	if len(updates) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return err
	}
	changed := false
	for _, update := range updates {
		applied, err := r.recordLoaded(update)
		if err != nil {
			return err
		}
		if applied {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return r.saveLocked()
}

func (r *Recorder) recordLoaded(update Update) (bool, error) {
	clientOrderID := strings.TrimSpace(update.ClientOrderID)
	status := strings.ToUpper(strings.TrimSpace(update.Status))
	if clientOrderID == "" || update.ExecutedQty <= 0 || !isFillBearingStatus(status) {
		return false, nil
	}
	normalizedClientOrderID := normalizeClientOrderID(clientOrderID)
	entryPrice, side, bookSide, _, ok := utils.ParseOrderID(normalizedClientOrderID, r.priceDecimals)
	if !ok || entryPrice <= 0 {
		return false, nil
	}

	order := r.snapshot.Orders[normalizedClientOrderID]
	deltaQty := update.ExecutedQty - order.ExecutedQty
	if deltaQty <= 0 {
		return false, nil
	}
	order.ExecutedQty = update.ExecutedQty
	r.snapshot.Orders[normalizedClientOrderID] = order

	tradePrice := firstPositive(update.AvgPrice, update.Price, expectedTradePrice(entryPrice, side, bookSide, r.priceInterval))
	volume := deltaQty * tradePrice
	realized := realizedPNL(entryPrice, tradePrice, deltaQty, side, bookSide, r.feeRate)
	dateKey := dateKeyForUpdate(update.UpdateTime)

	daily := r.snapshot.Daily[dateKey]
	daily.Volume += volume
	daily.RealizedPNL += realized
	r.snapshot.Daily[dateKey] = daily
	r.snapshot.TotalVolume += volume
	r.snapshot.TotalRealizedPNL += realized
	now := time.Now()
	r.snapshot.RecentTrades = append(r.snapshot.RecentTrades, TradeRecord{
		Time:          timeForUpdate(update.UpdateTime, now),
		Symbol:        normalizeSymbol(update.Symbol),
		ClientOrderID: normalizedClientOrderID,
		Side:          side,
		BookSide:      bookSide,
		Quantity:      deltaQty,
		Price:         tradePrice,
		RealizedPNL:   realized,
	})
	if len(r.snapshot.RecentTrades) > 100 {
		r.snapshot.RecentTrades = r.snapshot.RecentTrades[len(r.snapshot.RecentTrades)-100:]
	}
	r.snapshot.UpdatedAt = now
	return true, nil
}

func isFillBearingStatus(status string) bool {
	switch status {
	case "PARTIALLY_FILLED", "FILLED", "CANCELED", "CANCELLED", "EXPIRED":
		return true
	default:
		return false
	}
}

func normalizeClientOrderID(clientOrderID string) string {
	clientOrderID = strings.TrimSpace(clientOrderID)
	for _, exchange := range []string{"binance", "gate"} {
		withoutPrefix := utils.RemoveBrokerPrefix(exchange, clientOrderID)
		if withoutPrefix != clientOrderID {
			return withoutPrefix
		}
	}
	return clientOrderID
}

func normalizeSymbol(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	symbol = strings.ReplaceAll(symbol, "/", "")
	symbol = strings.ReplaceAll(symbol, "_", "")
	symbol = strings.ReplaceAll(symbol, "-", "")
	return symbol
}

func FilterTradesBySymbol(trades []TradeRecord, symbol string) []TradeRecord {
	normalizedSymbol := normalizeSymbol(symbol)
	if normalizedSymbol == "" {
		return trades
	}
	filtered := make([]TradeRecord, 0, len(trades))
	for _, trade := range trades {
		tradeSymbol := normalizeSymbol(trade.Symbol)
		if tradeSymbol == "" || tradeSymbol == normalizedSymbol {
			filtered = append(filtered, trade)
		}
	}
	return filtered
}

func (r *Recorder) RecordTotals(buyQty, sellQty, markPrice, realizedPNL, unrealizedPNL float64) error {
	if buyQty <= 0 && sellQty <= 0 && realizedPNL == 0 && unrealizedPNL == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return err
	}
	volume := 0.0
	if markPrice > 0 && !math.IsNaN(markPrice) && !math.IsInf(markPrice, 0) {
		volume = (buyQty + sellQty) * markPrice
	}
	now := time.Now()
	day := now.Format("2006-01-02")
	daily := r.snapshot.Daily[day]
	r.snapshot.TotalBuyQty = math.Max(r.snapshot.TotalBuyQty, buyQty)
	r.snapshot.TotalSellQty = math.Max(r.snapshot.TotalSellQty, sellQty)
	if markPrice > 0 && !math.IsNaN(markPrice) && !math.IsInf(markPrice, 0) {
		r.snapshot.LastMarkPrice = markPrice
	}
	daily.BuyQty = math.Max(daily.BuyQty, buyQty)
	daily.SellQty = math.Max(daily.SellQty, sellQty)
	if volume > r.snapshot.TotalVolume {
		r.snapshot.TotalVolume = volume
	}
	if volume > daily.Volume {
		daily.Volume = volume
	}
	if len(r.snapshot.Orders) == 0 && math.Abs(realizedPNL) > math.Abs(r.snapshot.TotalRealizedPNL) {
		r.snapshot.TotalRealizedPNL = realizedPNL
	}
	if len(r.snapshot.Orders) == 0 && math.Abs(realizedPNL) > math.Abs(daily.RealizedPNL) {
		daily.RealizedPNL = realizedPNL
	}
	r.snapshot.UnrealizedPNL = unrealizedPNL
	r.snapshot.Daily[day] = daily
	r.snapshot.UpdatedAt = now
	return r.saveLocked()
}

var logTotalsPattern = regexp.MustCompile(`累计买入:\s*([0-9]+(?:\.[0-9]+)?)\s*,\s*累计卖出:\s*([0-9]+(?:\.[0-9]+)?)\s*,\s*(?:已实现盈亏|预计盈利):\s*([-+]?[0-9]+(?:\.[0-9]+)?)`)

func parseLogTotalsLine(line string) (LogTotals, bool) {
	match := logTotalsPattern.FindStringSubmatch(line)
	if len(match) != 4 {
		return LogTotals{}, false
	}
	buyQty, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return LogTotals{}, false
	}
	sellQty, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return LogTotals{}, false
	}
	estimatedProfit, err := strconv.ParseFloat(match[3], 64)
	if err != nil {
		return LogTotals{}, false
	}
	return LogTotals{BuyQty: buyQty, SellQty: sellQty, EstimatedProfit: estimatedProfit}, true
}

func snapshotFromLogTotals(totals LogTotals, markPrice float64) Snapshot {
	snap := normalizeSnapshot(Snapshot{})
	volume := 0.0
	if markPrice > 0 && !math.IsNaN(markPrice) && !math.IsInf(markPrice, 0) {
		volume = (totals.BuyQty + totals.SellQty) * markPrice
		snap.LastMarkPrice = markPrice
	}
	snap.TotalVolume = volume
	snap.TotalRealizedPNL = totals.EstimatedProfit
	snap.TotalBuyQty = totals.BuyQty
	snap.TotalSellQty = totals.SellQty
	if totals.Time.IsZero() {
		totals.Time = time.Now()
	}
	snap.UpdatedAt = totals.Time
	day := totals.Time.Format("2006-01-02")
	snap.Daily[day] = DailyStat{RealizedPNL: totals.EstimatedProfit, Volume: volume, BuyQty: totals.BuyQty, SellQty: totals.SellQty}
	return snap
}

func (r *Recorder) loadLocked() error {
	if r.snapshotLoaded {
		return nil
	}
	snap, err := Load(r.path)
	if err != nil {
		return err
	}
	r.snapshot = snap
	r.snapshotLoaded = true
	return nil
}

func (r *Recorder) saveLocked() error {
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r.snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".stats-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, r.path)
}

func normalizeSnapshot(snap Snapshot) Snapshot {
	if snap.Daily == nil {
		snap.Daily = make(map[string]DailyStat)
	}
	if snap.Orders == nil {
		snap.Orders = make(map[string]OrderStat)
	}
	if len(snap.RecentTrades) > 100 {
		snap.RecentTrades = snap.RecentTrades[len(snap.RecentTrades)-100:]
	}
	return snap
}

func Today(snap Snapshot) DailyStat {
	snap = normalizeSnapshot(snap)
	return snap.Daily[time.Now().Format("2006-01-02")]
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			return value
		}
	}
	return 0
}

func expectedTradePrice(entryPrice float64, side string, bookSide string, interval float64) float64 {
	side = strings.ToUpper(side)
	bookSide = strings.ToUpper(bookSide)
	if bookSide == "LONG" && side == "SELL" {
		return entryPrice + interval
	}
	if bookSide == "SHORT" && side == "BUY" {
		return entryPrice - interval
	}
	return entryPrice
}

func realizedPNL(entryPrice, tradePrice, qty float64, side string, bookSide string, feeRate float64) float64 {
	side = strings.ToUpper(side)
	bookSide = strings.ToUpper(bookSide)
	isLongExit := bookSide == "LONG" && side == "SELL"
	isShortExit := bookSide == "SHORT" && side == "BUY"
	if !isLongExit && !isShortExit {
		return 0
	}
	gross := (tradePrice - entryPrice) * qty
	if isShortExit {
		gross = (entryPrice - tradePrice) * qty
	}
	fees := (entryPrice + tradePrice) * qty * feeRate
	return gross - fees
}

func dateKeyForUpdate(updateTime int64) string {
	if updateTime > 0 {
		if updateTime > 1_000_000_000_000 {
			return time.UnixMilli(updateTime).Format("2006-01-02")
		}
		return time.Unix(updateTime, 0).Format("2006-01-02")
	}
	return time.Now().Format("2006-01-02")
}

func timeForUpdate(updateTime int64, fallback time.Time) time.Time {
	if updateTime > 0 {
		if updateTime > 1_000_000_000_000 {
			return time.UnixMilli(updateTime)
		}
		return time.Unix(updateTime, 0)
	}
	if fallback.IsZero() {
		return time.Now()
	}
	return fallback
}
