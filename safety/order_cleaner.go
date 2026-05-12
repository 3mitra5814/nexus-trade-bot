package safety

import (
	"context"
	"nexus-trade-bot/config"
	"nexus-trade-bot/logger"
	"reflect"
	"sort"
	"time"
)

// OrderCleanerSlotInfo 订单清理所需的槽位信息
type OrderCleanerSlotInfo struct {
	Price       float64
	OrderID     int64
	OrderSide   string
	OrderStatus string
}

// IOrderExecutor 订单执行器接口（用于批量撤单）
type IOrderExecutor interface {
	BatchCancelOrders(orderIDs []int64) error
}

type openOrdersChecker interface {
	GetOpenOrders() ([]interface{}, error)
}

// IOrderCleanerPositionManager 订单清理所需的仓位管理器接口
type IOrderCleanerPositionManager interface {
	// 遍历所有槽位
	IterateSlots(fn func(price float64, slot interface{}) bool)
	// 更新槽位状态
	UpdateSlotOrderStatus(price float64, bookSide, status string)
	UpdateSlotOrderStatusIfCurrent(price float64, bookSide, status string, orderID int64, clientOID string)
}

// OrderCleaner 订单清理器
type OrderCleaner struct {
	cfg      *config.Config
	executor IOrderExecutor
	pm       IOrderCleanerPositionManager
}

// NewOrderCleaner 创建订单清理器
func NewOrderCleaner(cfg *config.Config, executor IOrderExecutor, pm IOrderCleanerPositionManager) *OrderCleaner {
	return &OrderCleaner{
		cfg:      cfg,
		executor: executor,
		pm:       pm,
	}
}

// Start 启动订单清理协程
func (oc *OrderCleaner) Start(ctx context.Context) {
	go func() {
		defer recoverWorker("订单清理")
		cleanupInterval := time.Duration(oc.cfg.Timing.OrderCleanupInterval) * time.Second
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.Info("⏹️ 订单清理协程已停止")
				return
			case <-ticker.C:
				runProtected("订单清理", oc.CleanupOrders)
			}
		}
	}()
	logger.Info("✅ 订单清理协程已启动")
}

// CleanupOrders 清理订单
func (oc *OrderCleaner) CleanupOrders() {
	// 订单状态常量
	const (
		OrderStatusPlaced          = "PLACED"
		OrderStatusConfirmed       = "CONFIRMED"
		OrderStatusPartiallyFilled = "PARTIALLY_FILLED"
		OrderStatusCancelRequested = "CANCEL_REQUESTED"
	)

	// 统计当前订单数
	totalOrders := 0
	var longEntryOrders []struct {
		Price     float64
		BookSide  string
		OrderID   int64
		ClientOID string
	}
	var shortEntryOrders []struct {
		Price     float64
		BookSide  string
		OrderID   int64
		ClientOID string
	}

	oc.pm.IterateSlots(func(price float64, slotRaw interface{}) bool {
		// 使用反射提取槽位字段
		v := reflect.ValueOf(slotRaw)
		if v.Kind() != reflect.Struct {
			return true
		}

		// 提取字段
		getStringField := func(name string) string {
			field := v.FieldByName(name)
			if field.IsValid() && field.Kind() == reflect.String {
				return field.String()
			}
			return ""
		}

		getInt64Field := func(name string) int64 {
			field := v.FieldByName(name)
			if field.IsValid() && field.CanInt() {
				return field.Int()
			}
			return 0
		}

		orderID := getInt64Field("OrderID")
		clientOID := getStringField("ClientOID")
		orderSide := getStringField("OrderSide")
		orderStatus := getStringField("OrderStatus")
		bookSide := getStringField("BookSide")
		if bookSide == "" {
			bookSide = "LONG"
		}

		isActive := orderStatus == OrderStatusPlaced || orderStatus == OrderStatusConfirmed || orderStatus == OrderStatusPartiallyFilled
		if isActive {
			totalOrders++
		}
		// 部分成交订单和所有平仓单都计入阈值，但不主动撤销，避免有仓位时失去保护。
		if orderStatus == OrderStatusPlaced || orderStatus == OrderStatusConfirmed {
			isEntry := (bookSide == "SHORT" && orderSide == "SELL") || (bookSide != "SHORT" && orderSide == "BUY")
			if isEntry && bookSide == "SHORT" {
				shortEntryOrders = append(shortEntryOrders, struct {
					Price     float64
					BookSide  string
					OrderID   int64
					ClientOID string
				}{Price: price, BookSide: bookSide, OrderID: orderID, ClientOID: clientOID})
			} else if isEntry {
				longEntryOrders = append(longEntryOrders, struct {
					Price     float64
					BookSide  string
					OrderID   int64
					ClientOID string
				}{Price: price, BookSide: bookSide, OrderID: orderID, ClientOID: clientOID})
			}
		}
		return true
	})

	threshold := oc.cfg.Trading.OrderCleanupThreshold
	if threshold <= 0 {
		threshold = 100
	}

	batchSize := oc.cfg.Trading.CleanupBatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	// 🔥 核心策略：超过阈值才清理，不提前
	// 清理时优先清理数量多的一方（买单或卖单）
	if totalOrders > threshold {
		canceledCount := 0
		cleanupBudget := totalOrders - threshold
		if cleanupBudget > batchSize {
			cleanupBudget = batchSize
		}

		logger.Info("🧹 [订单清理] 当前订单数: %d (多头开仓单: %d, 空头开仓单: %d), 阈值: %d, 批次大小: %d",
			totalOrders, len(longEntryOrders), len(shortEntryOrders), threshold, batchSize)

		longOrdersToCancel := 0
		shortOrdersToCancel := 0

		if len(longEntryOrders) > len(shortEntryOrders) {
			longOrdersToCancel = cleanupBudget
			logger.Info("📊 [清理策略] 多头开仓单较多，清理 %d 个多头开仓单", longOrdersToCancel)
		} else if len(shortEntryOrders) > len(longEntryOrders) {
			shortOrdersToCancel = cleanupBudget
			logger.Info("📊 [清理策略] 空头开仓单较多，清理 %d 个空头开仓单", shortOrdersToCancel)
		} else {
			longOrdersToCancel = cleanupBudget / 2
			shortOrdersToCancel = cleanupBudget - longOrdersToCancel
			logger.Info("📊 [清理策略] 多空开仓单数量相等，平均清理 (多头: %d, 空头: %d)", longOrdersToCancel, shortOrdersToCancel)
		}

		// 清理多头开仓单：清理价格最低的（离当前价格最远）
		if len(longEntryOrders) > 0 && longOrdersToCancel > 0 {
			sort.Slice(longEntryOrders, func(i, j int) bool {
				return longEntryOrders[i].Price < longEntryOrders[j].Price
			})

			cancelCount := longOrdersToCancel
			if cancelCount > len(longEntryOrders) {
				cancelCount = len(longEntryOrders)
			}

			if cancelCount > 0 {
				orderIDs := make([]int64, 0, cancelCount)
				targets := make([]struct {
					price     float64
					bookSide  string
					orderID   int64
					clientOID string
				}, 0, cancelCount)
				for i := 0; i < cancelCount; i++ {
					orderIDs = append(orderIDs, longEntryOrders[i].OrderID)
					targets = append(targets, struct {
						price     float64
						bookSide  string
						orderID   int64
						clientOID string
					}{price: longEntryOrders[i].Price, bookSide: longEntryOrders[i].BookSide, orderID: longEntryOrders[i].OrderID, clientOID: longEntryOrders[i].ClientOID})
				}

				logger.Info("🧹 [订单清理-多头开仓] 多头开仓单数: %d, 取消价格最低的 %d 个 (%.2f ~ %.2f)",
					len(longEntryOrders), cancelCount, longEntryOrders[0].Price, longEntryOrders[cancelCount-1].Price)

				if err := oc.executor.BatchCancelOrders(orderIDs); err != nil {
					logger.Error("❌ [订单清理-多头开仓] 批量撤单失败: %v", err)
				} else {
					remainingOpen, verified := oc.remainingOpenOrderIDs()
					// 更新槽位状态为已申请撤单
					for i, target := range targets {
						if !verified {
							oc.pm.UpdateSlotOrderStatusIfCurrent(target.price, target.bookSide, OrderStatusCancelRequested, target.orderID, target.clientOID)
						} else if _, stillOpen := remainingOpen[orderIDs[i]]; stillOpen {
							oc.pm.UpdateSlotOrderStatusIfCurrent(target.price, target.bookSide, OrderStatusCancelRequested, target.orderID, target.clientOID)
						} else {
							oc.pm.UpdateSlotOrderStatusIfCurrent(target.price, target.bookSide, "CANCELED", target.orderID, target.clientOID)
						}
					}
					canceledCount += cancelCount
				}
			}
		}

		// 清理空头开仓单：清理价格最高的（离当前价格最远）
		if len(shortEntryOrders) > 0 && shortOrdersToCancel > 0 {
			sort.Slice(shortEntryOrders, func(i, j int) bool {
				return shortEntryOrders[i].Price > shortEntryOrders[j].Price
			})

			cancelCount := shortOrdersToCancel
			if cancelCount > len(shortEntryOrders) {
				cancelCount = len(shortEntryOrders)
			}

			if cancelCount > 0 {
				orderIDs := make([]int64, 0, cancelCount)
				targets := make([]struct {
					price     float64
					bookSide  string
					orderID   int64
					clientOID string
				}, 0, cancelCount)
				for i := 0; i < cancelCount; i++ {
					orderIDs = append(orderIDs, shortEntryOrders[i].OrderID)
					targets = append(targets, struct {
						price     float64
						bookSide  string
						orderID   int64
						clientOID string
					}{price: shortEntryOrders[i].Price, bookSide: shortEntryOrders[i].BookSide, orderID: shortEntryOrders[i].OrderID, clientOID: shortEntryOrders[i].ClientOID})
				}

				logger.Info("🧹 [订单清理-空头开仓] 空头开仓单数: %d, 取消价格最高的 %d 个 (%.2f ~ %.2f)",
					len(shortEntryOrders), cancelCount, shortEntryOrders[0].Price, shortEntryOrders[cancelCount-1].Price)

				if err := oc.executor.BatchCancelOrders(orderIDs); err != nil {
					logger.Error("❌ [订单清理-空头开仓] 批量撤单失败: %v", err)
				} else {
					remainingOpen, verified := oc.remainingOpenOrderIDs()
					// 更新槽位状态为已申请撤单
					for i, target := range targets {
						if !verified {
							oc.pm.UpdateSlotOrderStatusIfCurrent(target.price, target.bookSide, OrderStatusCancelRequested, target.orderID, target.clientOID)
						} else if _, stillOpen := remainingOpen[orderIDs[i]]; stillOpen {
							oc.pm.UpdateSlotOrderStatusIfCurrent(target.price, target.bookSide, OrderStatusCancelRequested, target.orderID, target.clientOID)
						} else {
							oc.pm.UpdateSlotOrderStatusIfCurrent(target.price, target.bookSide, "CANCELED", target.orderID, target.clientOID)
						}
					}
					canceledCount += cancelCount
				}
			}
		}

		logger.Info("✅ [订单清理完成] 清理了 %d 个订单，剩余: %d", canceledCount, totalOrders-canceledCount)
	} else {
		logger.Debug("ℹ️ [订单清理] 总订单数: %d (阈值: %d，无需清理)", totalOrders, threshold)
	}
}

func (oc *OrderCleaner) remainingOpenOrderIDs() (map[int64]struct{}, bool) {
	result := make(map[int64]struct{})
	checker, ok := oc.executor.(openOrdersChecker)
	if !ok {
		return result, false
	}
	time.Sleep(500 * time.Millisecond)
	orders, err := checker.GetOpenOrders()
	if err != nil {
		logger.Warn("⚠️ [订单清理] 撤单后复核挂单失败: %v", err)
		return result, false
	}
	for _, order := range orders {
		v := reflect.ValueOf(order)
		valid := true
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				valid = false
				break
			}
			v = v.Elem()
		}
		if !valid {
			continue
		}
		if v.Kind() != reflect.Struct {
			continue
		}
		field := v.FieldByName("OrderID")
		if field.IsValid() && field.CanInt() {
			result[field.Int()] = struct{}{}
		}
	}
	return result, true
}
