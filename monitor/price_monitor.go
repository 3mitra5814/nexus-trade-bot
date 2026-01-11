package monitor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"nexus-trade-bot/exchange"
	"nexus-trade-bot/logger"
)

/*
PriceMonitor 架构说明：

1. **全局唯一的价格流**：
   - 整个系统中只有一个 PriceMonitor 实例（在 main.go 中创建）
   - 所有组件需要价格时，应该通过 priceMonitor.GetLastPrice() 获取
   - 不要在其他地方独立启动价格流

2. **价格获取方式**：
   - 必须使用 WebSocket 推送（毫秒级量化系统要求）
   - WebSocket 失败时系统将停止运行，不会降级
   - 价格缓存在内存中，读取无阻塞

3. **依赖关系**：
   - 依赖 exchange.IExchange 接口
   - 通过 exchange.StartPriceStream() 启动 WebSocket
   - WebSocket 是唯一的价格来源
*/

// PriceChange 价格变化事件
type PriceChange struct {
	OldPrice  float64
	NewPrice  float64
	Change    float64
	Timestamp time.Time
}

// PriceMonitor 价格监控器
type PriceMonitor struct {
	mu            sync.Mutex
	symbol        string
	exchange      exchange.IExchange // 依赖交易所接口
	lastPrice     atomic.Value       // float64
	lastPriceStr  atomic.Value       // string - 原始价格字符串（用于检测小数位数）
	lastPriceTime atomic.Value       // time.Time

	subscribers       map[chan PriceChange]struct{}
	latestPriceChange atomic.Value // *PriceChange - 保存最新的价格更新（不阻塞）
	isRunning         atomic.Bool
	ctx               context.Context
	cancel            context.CancelFunc

	// 时间配置
	priceSendInterval time.Duration
}

// NewPriceMonitor 创建价格监控器
// 参数说明：
// - ex: 交易所接口（用于启动价格流和轮询价格）
// - symbol: 交易对符号
// - priceSendInterval: 价格推送间隔（毫秒）
func NewPriceMonitor(ex exchange.IExchange, symbol string, priceSendInterval int) *PriceMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	pm := &PriceMonitor{
		symbol:            symbol,
		exchange:          ex,
		subscribers:       make(map[chan PriceChange]struct{}),
		ctx:               ctx,
		cancel:            cancel,
		priceSendInterval: time.Duration(priceSendInterval) * time.Millisecond,
	}
	pm.lastPrice.Store(0.0)
	pm.lastPriceStr.Store("")
	pm.lastPriceTime.Store(time.Time{})
	pm.latestPriceChange.Store((*PriceChange)(nil))
	return pm
}

// Start 启动价格监控
func (pm *PriceMonitor) Start() error {
	pm.mu.Lock()
	if pm.isRunning.Load() {
		pm.mu.Unlock()
		return fmt.Errorf("价格监控已在运行")
	}

	select {
	case <-pm.ctx.Done():
		pm.ctx, pm.cancel = context.WithCancel(context.Background())
		pm.subscribers = make(map[chan PriceChange]struct{})
	default:
	}

	pm.isRunning.Store(true)
	ctx := pm.ctx
	pm.mu.Unlock()

	// 启动价格流（WebSocket）- 这是唯一的价格来源
	// 注意：毫秒级量化系统不能容忍 REST API 轮询的延迟
	err := pm.exchange.StartPriceStream(ctx, pm.symbol, func(price float64) {
		defer recoverMonitor("价格推送回调")
		pm.updatePrice(price)
	})
	if err != nil {
		// WebSocket 失败时直接返回错误，系统将停止
		pm.mu.Lock()
		pm.isRunning.Store(false)
		pm.mu.Unlock()
		return fmt.Errorf("启动价格流失败（WebSocket 是唯一价格来源）: %w", err)
	}

	logger.Info("✅ 价格监控已启动 (WebSocket 推送)")
	go pm.periodicPriceSender() // 启动定期发送协程

	return nil
}

// pollPrice 已移除 - 毫秒级量化系统不使用 REST API 轮询
// WebSocket 是唯一的价格来源，失败时系统应该停止运行

// updatePrice 更新价格状态
func (pm *PriceMonitor) updatePrice(newPrice float64) {
	if newPrice <= 0 {
		return
	}

	oldPrice := pm.GetLastPrice()

	// 存储新价格
	pm.lastPrice.Store(newPrice)
	pm.lastPriceStr.Store(fmt.Sprintf("%f", newPrice)) // 简单转换，精度由后续逻辑处理
	pm.lastPriceTime.Store(time.Now())

	// 如果价格有变化，生成事件
	if oldPrice > 0 && newPrice != oldPrice {
		change := newPrice - oldPrice
		event := &PriceChange{
			OldPrice:  oldPrice,
			NewPrice:  newPrice,
			Change:    change,
			Timestamp: time.Now(),
		}
		pm.latestPriceChange.Store(event)
	}
}

// periodicPriceSender 定期发送最新价格
func (pm *PriceMonitor) periodicPriceSender() {
	defer recoverMonitor("价格事件分发")
	ticker := time.NewTicker(pm.priceSendInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			runProtected("价格事件分发", func() {
				// 获取最新价格更新
				if latestVal := pm.latestPriceChange.Load(); latestVal != nil {
					latestChange := latestVal.(*PriceChange)
					if latestChange != nil {
						delivered := false
						pm.mu.Lock()
						for subscriber := range pm.subscribers {
							select {
							case subscriber <- *latestChange:
								delivered = true
							default:
								// 每个订阅者只保留最新价格，慢消费者不阻塞交易主循环。
							}
						}
						pm.mu.Unlock()
						if delivered {
							pm.latestPriceChange.Store((*PriceChange)(nil))
						}
					}
				}
			})
		}
	}
}

// Stop 停止价格监控
func (pm *PriceMonitor) Stop() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if !pm.isRunning.Load() {
		return
	}
	if pm.cancel != nil {
		pm.cancel()
	}
	for subscriber := range pm.subscribers {
		close(subscriber)
		delete(pm.subscribers, subscriber)
	}
	pm.isRunning.Store(false)
}

// GetLastPrice 获取最新价格
func (pm *PriceMonitor) GetLastPrice() float64 {
	if val := pm.lastPrice.Load(); val != nil {
		return val.(float64)
	}
	return 0
}

// GetLastPriceString 获取最新价格的原始字符串（用于检测小数位数）
func (pm *PriceMonitor) GetLastPriceString() string {
	if val := pm.lastPriceStr.Load(); val != nil {
		return val.(string)
	}
	return ""
}

// Subscribe 订阅价格变化
func (pm *PriceMonitor) Subscribe() <-chan PriceChange {
	outCh := make(chan PriceChange, 10)
	pm.mu.Lock()
	pm.subscribers[outCh] = struct{}{}
	ctx := pm.ctx
	pm.mu.Unlock()

	go func() {
		defer recoverMonitor("价格订阅清理")
		<-ctx.Done()
		pm.mu.Lock()
		if _, ok := pm.subscribers[outCh]; ok {
			delete(pm.subscribers, outCh)
			close(outCh)
		}
		pm.mu.Unlock()
	}()
	return outCh
}

func recoverMonitor(name string) {
	if r := recover(); r != nil {
		logger.Error("🛡️ [%s] 捕获异常，协程已安全退出: %v", name, r)
	}
}

func runProtected(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("🛡️ [%s] 捕获异常，本轮任务已跳过: %v", name, r)
		}
	}()
	fn()
}
