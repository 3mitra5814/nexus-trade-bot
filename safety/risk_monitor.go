package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"nexus-trade-bot/config"
	"nexus-trade-bot/exchange"
	"nexus-trade-bot/logger"
	"strconv"
	"strings"
	"sync"
	"time"
)

const staleKlineThreshold = 2 * time.Minute

type klineProvider interface {
	GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*exchange.Candle, error)
}

// SymbolData 单个币种的K线数据缓存
type SymbolData struct {
	candles []*exchange.Candle
	mu      sync.RWMutex
}

func (s *SymbolData) snapshotCandles() []*exchange.Candle {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*exchange.Candle(nil), s.candles...)
}

func normalizeKlineCache(candles []*exchange.Candle, averageWindow int) []*exchange.Candle {
	if len(candles) == 0 {
		return candles
	}

	deduped := make([]*exchange.Candle, 0, len(candles))
	seen := make(map[int64]struct{}, len(candles))
	for i := len(candles) - 1; i >= 0; i-- {
		c := candles[i]
		if c == nil {
			continue
		}
		if _, exists := seen[c.Timestamp]; exists {
			continue
		}
		seen[c.Timestamp] = struct{}{}
		deduped = append(deduped, c)
	}
	for i, j := 0, len(deduped)-1; i < j; i, j = i+1, j-1 {
		deduped[i], deduped[j] = deduped[j], deduped[i]
	}

	maxCount := averageWindow + 2
	if maxCount < 2 {
		maxCount = 2
	}
	if len(deduped) > maxCount {
		deduped = deduped[len(deduped)-maxCount:]
	}
	return deduped
}

func candleTime(c *exchange.Candle) time.Time {
	if c == nil {
		return time.Time{}
	}
	if c.Timestamp > 10000000000 {
		return time.Unix(c.Timestamp/1000, 0)
	}
	return time.Unix(c.Timestamp, 0)
}

func latestCandleAge(candles []*exchange.Candle, inRiskControl bool) (time.Duration, bool) {
	for i := len(candles) - 1; i >= 0; i-- {
		c := candles[i]
		if c == nil {
			continue
		}
		if inRiskControl && !c.IsClosed {
			continue
		}
		t := candleTime(c)
		if t.IsZero() {
			continue
		}
		return time.Since(t), true
	}
	return 0, false
}

// RiskMonitor 主动安全风控监视器
type RiskMonitor struct {
	cfg           *config.Config
	exchange      exchange.IExchange
	backupKlines  klineProvider
	symbolDataMap map[string]*SymbolData
	mu            sync.RWMutex
	triggered     bool
	lastMsg       string
}

// NewRiskMonitor 创建风控监视器
func NewRiskMonitor(cfg *config.Config, ex exchange.IExchange) *RiskMonitor {
	symbolDataMap := make(map[string]*SymbolData)
	for _, symbol := range cfg.RiskControl.MonitorSymbols {
		symbolDataMap[symbol] = &SymbolData{
			candles: make([]*exchange.Candle, 0, cfg.RiskControl.AverageWindow+1),
		}
	}

	return &RiskMonitor{
		cfg:           cfg,
		exchange:      ex,
		backupKlines:  newBinancePublicKlineProvider(),
		symbolDataMap: symbolDataMap,
	}
}

// Start 启动监控
func (r *RiskMonitor) Start(ctx context.Context) {
	if !r.cfg.RiskControl.Enabled {
		logger.Info("⚠️ 主动安全风控未启用")
		return
	}

	logger.Info("🛡️ 启动主动安全风控监控 (周期: %s, 倍数: %.1f, 窗口: %d)",
		r.cfg.RiskControl.Interval, r.cfg.RiskControl.VolumeMultiplier, r.cfg.RiskControl.AverageWindow)
	logger.Info("🛡️ 监控币种: %v (恢复阈值: %d/%d)", r.cfg.RiskControl.MonitorSymbols,
		r.cfg.RiskControl.RecoveryThreshold, len(r.cfg.RiskControl.MonitorSymbols))

	// 预加载历史K线数据
	logger.Info("📊 正在加载历史K线数据...")
	for _, symbol := range r.cfg.RiskControl.MonitorSymbols {
		candles, source, err := r.fetchHistoricalKlines(ctx, symbol, r.cfg.RiskControl.AverageWindow+1)
		if err != nil {
			logger.Warn("⚠️ 加载 %s 历史K线失败: %v", symbol, err)
			continue
		}

		if len(candles) > 0 {
			r.mu.RLock()
			symbolData, exists := r.symbolDataMap[symbol]
			r.mu.RUnlock()

			if exists {
				symbolData.mu.Lock()
				symbolData.candles = normalizeKlineCache(candles, r.cfg.RiskControl.AverageWindow)
				symbolData.mu.Unlock()
				logger.Info("✅ %s: 已通过%s加载 %d 根历史K线", symbol, source, len(candles))
			}
		}
	}
	logger.Info("✅ 历史K线数据加载完成，风控系统已就绪")

	// 启动K线流
	if err := r.exchange.StartKlineStream(ctx, r.cfg.RiskControl.MonitorSymbols, r.cfg.RiskControl.Interval, r.onCandleUpdate); err != nil {
		msg := fmt.Sprintf("启动K线流失败，风控进入保护状态: %v", err)
		r.mu.Lock()
		r.triggered = true
		r.lastMsg = msg
		r.mu.Unlock()
		logger.Error("❌ %s", msg)
	} else {
		logger.Info("✅ 主交易所K线流已启动")
	}

	// 启动定期报告协程（每60秒）
	go r.reportLoop(ctx)
}

// onCandleUpdate K线更新回调（实时检测）
func (r *RiskMonitor) onCandleUpdate(candle *exchange.Candle) {
	defer recoverWorker("风控K线回调")
	if candle == nil {
		logger.Warn("⚠️ 收到空K线数据")
		return
	}
	c := candle

	// 更新缓存
	r.mu.RLock()
	symbolData, exists := r.symbolDataMap[c.Symbol]
	r.mu.RUnlock()

	if !exists {
		logger.Warn("⚠️ 收到未监控的币种K线: %s", c.Symbol)
		return
	}

	symbolData.mu.Lock()

	if c.IsClosed {
		// 完结的K线：追加到列表
		symbolData.candles = append(symbolData.candles, c)

		// 保留足够数量的完结K线（窗口大小）+ 可能的1根未完结K线
		// 只保留最近的完结K线，删除过旧的
		requiredClosedCount := r.cfg.RiskControl.AverageWindow
		closedCount := 0
		for i := len(symbolData.candles) - 1; i >= 0; i-- {
			if symbolData.candles[i].IsClosed {
				closedCount++
			}
		}

		// 如果完结K线超过需要的数量，从前面删除旧的
		if closedCount > requiredClosedCount+1 {
			// 找到需要保留的起始位置（从后往前数requiredClosedCount+1根完结K线）
			keepClosedCount := requiredClosedCount + 1
			foundCount := 0
			startIdx := len(symbolData.candles) - 1
			for i := len(symbolData.candles) - 1; i >= 0; i-- {
				if symbolData.candles[i].IsClosed {
					foundCount++
					if foundCount >= keepClosedCount {
						startIdx = i
						break
					}
				}
			}
			symbolData.candles = symbolData.candles[startIdx:]
		}
	} else {
		// 未完结的K线
		if len(symbolData.candles) > 0 && !symbolData.candles[len(symbolData.candles)-1].IsClosed {
			// 最后一根也是未完结的：更新它
			symbolData.candles[len(symbolData.candles)-1] = c
		} else {
			// 最后一根是完结的或列表为空：追加这个未完结K线
			symbolData.candles = append(symbolData.candles, c)
		}
	}
	currentCount := len(symbolData.candles)
	symbolData.mu.Unlock()

	// 只在完结K线时打印日志，避免日志过多
	if c.IsClosed {
		logger.Debug("📈 [K线收集] %s: 价格=%.4f, 成交量=%.0f, 完结=%v, 已缓存%d根",
			c.Symbol, c.Close, c.Volume, c.IsClosed, currentCount)
	}

	// 实时检测（使用最新数据，包括未完结的K线）
	r.checkMarket()
}

func (r *RiskMonitor) fetchHistoricalKlines(ctx context.Context, symbol string, limit int) ([]*exchange.Candle, string, error) {
	if r.exchange != nil {
		candles, err := r.exchange.GetHistoricalKlines(ctx, symbol, r.cfg.RiskControl.Interval, limit)
		if err == nil && len(candles) > 0 {
			return candles, "主交易所REST", nil
		}
		if err != nil {
			logger.Warn("⚠️ [K线补数] 主交易所REST获取 %s 失败: %v", symbol, err)
		}
	}

	if r.backupKlines == nil {
		return nil, "", fmt.Errorf("主交易所REST无数据且未配置备用K线源")
	}
	candles, err := r.backupKlines.GetHistoricalKlines(ctx, symbol, r.cfg.RiskControl.Interval, limit)
	if err != nil {
		return nil, "", fmt.Errorf("备用Binance公共K线获取失败: %w", err)
	}
	if len(candles) == 0 {
		return nil, "", fmt.Errorf("备用Binance公共K线返回空数据")
	}
	return candles, "Binance公共REST", nil
}

func (r *RiskMonitor) refreshStaleKlines(ctx context.Context, inRiskControl bool) bool {
	refreshed := false
	for _, symbol := range r.cfg.RiskControl.MonitorSymbols {
		r.mu.RLock()
		symbolData, exists := r.symbolDataMap[symbol]
		r.mu.RUnlock()
		if !exists {
			continue
		}

		candles := symbolData.snapshotCandles()
		age, ok := latestCandleAge(candles, inRiskControl)
		if ok && age <= staleKlineThreshold {
			continue
		}

		reason := "无可用K线"
		if ok {
			reason = fmt.Sprintf("最新K线%.0f秒未更新", age.Seconds())
		}
		limit := r.cfg.RiskControl.AverageWindow + 2
		newCandles, source, err := r.fetchHistoricalKlines(ctx, symbol, limit)
		if err != nil {
			logger.Warn("⚠️ [K线补数] %s %s，补数失败: %v", symbol, reason, err)
			continue
		}

		symbolData.mu.Lock()
		symbolData.candles = normalizeKlineCache(append(symbolData.candles, newCandles...), r.cfg.RiskControl.AverageWindow)
		symbolData.mu.Unlock()
		refreshed = true
		logger.Info("✅ [K线补数] %s %s，已通过%s刷新 %d 根K线", symbol, reason, source, len(newCandles))
	}

	if refreshed {
		r.checkMarket()
	}
	return refreshed
}

func (r *RiskMonitor) hasStaleKlines(inRiskControl bool) bool {
	for _, symbol := range r.cfg.RiskControl.MonitorSymbols {
		r.mu.RLock()
		symbolData, exists := r.symbolDataMap[symbol]
		r.mu.RUnlock()
		if !exists {
			return true
		}
		age, ok := latestCandleAge(symbolData.snapshotCandles(), inRiskControl)
		if !ok || age > staleKlineThreshold {
			return true
		}
	}
	return false
}

// checkMarket 执行市场检查（实时，无日志）
func (r *RiskMonitor) checkMarket() {
	// 先检查当前状态（不持有锁）
	r.mu.RLock()
	triggered := r.triggered
	r.mu.RUnlock()

	if triggered {
		// 已触发状态：检查是否可以解除
		canRecover, details := r.checkRecovery()

		r.mu.Lock()
		if canRecover {
			// 统计恢复的币种数量
			recoveredCount := 0
			for _, detail := range details {
				if !strings.Contains(detail, "未恢复") {
					recoveredCount++
				}
			}
			logger.Info("✅ 市场风险信号消失，解除风控限制。(%d/%d 币种已恢复正常，达到恢复阈值 %d)",
				recoveredCount, len(r.cfg.RiskControl.MonitorSymbols), r.cfg.RiskControl.RecoveryThreshold)
			logger.Info("详情: %s", strings.Join(details, ", "))
			r.triggered = false
			r.lastMsg = "已恢复正常"
		} else {
			r.lastMsg = fmt.Sprintf("风控中，等待恢复: %s", strings.Join(details, ","))
		}
		r.mu.Unlock()
	} else {
		// 未触发状态：检查是否需要触发
		panicCount := 0
		details := []string{}

		for _, symbol := range r.cfg.RiskControl.MonitorSymbols {
			isPanic, reason := r.checkSymbol(symbol)
			if isPanic {
				panicCount++
				details = append(details, fmt.Sprintf("%s(%s)", symbol, reason))
			}
		}

		// 多数监控币种异常即触发；要求全部异常会被单个数据缺失或断流拖住，实盘保护太迟钝。
		triggerThreshold := len(r.cfg.RiskControl.MonitorSymbols)/2 + 1
		r.mu.Lock()
		if panicCount > 0 && panicCount >= triggerThreshold {
			logger.Warn("🚨🚨🚨 触发主动安全风控！市场出现集体异动！🚨🚨🚨")
			logger.Warn("详情: %s", strings.Join(details, ", "))
			r.triggered = true
			r.lastMsg = fmt.Sprintf("触发风控: %d/%d 币种异常，阈值 %d (%s)", panicCount, len(r.cfg.RiskControl.MonitorSymbols), triggerThreshold, strings.Join(details, ","))
		} else {
			r.lastMsg = "监控正常"
		}
		r.mu.Unlock()
	}
}

// checkRecovery 检查是否可以解除风控（价格回到均线上方 + 成交量恢复正常）
func (r *RiskMonitor) checkRecovery() (bool, []string) {
	recoveredCount := 0
	details := []string{}

	for _, symbol := range r.cfg.RiskControl.MonitorSymbols {
		isRecovered, reason := r.checkSymbolRecovery(symbol)
		if isRecovered {
			recoveredCount++
			details = append(details, fmt.Sprintf("%s(%s)", symbol, reason))
		} else {
			details = append(details, fmt.Sprintf("%s(未恢复:%s)", symbol, reason))
		}
	}

	// 达到恢复阈值即可解除风控
	threshold := r.cfg.RiskControl.RecoveryThreshold
	return recoveredCount >= threshold, details
}

// checkSymbolRecovery 检查单个币种是否恢复（价格回到当前交易方向的非危险侧，且成交量恢复正常）
// 解除风控必须使用完结的K线数据
func (r *RiskMonitor) checkSymbolRecovery(symbol string) (bool, string) {
	symbolData, exists := r.symbolDataMap[symbol]
	if !exists {
		return false, "无数据"
	}

	candles := symbolData.snapshotCandles()
	candleCount := len(candles)

	if candleCount < r.cfg.RiskControl.AverageWindow+1 {
		return false, "数据不足"
	}

	// 找到最新的完结K线用于判断（如果最后一根是未完结的，使用倒数第二根）
	var currentCandle *exchange.Candle
	var currentPrice float64

	for i := candleCount - 1; i >= 0; i-- {
		if candles[i].IsClosed {
			currentCandle = candles[i]
			currentPrice = currentCandle.Close
			break
		}
	}

	if currentCandle == nil {
		return false, "无完结K线"
	}

	// 计算移动平均价格和移动平均成交量（只使用完结的K线，排除当前用于判断的这根）
	var totalPrice float64
	var totalVol float64
	var validCount int
	window := r.cfg.RiskControl.AverageWindow

	for i := candleCount - 1; i >= 0 && validCount < window; i-- {
		if candles[i].IsClosed && candles[i] != currentCandle {
			totalPrice += candles[i].Close
			totalVol += candles[i].Volume
			validCount++
		}
	}

	if validCount < window {
		return false, fmt.Sprintf("完结K线不足(%d<%d)", validCount, window)
	}

	avgPrice := totalPrice / float64(validCount)
	avgVol := totalVol / float64(validCount)

	// 恢复条件：价格回到非危险侧，且成交量恢复正常（与触发条件对应）
	priceRecovered := r.priceRecovered(currentPrice, avgPrice)
	volNormal := currentCandle.Volume < avgVol*r.cfg.RiskControl.VolumeMultiplier

	if priceRecovered && volNormal {
		return true, fmt.Sprintf("%s/量正常", r.recoveryPriceDescription())
	}

	// 返回未恢复原因
	if !priceRecovered {
		return false, r.unrecoveredPriceReason(currentPrice, avgPrice)
	}
	return false, fmt.Sprintf("量%.0f>均量×%.1f", currentCandle.Volume, r.cfg.RiskControl.VolumeMultiplier)
}

// checkSymbol 检查单个币种（基于移动平均线）
// 触发风控可以使用最新K线数据（包括未完结的K线），以便及时检测到异常
func (r *RiskMonitor) checkSymbol(symbol string) (bool, string) {
	r.mu.RLock()
	symbolData, exists := r.symbolDataMap[symbol]
	r.mu.RUnlock()

	if !exists {
		return false, ""
	}

	candles := symbolData.snapshotCandles()
	candleCount := len(candles)

	if candleCount < r.cfg.RiskControl.AverageWindow+1 {
		return false, ""
	}

	// 最新K线（可以是未完结的，用于实时检测）
	currentCandle := candles[candleCount-1]
	currentPrice := currentCandle.Close

	// 计算移动平均价格和移动平均成交量（使用历史完结的K线）
	var totalPrice float64
	var totalVol float64
	var validCount int
	window := r.cfg.RiskControl.AverageWindow

	// 从倒数第二根K线开始往前计算（排除当前可能未完结的K线）
	for i := candleCount - 2; i >= 0 && validCount < window; i-- {
		if candles[i].IsClosed {
			totalPrice += candles[i].Close
			totalVol += candles[i].Volume
			validCount++
		}
	}

	if validCount < window {
		return false, ""
	}

	avgPrice := totalPrice / float64(validCount)
	avgVol := totalVol / float64(validCount)

	// 计算当前价格偏离均线的百分比
	priceDeviation := (currentPrice - avgPrice) / avgPrice * 100
	volRatio := currentCandle.Volume / avgVol

	// 触发条件：当前价格进入当前交易方向的危险侧，且成交量放大（使用最新数据，包括未完结K线）
	if r.isAdversePriceMove(currentPrice, avgPrice) && currentCandle.Volume > avgVol*r.cfg.RiskControl.VolumeMultiplier {
		return true, fmt.Sprintf("%s/量×%.1f", r.adversePriceDescription(priceDeviation), volRatio)
	}

	return false, ""
}

func (r *RiskMonitor) tradingDirection() string {
	direction := strings.ToLower(strings.TrimSpace(r.cfg.Trading.Direction))
	switch direction {
	case "short", "neutral":
		return direction
	default:
		return "long"
	}
}

func (r *RiskMonitor) isAdversePriceMove(currentPrice, avgPrice float64) bool {
	switch r.tradingDirection() {
	case "short":
		return currentPrice > avgPrice
	case "neutral":
		return math.Abs(currentPrice-avgPrice) >= r.neutralDeviationThreshold(avgPrice)
	default:
		return currentPrice < avgPrice
	}
}

func (r *RiskMonitor) priceRecovered(currentPrice, avgPrice float64) bool {
	switch r.tradingDirection() {
	case "short":
		return currentPrice < avgPrice
	case "neutral":
		return math.Abs(currentPrice-avgPrice) < r.neutralDeviationThreshold(avgPrice)
	default:
		return currentPrice > avgPrice
	}
}

func (r *RiskMonitor) neutralDeviationThreshold(avgPrice float64) float64 {
	if r.cfg != nil && r.cfg.Trading.PriceInterval > 0 {
		return r.cfg.Trading.PriceInterval
	}
	if avgPrice <= 0 || math.IsNaN(avgPrice) || math.IsInf(avgPrice, 0) {
		return 0
	}
	return avgPrice * 0.003
}

func (r *RiskMonitor) adversePriceDescription(priceDeviation float64) string {
	switch r.tradingDirection() {
	case "short":
		return fmt.Sprintf("价格%.2f%%高于均线", priceDeviation)
	case "neutral":
		return fmt.Sprintf("价格偏离均线%.2f%%", priceDeviation)
	default:
		return fmt.Sprintf("价格%.2f%%低于均线", priceDeviation)
	}
}

func (r *RiskMonitor) recoveryPriceDescription() string {
	switch r.tradingDirection() {
	case "short":
		return "价格回到均线下方"
	case "neutral":
		return "价格风控解除"
	default:
		return "价格回到均线上方"
	}
}

func (r *RiskMonitor) unrecoveredPriceReason(currentPrice, avgPrice float64) string {
	switch r.tradingDirection() {
	case "short":
		return fmt.Sprintf("价格%.2f>均价%.2f", currentPrice, avgPrice)
	case "neutral":
		return fmt.Sprintf("价格%.2f偏离均价%.2f", currentPrice, avgPrice)
	default:
		return fmt.Sprintf("价格%.2f<均价%.2f", currentPrice, avgPrice)
	}
}

// IsTriggered 返回是否触发风控
func (r *RiskMonitor) IsTriggered() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.triggered
}

// reportLoop 定期报告状态（每60秒）
func (r *RiskMonitor) reportLoop(ctx context.Context) {
	defer recoverWorker("风控状态报告")
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runProtected("风控状态报告", func() {
				r.reportStatus(ctx)
			})
		}
	}
}

// reportStatus 报告状态
func (r *RiskMonitor) reportStatus(ctx context.Context) {
	r.mu.RLock()
	triggered := r.triggered
	r.mu.RUnlock()

	r.refreshStaleKlines(ctx, triggered)
	r.mu.RLock()
	triggered = r.triggered
	r.mu.RUnlock()
	staleData := r.hasStaleKlines(triggered)
	if staleData {
		r.mu.Lock()
		r.triggered = true
		r.lastMsg = "K线数据超过2分钟未更新，风控保持保护状态"
		triggered = true
		r.mu.Unlock()
	} else {
		r.checkMarket()
		r.mu.RLock()
		triggered = r.triggered
		r.mu.RUnlock()
	}

	if triggered {
		if staleData {
			logger.Warn("⚠️ [风控监测] K线数据过期且补数失败，保持风控保护，停止交易!")
		} else {
			logger.Warn("⚠️ [风控监测] 当前市场交易出现异动,触发主动安全风控,停止交易!")
		}
	} else {
		logger.Info("🛡️ [风控监测] 市场环境正常。")
	}

	// 打印各币种的移动平均线数值
	r.printMovingAverages(triggered)
}

// printMovingAverages 打印各币种的移动平均线数值
func (r *RiskMonitor) printMovingAverages(inRiskControl bool) {
	logger.Info("📊 [移动平均线监测] 当前各币种数据:")

	// 检查K线数据是否过期
	hasStaleData := false

	for _, symbol := range r.cfg.RiskControl.MonitorSymbols {
		r.mu.RLock()
		symbolData, exists := r.symbolDataMap[symbol]
		r.mu.RUnlock()

		if !exists {
			logger.Info("  %s: 无数据", symbol)
			continue
		}

		candles := symbolData.snapshotCandles()
		candleCount := len(candles)

		if candleCount < r.cfg.RiskControl.AverageWindow+1 {
			logger.Info("  %s: 数据不足 (当前%d根, 需要%d根)", symbol, candleCount, r.cfg.RiskControl.AverageWindow+1)
			continue
		}

		var currentCandle *exchange.Candle
		var currentPrice float64
		var currentVol float64

		// 根据是否在风控中，选择不同的K线
		if inRiskControl {
			// 风控中：使用最新的完结K线（与恢复判断逻辑一致）
			for i := candleCount - 1; i >= 0; i-- {
				if candles[i].IsClosed {
					currentCandle = candles[i]
					currentPrice = currentCandle.Close
					currentVol = currentCandle.Volume
					break
				}
			}
			if currentCandle == nil {
				logger.Info("  %s: 无完结K线", symbol)
				continue
			}
		} else {
			// 非风控状态：使用最新K线（包括未完结的）
			currentCandle = candles[candleCount-1]
			currentPrice = currentCandle.Close
			currentVol = currentCandle.Volume
		}

		// 计算移动平均价格和移动平均成交量（只使用完结的K线，排除当前用于判断的K线）
		var totalPrice float64
		var totalVol float64
		var validCount int
		window := r.cfg.RiskControl.AverageWindow

		for i := candleCount - 1; i >= 0 && validCount < window; i-- {
			if candles[i].IsClosed && candles[i] != currentCandle {
				totalPrice += candles[i].Close
				totalVol += candles[i].Volume
				validCount++
			}
		}

		if validCount < window {
			logger.Info("  %s: 完结K线不足 (当前%d根, 需要%d根)", symbol, validCount, window)
			continue
		}

		avgPrice := totalPrice / float64(validCount)
		avgVol := totalVol / float64(validCount)

		// 计算偏离度
		priceDeviation := (currentPrice - avgPrice) / avgPrice * 100
		volRatio := currentVol / avgVol

		// 判断各项指标状态
		priceRecovered := r.priceRecovered(currentPrice, avgPrice)
		volNormal := currentVol < avgVol*r.cfg.RiskControl.VolumeMultiplier

		// 根据是否在风控中，显示不同的状态信息
		klineStatus := "完结"
		if !currentCandle.IsClosed {
			klineStatus = "未完结"
		}

		// 计算K线时间距离现在的时间差（帮助调试）
		klineAge := time.Since(candleTime(currentCandle))
		klineAgeStr := fmt.Sprintf("%.0f秒前", klineAge.Seconds())
		if klineAge > time.Minute {
			klineAgeStr = fmt.Sprintf("%.0f分前", klineAge.Minutes())
		}

		var statusMsg string
		if inRiskControl {
			// 风控中，显示详细的异常/恢复状态
			if priceRecovered && volNormal {
				statusMsg = fmt.Sprintf("正常[%s|%s]: 当前价=%.4f, 均价=%.4f (偏离%.2f%%), %s, 当前量=%.0f, 均量=%.0f (倍数×%.2f) 成交量已恢复",
					klineStatus, klineAgeStr, currentPrice, avgPrice, priceDeviation, r.recoveryPriceDescription(), currentVol, avgVol, volRatio)
			} else {
				// 异常状态，说明未恢复的原因
				var priceStatus, volStatus string
				if priceRecovered {
					priceStatus = r.recoveryPriceDescription()
				} else {
					priceStatus = r.unrecoveredPriceReason(currentPrice, avgPrice)
				}
				if volNormal {
					volStatus = "成交量已恢复"
				} else {
					volStatus = "成交量未恢复"
				}
				statusMsg = fmt.Sprintf("异常[%s|%s]: 当前价=%.4f, 均价=%.4f (偏离%.2f%%), %s, 当前量=%.0f, 均量=%.0f (倍数×%.2f) %s",
					klineStatus, klineAgeStr, currentPrice, avgPrice, priceDeviation, priceStatus, currentVol, avgVol, volRatio, volStatus)
			}
		} else {
			// 非风控状态，判断异常需要同时满足方向对应的危险价格侧和成交量放大。
			isAdversePrice := r.isAdversePriceMove(currentPrice, avgPrice)
			isVolHigh := !volNormal

			if isAdversePrice && isVolHigh {
				// 同时满足两个条件才是真正的异常
				statusMsg = fmt.Sprintf("🚨异常[%s|%s]: 当前价=%.4f, 均价=%.4f (%s), 当前量=%.0f, 均量=%.0f (倍数×%.2f)",
					klineStatus, klineAgeStr, currentPrice, avgPrice, r.adversePriceDescription(priceDeviation), currentVol, avgVol, volRatio)
			} else {
				// 否则显示正常（添加K线时间信息）
				statusMsg = fmt.Sprintf("✅正常[%s|%s]: 当前价=%.4f, 均价=%.4f (偏离%.2f%%), 当前量=%.0f, 均量=%.0f (倍数×%.2f)",
					klineStatus, klineAgeStr, currentPrice, avgPrice, priceDeviation, currentVol, avgVol, volRatio)
			}
		}

		logger.Info("  %s %s", symbol, statusMsg)

		// 检查数据是否过期（超过2分钟）
		if klineAge > staleKlineThreshold {
			hasStaleData = true
		}
	}

	// 如果有过期数据，发出警告
	if hasStaleData {
		logger.Warn("⚠️ [K线数据] 部分币种的K线数据超过2分钟未更新，可能K线流断开或重连中")
	}
}

// Stop 停止监控
func (r *RiskMonitor) Stop() {
	if r.exchange != nil {
		r.exchange.StopKlineStream()
	}
}

type binancePublicKlineProvider struct {
	client *http.Client
}

func newBinancePublicKlineProvider() *binancePublicKlineProvider {
	return &binancePublicKlineProvider{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *binancePublicKlineProvider) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*exchange.Candle, error) {
	if limit <= 0 {
		limit = 100
	}
	values := url.Values{}
	values.Set("symbol", strings.ToUpper(strings.TrimSpace(symbol)))
	values.Set("interval", interval)
	values.Set("limit", strconv.Itoa(limit))

	endpoints := []string{
		"https://fapi.binance.com/fapi/v1/klines",
		"https://api.binance.com/api/v3/klines",
	}

	var lastErr error
	for _, endpoint := range endpoints {
		candles, err := p.fetch(ctx, endpoint, values)
		if err == nil && len(candles) > 0 {
			return candles, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("Binance公共K线返回空数据")
}

func (p *binancePublicKlineProvider) fetch(ctx context.Context, endpoint string, values url.Values) ([]*exchange.Candle, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s HTTP %d", endpoint, resp.StatusCode)
	}

	var raw [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析Binance公共K线失败: %w", err)
	}

	symbol := strings.ToUpper(values.Get("symbol"))
	candles := make([]*exchange.Candle, 0, len(raw))
	for _, row := range raw {
		if len(row) < 6 {
			continue
		}
		timestamp, ok := parseKlineInt(row[0])
		if !ok {
			continue
		}
		open, okOpen := parseKlineFloat(row[1])
		high, okHigh := parseKlineFloat(row[2])
		low, okLow := parseKlineFloat(row[3])
		closePrice, okClose := parseKlineFloat(row[4])
		volume, okVol := parseKlineFloat(row[5])
		if !okOpen || !okHigh || !okLow || !okClose || !okVol {
			continue
		}
		candles = append(candles, &exchange.Candle{
			Symbol:    symbol,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closePrice,
			Volume:    volume,
			Timestamp: timestamp,
			IsClosed:  true,
		})
	}
	return candles, nil
}

func parseKlineFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case string:
		n, err := strconv.ParseFloat(x, 64)
		return n, err == nil
	case float64:
		return x, true
	default:
		return 0, false
	}
}

func parseKlineInt(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}
