package safety

import (
	"context"
	"fmt"
	"nexus-trade-bot/config"
	"nexus-trade-bot/logger"
	"reflect"
	"strings"
	"time"
)

// IExchange 定义对账所需的交易所接口方法
type IExchange interface {
	GetPositions(ctx context.Context, symbol string) (interface{}, error)
	GetOpenOrders(ctx context.Context, symbol string) (interface{}, error)
	GetBaseAsset() string // 获取基础资产（交易币种）
}

// SlotInfo 槽位信息（避免直接依赖 position 包的内部结构）
type SlotInfo struct {
	Price          float64
	PositionStatus string
	PositionQty    float64
	BookSide       string
	OrderID        int64
	OrderSide      string
	OrderStatus    string
	OrderCreatedAt time.Time
}

// IPositionManager 定义对账所需的仓位管理器接口方法
type IPositionManager interface {
	// 遍历所有槽位（封装 sync.Map.Range）
	// 注意：slot 为 interface{} 类型，需要转换为 SlotInfo
	IterateSlots(fn func(price float64, slot interface{}) bool)
	// 获取统计数据
	GetTotalBuyQty() float64
	GetTotalSellQty() float64
	GetReconcileCount() int64
	// 更新统计数据
	IncrementReconcileCount()
	UpdateLastReconcileTime(t time.Time)
	// 获取配置信息
	GetSymbol() string
	GetPriceInterval() float64
}

type snapshotApplier interface {
	ApplyExchangeSnapshot(positionsRaw interface{}, openOrdersRaw interface{})
}

type realizedPNLProvider interface {
	GetRealizedPNL() float64
}

type unrealizedPNLProvider interface {
	EstimateUnrealizedPNL(markPrice float64) float64
}

type markPriceProvider func() float64

// Reconciler 持仓对账器
type Reconciler struct {
	cfg               *config.Config
	exchange          IExchange
	pm                IPositionManager
	pauseChecker      func() bool
	markPriceProvider markPriceProvider
}

// NewReconciler 创建对账器
func NewReconciler(cfg *config.Config, exchange IExchange, pm IPositionManager) *Reconciler {
	return &Reconciler{
		cfg:      cfg,
		exchange: exchange,
		pm:       pm,
	}
}

// SetPauseChecker 设置暂停检查函数（用于风控暂停）
func (r *Reconciler) SetPauseChecker(checker func() bool) {
	r.pauseChecker = checker
}

func (r *Reconciler) SetMarkPriceProvider(provider func() float64) {
	r.markPriceProvider = provider
}

// Start 启动对账协程
func (r *Reconciler) Start(ctx context.Context) {
	go func() {
		defer recoverWorker("持仓对账")
		interval := time.Duration(r.cfg.Trading.ReconcileInterval) * time.Second
		if interval <= 0 {
			interval = 30 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		runProtected("首次持仓对账", func() {
			if err := r.Reconcile(); err != nil {
				logger.Error("❌ [首次对账失败] %v", err)
			}
		})

		for {
			select {
			case <-ctx.Done():
				logger.Info("⏹️ 持仓对账协程已停止")
				return
			case <-ticker.C:
				runProtected("持仓对账", func() {
					if err := r.Reconcile(); err != nil {
						logger.Error("❌ [对账失败] %v", err)
					}
				})
			}
		}
	}()
	logger.Info("✅ 持仓对账已启动 (间隔: %d秒)", r.cfg.Trading.ReconcileInterval)
}

// Reconcile 执行对账（通用实现，支持所有交易所）
func (r *Reconciler) Reconcile() error {
	// 检查是否暂停（风控触发时不输出日志）
	if r.pauseChecker != nil && r.pauseChecker() {
		return nil
	}

	logger.Debugln("🔍 ===== 开始持仓对账 =====")

	symbol := r.pm.GetSymbol()
	apiCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// 1. 查询交易所持仓信息（使用通用接口）
	positionsRaw, err := r.exchange.GetPositions(apiCtx, symbol)
	if err != nil {
		return fmt.Errorf("查询持仓失败: %w", err)
	}

	// 2. 查询所有挂单（使用通用接口）
	openOrdersRaw, err := r.exchange.GetOpenOrders(apiCtx, symbol)
	if err != nil {
		return fmt.Errorf("查询挂单失败: %w", err)
	}

	// 3. 解析持仓和挂单信息（通用处理）
	logger.Debug("📊 交易所持仓信息类型: %T", positionsRaw)
	logger.Debug("📊 交易所挂单信息类型: %T", openOrdersRaw)
	if applier, ok := r.pm.(snapshotApplier); ok {
		applier.ApplyExchangeSnapshot(positionsRaw, openOrdersRaw)
	}

	// 4. 计算本地持仓统计
	var localTotal float64
	var localPendingExitQty float64
	var localFilledPosition float64
	var activeBuyOrders int
	var activeSellOrders int
	var activeEntryOrders int
	var activeExitOrders int

	// 订单状态常量（与 position 包保持一致）
	const (
		OrderStatusPlaced          = "PLACED"
		OrderStatusConfirmed       = "CONFIRMED"
		OrderStatusPartiallyFilled = "PARTIALLY_FILLED"
		OrderStatusCancelRequested = "CANCEL_REQUESTED"
		PositionStatusFilled       = "FILLED"
	)

	r.pm.IterateSlots(func(price float64, slotRaw interface{}) bool {
		// 使用反射提取槽位字段
		v := reflect.ValueOf(slotRaw)
		if v.Kind() != reflect.Struct {
			return true
		}

		// 提取字段的辅助函数
		getStringField := func(name string) string {
			field := v.FieldByName(name)
			if field.IsValid() && field.Kind() == reflect.String {
				return field.String()
			}
			return ""
		}

		getFloat64Field := func(name string) float64 {
			field := v.FieldByName(name)
			if field.IsValid() && field.CanFloat() {
				return field.Float()
			}
			return 0.0
		}

		getInt64Field := func(name string) int64 {
			field := v.FieldByName(name)
			if field.IsValid() && field.CanInt() {
				return field.Int()
			}
			return 0
		}

		positionStatus := getStringField("PositionStatus")
		positionQty := getFloat64Field("PositionQty")
		bookSide := getStringField("BookSide")
		orderSide := getStringField("OrderSide")
		orderStatus := getStringField("OrderStatus")
		orderID := getInt64Field("OrderID")
		clientOID := getStringField("ClientOID")
		slotStatus := getStringField("SlotStatus")
		hasActiveOrder := (orderID != 0 || clientOID != "") &&
			(orderStatus == OrderStatusPlaced || orderStatus == OrderStatusConfirmed ||
				orderStatus == OrderStatusPartiallyFilled || orderStatus == OrderStatusCancelRequested)
		if slotStatus == "PENDING" {
			hasActiveOrder = clientOID != ""
		}

		if positionStatus == PositionStatusFilled {
			localFilledPosition += positionQty
			isExitOrder := (bookSide == "SHORT" && orderSide == "BUY") || (bookSide != "SHORT" && orderSide == "SELL")
			if isExitOrder && hasActiveOrder {
				localPendingExitQty += positionQty
				activeExitOrders++
			}
		}

		isEntryOrder := (bookSide == "SHORT" && orderSide == "SELL") || (bookSide != "SHORT" && orderSide == "BUY")
		if isEntryOrder && hasActiveOrder {
			activeEntryOrders++
		}
		if hasActiveOrder {
			switch orderSide {
			case "BUY":
				activeBuyOrders++
			case "SELL":
				activeSellOrders++
			}
		}

		return true
	})

	localTotal = localFilledPosition

	logger.Debug("📊 [对账统计] 本地持仓: %.4f, 活跃挂单: BUY=%d/SELL=%d, 开仓=%d, 平仓=%d (%.4f)",
		localTotal, activeBuyOrders, activeSellOrders, activeEntryOrders, activeExitOrders, localPendingExitQty)

	r.pm.IncrementReconcileCount()

	// 5. 输出对账统计（从交易所接口获取基础币种，支持U本位和币本位合约）
	baseCurrency := r.exchange.GetBaseAsset()
	logger.Info("✅ [对账完成] 本地持仓: %.4f %s, 活跃挂单: BUY=%d/SELL=%d, 开仓=%d, 平仓=%d (%.4f %s)",
		localTotal, baseCurrency, activeBuyOrders, activeSellOrders, activeEntryOrders, activeExitOrders, localPendingExitQty, baseCurrency)

	r.pm.UpdateLastReconcileTime(time.Now())

	totalBuyQty := r.pm.GetTotalBuyQty()
	totalSellQty := r.pm.GetTotalSellQty()
	realizedPNL := 0.0
	if provider, ok := r.pm.(realizedPNLProvider); ok {
		realizedPNL = provider.GetRealizedPNL()
	}
	unrealizedPNL := r.reconciledUnrealizedPNL(positionsRaw)
	logger.Info("📊 [统计] 对账次数: %d, 累计买入: %.4f, 累计卖出: %.4f, 已实现盈亏: %.4f USD, 未实现盈亏: %.4f USD",
		r.pm.GetReconcileCount(), totalBuyQty, totalSellQty, realizedPNL, unrealizedPNL)
	logger.Debugln("🔍 ===== 对账完成 =====")
	return nil
}

func (r *Reconciler) reconciledUnrealizedPNL(positionsRaw interface{}) float64 {
	if total, ok := sumExchangeUnrealizedPNL(positionsRaw); ok {
		return total
	}
	markPrice := 0.0
	if r.markPriceProvider != nil {
		markPrice = r.markPriceProvider()
	}
	markPrice = firstPositive(markPrice, firstPositivePositionField(positionsRaw, "MarkPrice"))
	if provider, ok := r.pm.(unrealizedPNLProvider); ok && markPrice > 0 {
		return provider.EstimateUnrealizedPNL(markPrice)
	}
	return sumPositionUnrealizedPNL(positionsRaw)
}

func sumExchangeUnrealizedPNL(raw interface{}) (float64, bool) {
	return sumPositionFieldWhenFlagged(raw, "UnrealizedPNL", "HasUnrealizedPNL")
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func sumPositionUnrealizedPNL(raw interface{}) float64 {
	return sumPositionField(raw, "UnrealizedPNL")
}

func firstPositivePositionField(raw interface{}, fieldName string) float64 {
	v := reflect.ValueOf(raw)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return 0
	}
	for i := 0; i < v.Len(); i++ {
		item := v.Index(i)
		for item.Kind() == reflect.Pointer || item.Kind() == reflect.Interface {
			if item.IsNil() {
				item = reflect.Value{}
				break
			}
			item = item.Elem()
		}
		if !item.IsValid() || item.Kind() != reflect.Struct {
			continue
		}
		field := item.FieldByName(fieldName)
		if field.IsValid() && field.CanFloat() && field.Float() > 0 {
			return field.Float()
		}
	}
	return 0
}

func sumPositionField(raw interface{}, fieldName string) float64 {
	total, _ := sumPositionFieldWhenFlagged(raw, fieldName, "")
	return total
}

func sumPositionFieldWhenFlagged(raw interface{}, fieldName, flagName string) (float64, bool) {
	v := reflect.ValueOf(raw)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return 0, false
	}
	total := 0.0
	found := false
	for i := 0; i < v.Len(); i++ {
		item := v.Index(i)
		for item.Kind() == reflect.Pointer || item.Kind() == reflect.Interface {
			if item.IsNil() {
				item = reflect.Value{}
				break
			}
			item = item.Elem()
		}
		if !item.IsValid() || item.Kind() != reflect.Struct {
			continue
		}
		if strings.TrimSpace(flagName) != "" {
			flag := item.FieldByName(flagName)
			if !flag.IsValid() || flag.Kind() != reflect.Bool || !flag.Bool() {
				continue
			}
		}
		field := item.FieldByName(fieldName)
		if field.IsValid() && field.CanFloat() {
			total += field.Float()
			found = true
		}
	}
	return total, found
}
