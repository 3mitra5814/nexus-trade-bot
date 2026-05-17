package safety

import (
	"context"
	"fmt"
	"nexus-trade-bot/exchange"
	"nexus-trade-bot/logger"
	"strings"
	"time"
)

const (
	MaxLeverage = 10 // 最大允许杠杆倍数（硬编码）
)

type positionModeChecker interface {
	ValidatePositionMode(ctx context.Context, direction string) error
}

// CheckAccountSafety 检查账户安全性（支持所有交易所）
// 参数：
//   - ex: 交易所接口
//   - symbol: 交易对
//   - currentPrice: 当前币价
//   - orderAmount: 每笔交易金额（USDT/USDC）
//   - priceInterval: 价格间隔（买入价和卖出价的差值）
//   - feeRate: 手续费率
//   - requiredPositions: 要求的最少可承受仓位数量（默认100）
//   - priceDecimals: 价格小数位数（用于格式化显示）
//   - direction: 交易方向（long / short / neutral）
func CheckAccountSafety(ex exchange.IExchange, symbol string, currentPrice, orderAmount, priceInterval, feeRate float64, requiredPositions, priceDecimals int, direction string) error {
	logger.Info("🔒 ===== 开始持仓安全性检查 =====")
	direction = strings.ToLower(direction)
	if direction == "" {
		direction = "long"
	}

	// 从交易所接口获取计价币种（支持U本位和币本位合约）
	quoteCurrency := ex.GetQuoteAsset()

	// 1. 获取账户信息
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	account, err := ex.GetAccount(ctx)
	if err != nil {
		return fmt.Errorf("获取账户信息失败: %w", err)
	}

	if checker, ok := ex.(positionModeChecker); ok {
		if err := checker.ValidatePositionMode(ctx, direction); err != nil {
			return err
		}
	}

	// 2. 获取交易对的杠杆倍数和持仓信息
	var leverage int = 1 // 默认1倍杠杆
	var positionAmt float64 = 0
	var existingPositionSlots float64

	// 尝试获取持仓信息
	positions, err := ex.GetPositions(ctx, symbol)
	if err == nil && positions != nil {
		for _, p := range positions {
			if p.Symbol == symbol {
				positionAmt += p.Size
				if currentPrice > 0 && orderAmount > 0 && p.Size != 0 {
					existingPositionSlots += absFloat(p.Size) * currentPrice / orderAmount
				}
				if p.Leverage > 0 {
					if p.Leverage > leverage {
						leverage = p.Leverage
					}
				}
			}
		}
	}

	// 如果持仓中没有找到杠杆倍数，尝试从账户信息中获取
	if leverage == 1 && account.AccountLeverage > 0 {
		leverage = account.AccountLeverage
		logger.Info("ℹ️ 从账户信息中获取杠杆倍数: %dx", leverage)
	}

	// 当前已有持仓时仍继续执行杠杆、余额和手续费检查。实盘里已有仓位不是绕过风控的理由。
	if positionAmt != 0 {
		logger.Warn("⚠️ 检测到当前已有持仓: %.4f，将继续执行启动安全检查", positionAmt)
	}
	accountBalance := account.AvailableBalance
	if accountBalance <= 0 {
		return fmt.Errorf("账户余额不足，当前余额: %.2f %s", accountBalance, quoteCurrency)
	}
	logger.Info("💰 账户余额: %.2f %s (交易对: %s)", accountBalance, quoteCurrency, symbol)
	// 如果是币安交易所，尝试获取更准确的杠杆信息
	exchangeName := ex.GetName()
	if leverage == 1 && exchangeName == "Binance" {
		// 尝试通过币安特定的方法获取杠杆（如果获取失败也没关系，使用默认值）
		if binanceLeverage := tryGetBinanceLeverage(ex, symbol); binanceLeverage > 0 {
			leverage = binanceLeverage
		}
	}

	logger.Info("📊 交易所: %s, 交易对: %s, 当前杠杆倍数: %dx, 当前持仓: %.4f", exchangeName, symbol, leverage, positionAmt)

	// 3. 强制杠杆倍数检查（硬编码最多10倍）
	if leverage > MaxLeverage {
		return fmt.Errorf("您的账户杠杆倍率太高（%dx），风险太大，禁止开仓。最大允许杠杆倍数: %dx", leverage, MaxLeverage)
	}

	// 4. 计算最大可持有仓位
	// 固定金额模式下，orderAmount 是每笔交易的名义金额（USDT/USDC）。
	// 这里按可用余额估算新增承载能力，并对已有仓位/中性双向网格做保守折算。
	maxAvailableMargin := accountBalance * float64(leverage)
	costPerPosition := orderAmount // 每仓成本就是配置的金额
	maxPositions := maxAvailableMargin / costPerPosition
	requiredPositionBudget := float64(requiredPositions)
	if direction == "neutral" {
		requiredPositionBudget *= 2
	}
	requiredPositionBudget += existingPositionSlots

	// 如果未设置小数位数，使用默认值2
	if priceDecimals <= 0 {
		priceDecimals = 2
	}

	// 根据当前价格计算实际购买数量（用于显示）
	orderQuantity := orderAmount / currentPrice

	logger.Info("📈 当前币价: %.*f, 每笔金额: %.2f %s, 每笔数量: %.4f", priceDecimals, currentPrice, orderAmount, quoteCurrency, orderQuantity)
	logger.Info("💵 最大可用保证金: %.2f %s (余额 %.2f × 杠杆 %dx)", maxAvailableMargin, quoteCurrency, accountBalance, leverage)
	logger.Info("📦 每仓成本: %.2f %s (固定金额模式)", costPerPosition, quoteCurrency)
	logger.Info("🎯 最大可持有仓位: %.0f 仓", maxPositions)
	if direction == "neutral" {
		logger.Info("✅ 要求最少持有: %d 仓 × 双向预算 = %.0f 仓", requiredPositions, float64(requiredPositions)*2)
	} else {
		logger.Info("✅ 要求最少持有: %d 仓", requiredPositions)
	}
	if existingPositionSlots > 0 {
		logger.Info("📌 已有持仓折算占用: %.1f 仓", existingPositionSlots)
	}

	// 5. 验证是否满足要求
	if maxPositions < requiredPositionBudget {
		return fmt.Errorf("持仓安全检查失败：您的账户余额不足，请补充足够保证金或调整配置参数，至少需要支持 %.0f 仓（含已有持仓/双向预算）。当前最大可持有: %.0f 仓", requiredPositionBudget, maxPositions)
	}

	logger.Info("✅ 持仓安全性检查通过：可以安全支撑至少 %.0f 仓预算", requiredPositionBudget)

	// 6. 手续费率安全检查
	buyFeeRate := feeRate
	sellFeeRate := feeRate

	logger.Info("💳 手续费率检查: 交易对=%s, 买入费率=%.4f%%, 卖出费率=%.4f%%",
		symbol, buyFeeRate*100, sellFeeRate*100)

	// 计算每笔交易的利润和手续费
	entryPrice := currentPrice
	exitGap := priceInterval * 2
	exitPrice := currentPrice + exitGap
	actionDesc := "买入后卖出"
	entrySide := "买入"
	exitSide := "卖出"
	if direction == "short" {
		exitPrice = currentPrice - exitGap
		actionDesc = "卖出后买回"
		entrySide = "卖出"
		exitSide = "买回"
	} else if direction == "neutral" {
		actionDesc = "中性双向（按单边 long 模型校验）"
	}

	entryQuantity := orderAmount / entryPrice
	exitQuantity := entryQuantity
	entryAmount := orderAmount
	exitAmount := exitPrice * exitQuantity
	profitPerTrade := exitGap * entryQuantity

	// 手续费 = 买入手续费 + 卖出手续费
	buyFee := entryAmount * buyFeeRate
	sellFee := exitAmount * sellFeeRate
	totalFee := buyFee + sellFee

	// 计算总手续费率（买入费率 + 卖出费率）
	totalFeeRate := buyFeeRate + sellFeeRate

	// 计算利润占入场价的比例（利润率）
	profitRate := exitGap / entryPrice

	logger.Info("💰 每笔交易分析 (固定金额模式, %s):", actionDesc)
	logger.Info("   %s价: %.*f, %s价: %.*f, 价格差: %.*f", entrySide, priceDecimals, entryPrice, exitSide, priceDecimals, exitPrice, priceDecimals, exitGap)
	logger.Info("   %s金额: %.2f %s, %s数量: %.4f", entrySide, entryAmount, quoteCurrency, entrySide, entryQuantity)
	logger.Info("   %s金额: %.2f %s, %s数量: %.4f", exitSide, exitAmount, quoteCurrency, exitSide, exitQuantity)
	logger.Info("   每笔利润: %.4f %s", profitPerTrade, quoteCurrency)
	logger.Info("   利润率: %.4f%% (价格差 %.*f / 入场价 %.*f)", profitRate*100, priceDecimals, exitGap, priceDecimals, entryPrice)
	logger.Info("   入场手续费: %.4f %s (金额 %.2f × 费率 %.4f%%)", buyFee, quoteCurrency, entryAmount, buyFeeRate*100)
	logger.Info("   出场手续费: %.4f %s (金额 %.2f × 费率 %.4f%%)", sellFee, quoteCurrency, exitAmount, sellFeeRate*100)
	logger.Info("   总手续费: %.4f %s (费率: %.4f%%)", totalFee, quoteCurrency, totalFeeRate*100)

	netProfit := profitPerTrade - totalFee
	logger.Info("   净利润: %.4f %s (利润 %.4f - 手续费 %.4f)", netProfit, quoteCurrency, profitPerTrade, totalFee)

	// 验证利润是否足够支付手续费（净利润必须为正）
	if netProfit <= 0 {
		logger.Error("❌ 错误：每笔净利润为负或为零 (%.4f %s)，无法盈利！", netProfit, quoteCurrency)
		logger.Error("   建议：增加价格间隔或降低手续费率")
		logger.Error("   当前价格间隔: %.*f, 手续费率: %.4f%%", priceDecimals, priceInterval, totalFeeRate*100)
		return fmt.Errorf("每笔净利润为负或为零 (%.4f %s)，系统拒绝启动", netProfit, quoteCurrency)
	}

	logger.Info("✅ 手续费率安全检查通过：每笔净利润 %.4f %s", netProfit, quoteCurrency)

	logger.Info("🔒 ===== 持仓安全性检查完成 =====")

	return nil
}

// tryGetBinanceLeverage 尝试获取币安的杠杆信息（可选功能，失败不影响主流程）
func tryGetBinanceLeverage(ex exchange.IExchange, symbol string) int {
	// 由于币安适配器可能有特定的方法，这里我们通过反射或类型断言来获取
	// 如果失败，返回0表示无法获取

	// 这里可以根据实际情况实现，暂时返回0让其使用默认值
	// 后续可以扩展：通过反射或扩展接口来获取特定交易所的杠杆信息

	return 0 // 表示无法获取，使用默认值
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
