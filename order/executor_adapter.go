package order

import (
	"context"
	"fmt"
	"nexus-trade-bot/exchange"
	"nexus-trade-bot/exchange/ratelimit"
	"nexus-trade-bot/logger"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// OrderRequest 订单请求
type OrderRequest struct {
	Symbol        string
	Side          string
	Price         float64
	Quantity      float64
	PriceDecimals int    // 价格小数位数（用于格式化价格字符串）
	ReduceOnly    bool   // 是否只减仓（平仓单）
	PostOnly      bool   // 是否只做 Maker（Post Only）
	ClientOrderID string // 自定义订单ID
}

// Order 订单信息
type Order struct {
	OrderID       int64
	ClientOrderID string
	Symbol        string
	Side          string
	Price         float64
	Quantity      float64
	Status        string
	CreatedAt     time.Time
}

// ExchangeOrderExecutor 基于 exchange.IExchange 的订单执行器
type ExchangeOrderExecutor struct {
	exchange    exchange.IExchange
	symbol      string
	rateLimiter *rate.Limiter

	// 时间配置
	rateLimitRetryDelay time.Duration
	orderRetryDelay     time.Duration
}

const exchangeAPITimeout = 8 * time.Second

func apiContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), exchangeAPITimeout)
}

// NewExchangeOrderExecutor 创建基于交易所接口的订单执行器
func NewExchangeOrderExecutor(ex exchange.IExchange, symbol string, rateLimitRetryDelay, orderRetryDelay int) *ExchangeOrderExecutor {
	return &ExchangeOrderExecutor{
		exchange:            ex,
		symbol:              symbol,
		rateLimiter:         rate.NewLimiter(rate.Limit(orderLocalRateLimit()), orderLocalBurst()),
		rateLimitRetryDelay: time.Duration(rateLimitRetryDelay) * time.Second,
		orderRetryDelay:     time.Duration(orderRetryDelay) * time.Millisecond,
	}
}

// isPostOnlyError 检查是否为PostOnly错误
func isPostOnlyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Binance: code=-5022, Bitget: Post Only order will be rejected, Gate.io: ORDER_POC_IMMEDIATE
	return strings.Contains(errStr, "-5022") ||
		strings.Contains(errStr, "Post Only") ||
		strings.Contains(strings.ToLower(errStr), "post only") ||
		strings.Contains(errStr, "post_only") ||
		strings.Contains(errStr, "would immediately match") ||
		strings.Contains(errStr, "ORDER_POC_IMMEDIATE")
}

// PlaceOrder 下单（带重试）
func (oe *ExchangeOrderExecutor) PlaceOrder(req *OrderRequest) (*Order, error) {
	// 限流
	waitCtx, waitCancel := apiContext()
	if err := oe.rateLimiter.Wait(waitCtx); err != nil {
		waitCancel()
		return nil, fmt.Errorf("速率限制等待失败: %v", err)
	}
	if err := ratelimit.Wait(waitCtx, exchangeTradeRateLimitProfile(oe.exchange.GetName())); err != nil {
		waitCancel()
		return nil, fmt.Errorf("交易所全局限速等待失败: %v", err)
	}
	waitCancel()

	maxRetries := 3
	var lastErr error
	postOnlyFailCount := 0

	for i := 0; i <= maxRetries; i++ {
		// 转换为通用订单请求
		exchangeReq := &exchange.OrderRequest{
			Symbol:        req.Symbol,
			Side:          exchange.Side(req.Side),
			Type:          exchange.OrderTypeLimit,
			TimeInForce:   exchange.TimeInForceGTC,
			Quantity:      req.Quantity,
			Price:         req.Price,
			PriceDecimals: req.PriceDecimals,
			ReduceOnly:    req.ReduceOnly,
			PostOnly:      req.PostOnly,
			ClientOrderID: req.ClientOrderID, // 传递自定义订单ID
		}

		// 调用交易所接口
		ctx, cancel := apiContext()
		exchangeOrder, err := oe.exchange.PlaceOrder(ctx, exchangeReq)
		cancel()
		if err == nil && exchangeOrder == nil {
			err = fmt.Errorf("交易所返回空订单")
		}
		if err == nil {
			clientOrderID := strings.TrimSpace(exchangeOrder.ClientOrderID)
			if clientOrderID == "" {
				clientOrderID = req.ClientOrderID
			}
			// 转换回 Order 格式
			order := &Order{
				OrderID:       exchangeOrder.OrderID,
				ClientOrderID: clientOrderID,
				Symbol:        req.Symbol,
				Side:          req.Side,
				Price:         req.Price,
				Quantity:      req.Quantity,
				Status:        string(exchangeOrder.Status),
				CreatedAt:     time.Now(),
			}

			// 根据实际使用的订单类型显示日志
			orderTypeDesc := "PostOnly"
			if !exchangeReq.PostOnly {
				orderTypeDesc = "普通限价单"
			}
			logger.Info("✅ [%s] 下单成功(%s): %s %.*f 数量: %.4f 订单ID: %d",
				oe.exchange.GetName(), orderTypeDesc, req.Side, req.PriceDecimals, req.Price, req.Quantity, exchangeOrder.OrderID)
			return order, nil
		}

		lastErr = err

		// 判断错误类型
		errStr := err.Error()
		if isDuplicateClientOrderError(err) {
			if recovered, ok := oe.recoverOpenOrder(req, err); ok {
				return recovered, nil
			}
			return nil, fmt.Errorf("客户端订单ID重复但未能找回订单: %w", err)
		}
		if isAmbiguousOrderError(err) {
			if recovered, ok := oe.recoverOpenOrder(req, err); ok {
				return recovered, nil
			}
		}
		if strings.Contains(errStr, "-4061") {
			// 持仓模式不匹配：双向持仓 vs 单向持仓
			logger.Error("❌ 下单失败，请在交易所将双向持仓改为单向持仓。错误码: -4061")
			return nil, fmt.Errorf("持仓模式不匹配: %w", err)
		} else if isRateLimitError(err) {
			// 速率限制，等待后重试
			logger.Warn("⚠️ 触发速率限制，等待后重试...")
			time.Sleep(oe.rateLimitRetryDelay)
			continue
		} else if isPostOnlyError(err) {
			postOnlyFailCount++
			logger.Warn("⚠️ [%s] PostOnly被拒(%d/3): %s %.2f，继续保持Maker模式，不降级吃单",
				oe.exchange.GetName(), postOnlyFailCount, req.Side, req.Price)
			if postOnlyFailCount < 3 {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return nil, fmt.Errorf("PostOnly连续被拒，已停止该订单以避免吃单: %w", err)
		} else if strings.Contains(errStr, "-4061") {
			// 持仓模式不匹配（已在前面处理，这里保留以防万一）
			return nil, err
		} else if strings.Contains(errStr, "-2019") || strings.Contains(errStr, "保证金不足") || strings.Contains(errStr, "insufficient") {
			// 保证金不足，不重试
			return nil, err
		} else if strings.Contains(errStr, "-1021") {
			// 时间戳不同步，不重试
			return nil, err
		}

		// 其他错误，短暂等待后重试
		if i < maxRetries {
			time.Sleep(oe.orderRetryDelay)
		}
	}

	return nil, fmt.Errorf("下单失败（重试%d次）: %w", maxRetries, lastErr)
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "-1003") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "too many requests")
}

func isDuplicateClientOrderError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	duplicate := strings.Contains(errStr, "duplicate") ||
		strings.Contains(errStr, "duplicated") ||
		strings.Contains(errStr, "already exist") ||
		strings.Contains(errStr, "already used")
	clientID := strings.Contains(errStr, "client") ||
		strings.Contains(errStr, "clientoid") ||
		strings.Contains(errStr, "clordid") ||
		strings.Contains(errStr, "orderlinkid")
	return duplicate && clientID
}

func isAmbiguousOrderError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline") ||
		strings.Contains(errStr, "context canceled") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "temporarily") ||
		strings.Contains(errStr, "try again") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "server error") ||
		strings.Contains(errStr, "internal")
}

func (oe *ExchangeOrderExecutor) recoverOpenOrder(req *OrderRequest, cause error) (*Order, bool) {
	if strings.TrimSpace(req.ClientOrderID) == "" {
		return nil, false
	}
	ctx, cancel := apiContext()
	openOrders, err := oe.exchange.GetOpenOrders(ctx, oe.symbol)
	cancel()
	if err != nil {
		logger.Warn("⚠️ [%s] 下单异常后查询挂单失败，无法确认 ClientOID=%s 是否已提交: %v",
			oe.exchange.GetName(), req.ClientOrderID, err)
		return nil, false
	}
	for _, exchangeOrder := range openOrders {
		if exchangeOrder == nil || exchangeOrder.ClientOrderID != req.ClientOrderID {
			continue
		}
		logger.Warn("🧩 [%s] 下单返回异常但已通过 ClientOID 找回挂单: clientOID=%s, orderID=%d, 原因: %v",
			oe.exchange.GetName(), req.ClientOrderID, exchangeOrder.OrderID, cause)
		return &Order{
			OrderID:       exchangeOrder.OrderID,
			ClientOrderID: firstNonEmpty(exchangeOrder.ClientOrderID, req.ClientOrderID),
			Symbol:        req.Symbol,
			Side:          req.Side,
			Price:         req.Price,
			Quantity:      req.Quantity,
			Status:        string(exchangeOrder.Status),
			CreatedAt:     time.Now(),
		}, true
	}
	return nil, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// BatchPlaceOrders 批量下单
// 返回：成功下单的订单列表，以及是否出现保证金不足错误
func (oe *ExchangeOrderExecutor) BatchPlaceOrders(orders []*OrderRequest) ([]*Order, bool) {
	if len(orders) == 0 {
		return nil, false
	}
	placedByIndex := make([]*Order, len(orders))
	var hasMarginError bool
	var mu sync.Mutex
	concurrency := orderConcurrency()
	if len(orders) < concurrency {
		concurrency = len(orders)
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, orderReq := range orders {
		wg.Add(1)
		sem <- struct{}{}
		go func(index int, req *OrderRequest) {
			defer wg.Done()
			defer func() { <-sem }()
			order, err := oe.PlaceOrder(req)
			if err != nil {
				logger.Warn("⚠️ [%s] 下单失败 %.2f %s: %v",
					oe.exchange.GetName(), req.Price, req.Side, err)

				// 检查是否是保证金不足错误
				errStr := err.Error()
				if strings.Contains(errStr, "保证金不足") || strings.Contains(errStr, "-2019") || strings.Contains(errStr, "insufficient") {
					mu.Lock()
					hasMarginError = true
					mu.Unlock()
					logger.Error("❌ [保证金不足] 订单 %.2f %s 因保证金不足失败", req.Price, req.Side)
				}
				return
			}
			placedByIndex[index] = order
		}(i, orderReq)
	}
	wg.Wait()

	placedOrders := make([]*Order, 0, len(orders))
	for _, order := range placedByIndex {
		if order != nil {
			placedOrders = append(placedOrders, order)
		}
	}

	return placedOrders, hasMarginError
}

func orderConcurrency() int {
	concurrency := runtime.GOMAXPROCS(0)
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 3 {
		concurrency = 3
	}
	if configured := strings.TrimSpace(os.Getenv("NEXUS_ORDER_CONCURRENCY")); configured != "" {
		if parsed, err := strconv.Atoi(configured); err == nil && parsed > 0 {
			concurrency = parsed
		}
	}
	if concurrency > 16 {
		concurrency = 16
	}
	return concurrency
}

func orderLocalRateLimit() int {
	if configured := strings.TrimSpace(os.Getenv("NEXUS_ORDER_RATE_PER_SEC")); configured != "" {
		if parsed, err := strconv.Atoi(configured); err == nil && parsed > 0 {
			if parsed > 25 {
				return 25
			}
			return parsed
		}
	}
	return 4
}

func orderLocalBurst() int {
	if configured := strings.TrimSpace(os.Getenv("NEXUS_ORDER_RATE_BURST")); configured != "" {
		if parsed, err := strconv.Atoi(configured); err == nil && parsed > 0 {
			if parsed > 30 {
				return 30
			}
			return parsed
		}
	}
	return 6
}

// CancelOrder 取消订单
func (oe *ExchangeOrderExecutor) CancelOrder(orderID int64) error {
	// 限流
	waitCtx, waitCancel := apiContext()
	if err := oe.rateLimiter.Wait(waitCtx); err != nil {
		waitCancel()
		return fmt.Errorf("速率限制等待失败: %v", err)
	}
	if err := ratelimit.Wait(waitCtx, exchangeTradeRateLimitProfile(oe.exchange.GetName())); err != nil {
		waitCancel()
		return fmt.Errorf("交易所全局限速等待失败: %v", err)
	}
	waitCancel()

	ctx, cancel := apiContext()
	err := oe.exchange.CancelOrder(ctx, oe.symbol, orderID)
	cancel()
	if err != nil {
		// 如果是"Unknown order"错误，说明订单已经不存在（可能已成交或已取消），不算错误
		errStr := err.Error()
		if strings.Contains(errStr, "-2011") || strings.Contains(errStr, "Unknown order") || strings.Contains(errStr, "does not exist") {
			logger.Info("ℹ️ [%s] 订单 %d 已不存在（可能已成交或已取消），跳过取消", oe.exchange.GetName(), orderID)
			return nil
		}
		return fmt.Errorf("取消订单失败: %v", err)
	}

	logger.Info("✅ [%s] 取消订单成功: %d", oe.exchange.GetName(), orderID)
	return nil
}

// BatchCancelOrders 批量撤单
func (oe *ExchangeOrderExecutor) BatchCancelOrders(orderIDs []int64) error {
	if len(orderIDs) == 0 {
		return nil
	}
	waitCtx, waitCancel := apiContext()
	if err := ratelimit.Wait(waitCtx, exchangeTradeRateLimitProfile(oe.exchange.GetName())); err != nil {
		waitCancel()
		return fmt.Errorf("交易所全局限速等待失败: %v", err)
	}
	waitCancel()

	// 使用交易所的批量撤单接口
	ctx, cancel := apiContext()
	err := oe.exchange.BatchCancelOrders(ctx, oe.symbol, orderIDs)
	cancel()
	if err != nil {
		logger.Warn("⚠️ [%s] 批量撤单失败: %v，尝试单个撤单", oe.exchange.GetName(), err)
		// 如果批量撤单失败，尝试单个撤单
		var failed []string
		for _, orderID := range orderIDs {
			if err := oe.CancelOrder(orderID); err != nil {
				logger.Warn("⚠️ [%s] 取消订单 %d 失败: %v", oe.exchange.GetName(), orderID, err)
				failed = append(failed, fmt.Sprintf("%d: %v", orderID, err))
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("批量撤单失败，且 %d 个订单单独撤销失败: %s", len(failed), strings.Join(failed, "; "))
		}
	}

	return nil
}

func exchangeTradeRateLimitProfile(exchangeName string) ratelimit.Profile {
	exchangeKey := strings.ToUpper(strings.TrimSpace(exchangeName))
	exchangeKey = strings.ReplaceAll(exchangeKey, ".", "")
	switch {
	case strings.Contains(exchangeKey, "OKX"):
		return ratelimit.Profile{Exchange: "OKX", Bucket: "TRADE", DefaultQPS: 27}
	case strings.Contains(exchangeKey, "BYBIT"):
		return ratelimit.Profile{Exchange: "BYBIT", Bucket: "TRADE", DefaultQPS: 9}
	case strings.Contains(exchangeKey, "GATE"):
		return ratelimit.Profile{Exchange: "GATE", Bucket: "TRADE", DefaultQPS: 9}
	case strings.Contains(exchangeKey, "HYPER"):
		return ratelimit.Profile{Exchange: "HYPERLIQUID", Bucket: "TRADE", DefaultQPS: 18}
	case strings.Contains(exchangeKey, "BINANCE"):
		return ratelimit.Profile{Exchange: "BINANCE", Bucket: "TRADE", DefaultQPS: 9}
	case strings.Contains(exchangeKey, "BITGET"):
		return ratelimit.Profile{Exchange: "BITGET", Bucket: "TRADE", DefaultQPS: 9}
	default:
		return ratelimit.Profile{Exchange: exchangeKey, Bucket: "TRADE", DefaultQPS: 9}
	}
}

// CheckOrderStatus 检查订单状态
func (oe *ExchangeOrderExecutor) CheckOrderStatus(orderID int64) (string, float64, error) {
	ctx, cancel := apiContext()
	order, err := oe.exchange.GetOrder(ctx, oe.symbol, orderID)
	cancel()
	if err != nil {
		return "", 0, err
	}

	return string(order.Status), order.ExecutedQty, nil
}

// GetOpenOrders 获取未完成订单
func (oe *ExchangeOrderExecutor) GetOpenOrders() ([]interface{}, error) {
	ctx, cancel := apiContext()
	orders, err := oe.exchange.GetOpenOrders(ctx, oe.symbol)
	cancel()
	if err != nil {
		return nil, err
	}

	// 转换为 interface{} 列表（为了兼容现有代码）
	result := make([]interface{}, len(orders))
	for i, order := range orders {
		result[i] = order
	}

	return result, nil
}
