package bitget

/*
Bitget WebSocket 架构说明：

1. **WebSocket下单**：Bitget不支持WebSocket下单，所有下单操作请使用REST API

2. **WebSocket用途**：
   - 公共频道：订阅价格推送 (ticker)
   - 私有频道：订阅订单更新 (orders)

3. **启动流程**：
   - main.go 中通过 PriceMonitor.Start() 启动价格流
   - main.go 中通过 ex.StartOrderStream() 启动订单流
   - 价格流和订单流共用同一个 WebSocketManager 实例
   - 公共频道和私有频道是两个独立的WebSocket连接

4. **价格获取方式**：
   - 优先从 WebSocket 缓存获取 (GetLatestPrice)
   - 如果缓存为空，降级使用 REST API
*/

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexus-trade-bot/logger"

	"github.com/gorilla/websocket"
)

const (
	// Bitget V2 WebSocket 地址
	BitgetWSPrivate = "wss://ws.bitget.com/v2/ws/private"
	BitgetWSPublic  = "wss://ws.bitget.com/v2/ws/public"

	// API Code - 重要：不要丢失！
	BitgetAPICode = "3xh1b"
)

// WebSocketManager Bitget WebSocket 管理器
type WebSocketManager struct {
	apiKey     string
	secretKey  string
	passphrase string

	// 连接管理
	privateConn *websocket.Conn
	publicConn  *websocket.Conn
	mu          sync.RWMutex

	// 回调函数
	orderCallback func(interface{})
	priceCallback func(string, float64) // symbol, price

	// 控制
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 价格缓存
	latestPrice float64
	priceMu     sync.RWMutex

	// 🔥 标记消息处理是否已启动
	privateHandlerStarted bool
	publicHandlerStarted  bool

	// 🔥 重连控制
	publicReconnectChan  chan struct{}
	privateReconnectChan chan struct{}
	reconnectDelay       time.Duration
	subscribedSymbol     string // 记录订阅的交易对，用于重连后重新订阅
	productType          string
}

// SetPriceCallback 设置价格回调
func (w *WebSocketManager) SetPriceCallback(callback func(string, float64)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.priceCallback = callback
}

// IsRunning 检查 WebSocket 是否运行中
func (w *WebSocketManager) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.publicHandlerStarted || w.privateHandlerStarted
}

func (w *WebSocketManager) SetProductType(productType string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.productType = normalizeWSProductType(productType)
}

// OrderResponse 订单响应
type OrderResponse struct {
	Success   bool
	OrderID   string
	ClientOid string
	Code      string
	Msg       string
}

// WebSocket 消息结构
type WSMessage struct {
	Op   string          `json:"op"`
	Args []interface{}   `json:"args,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
	Code json.RawMessage `json:"code,omitempty"` // 可能是字符串或数字
	Msg  string          `json:"msg,omitempty"`
}

// GetCodeString 获取 code 的字符串值
func (m *WSMessage) GetCodeString() string {
	if len(m.Code) == 0 {
		return ""
	}
	// 尝试解析为数字
	var codeNum int
	if err := json.Unmarshal(m.Code, &codeNum); err == nil {
		return fmt.Sprintf("%d", codeNum)
	}
	// 尝试解析为字符串
	var codeStr string
	if err := json.Unmarshal(m.Code, &codeStr); err == nil {
		return codeStr
	}
	return ""
}

// WebSocket 订阅参数
type WSSubscribeArg struct {
	InstType string `json:"instType"`
	Channel  string `json:"channel"`
	InstId   string `json:"instId,omitempty"`
}

// NewWebSocketManager 创建 WebSocket 管理器
func NewWebSocketManager(apiKey, secretKey, passphrase string) *WebSocketManager {
	return &WebSocketManager{
		apiKey:               apiKey,
		secretKey:            secretKey,
		passphrase:           passphrase,
		publicReconnectChan:  make(chan struct{}, 1),
		privateReconnectChan: make(chan struct{}, 1),
		reconnectDelay:       5 * time.Second,
		productType:          "USDT-FUTURES",
	}
}

// publicConnectLoop 公共频道连接循环（自动重连）
func (w *WebSocketManager) publicConnectLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			logger.Info("✅ [Bitget WS公共] 停止连接循环")
			return
		default:
		}

		logger.Info("🔗 [Bitget WS公共] 正在连接...")

		// 连接公共频道
		conn, _, err := websocket.DefaultDialer.Dial(BitgetWSPublic, nil)
		if err != nil {
			logger.Error("❌ [Bitget WS公共] 连接失败: %v，%v后重试", err, w.reconnectDelay)
			// 使用 select 等待，可以立即响应 context 取消
			select {
			case <-w.ctx.Done():
				logger.Info("✅ [Bitget WS公共] 停止连接循环")
				return
			case <-time.After(w.reconnectDelay):
			}
			continue
		}

		w.mu.Lock()
		w.publicConn = conn
		symbol := w.subscribedSymbol
		w.mu.Unlock()

		logger.Info("✅ [Bitget WS公共] 已连接")

		// 订阅价格更新
		if err := w.subscribeTicker(symbol); err != nil {
			logger.Error("❌ [Bitget WS公共] 订阅失败: %v", err)
			conn.Close()
			// 使用 select 等待，可以立即响应 context 取消
			select {
			case <-w.ctx.Done():
				logger.Info("✅ [Bitget WS公共] 停止连接循环")
				return
			case <-time.After(w.reconnectDelay):
			}
			continue
		}

		// 启动 ping 和读取协程
		done := make(chan struct{})
		go func() {
			w.keepAlive(conn, "公共", w.publicReconnectChan)
			close(done)
		}()

		// 启动读取循环（阻塞直到连接断开）
		w.handlePublicMessages(conn)

		// 等待 keepAlive 退出（同时监听 context 取消）
		select {
		case <-done:
			// keepAlive 正常退出
		case <-w.ctx.Done():
			// context 取消，不等待 keepAlive
			logger.Info("✅ [Bitget WS公共] 停止连接循环")
			return
		}

		// 连接断开，清理
		w.mu.Lock()
		if w.publicConn == conn {
			w.publicConn = nil
		}
		w.mu.Unlock()
		conn.Close()

		// 检查是否因为 context 取消而断开，如果是则直接退出
		select {
		case <-w.ctx.Done():
			logger.Info("✅ [Bitget WS公共] 停止连接循环")
			return
		default:
		}

		logger.Warn("⚠️ [Bitget WS公共] 连接断开，%v后重连...", w.reconnectDelay)
		// 使用 select 等待，可以立即响应 context 取消
		select {
		case <-w.ctx.Done():
			logger.Info("✅ [Bitget WS公共] 停止连接循环")
			return
		case <-time.After(w.reconnectDelay):
		}
	}
}

// privateConnectLoop 私有频道连接循环（自动重连）
func (w *WebSocketManager) privateConnectLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			logger.Info("✅ [Bitget WS私有] 停止连接循环")
			return
		default:
		}

		logger.Info("🔗 [Bitget WS私有] 正在连接...")

		// 连接私有频道
		if err := w.connectPrivate(); err != nil {
			logger.Error("❌ [Bitget WS私有] 连接失败: %v，%v后重试", err, w.reconnectDelay)
			// 使用 select 等待，可以立即响应 context 取消
			select {
			case <-w.ctx.Done():
				logger.Info("✅ [Bitget WS私有] 停止连接循环")
				return
			case <-time.After(w.reconnectDelay):
			}
			continue
		}

		w.mu.Lock()
		conn := w.privateConn
		symbol := w.subscribedSymbol
		w.mu.Unlock()

		// 订阅订单更新
		if err := w.subscribeOrders(symbol); err != nil {
			logger.Error("❌ [Bitget WS私有] 订阅失败: %v", err)
			conn.Close()
			// 使用 select 等待，可以立即响应 context 取消
			select {
			case <-w.ctx.Done():
				logger.Info("✅ [Bitget WS私有] 停止连接循环")
				return
			case <-time.After(w.reconnectDelay):
			}
			continue
		}

		// 启动 ping 和读取协程
		done := make(chan struct{})
		go func() {
			w.keepAlive(conn, "私有", w.privateReconnectChan)
			close(done)
		}()

		// 启动读取循环（阻塞直到连接断开）
		w.handlePrivateMessages(conn)

		// 等待 keepAlive 退出（同时监听 context 取消）
		select {
		case <-done:
			// keepAlive 正常退出
		case <-w.ctx.Done():
			// context 取消，不等待 keepAlive
			logger.Info("✅ [Bitget WS私有] 停止连接循环")
			return
		}

		// 连接断开，清理
		w.mu.Lock()
		if w.privateConn == conn {
			w.privateConn = nil
		}
		w.mu.Unlock()
		conn.Close()

		// 检查是否因为 context 取消而断开，如果是则直接退出
		select {
		case <-w.ctx.Done():
			logger.Info("✅ [Bitget WS私有] 停止连接循环")
			return
		default:
		}

		logger.Warn("⚠️ [Bitget WS私有] 连接断开，%v后重连...", w.reconnectDelay)
		// 使用 select 等待，可以立即响应 context 取消
		select {
		case <-w.ctx.Done():
			logger.Info("✅ [Bitget WS私有] 停止连接循环")
			return
		case <-time.After(w.reconnectDelay):
		}
	}
}

// ConnectAndLogin 已废弃 - 请使用 Start() 方法
// 保留该方法以兼容旧代码，但建议直接调用 Start()
func (w *WebSocketManager) ConnectAndLogin(ctx context.Context, symbol string) error {
	// 直接调用 Start 方法
	return w.Start(ctx, symbol, nil)
}

// Start 启动 WebSocket 连接（公共频道+私有频道）
// 订阅价格更新(ticker)和订单更新(orders)
// callback: 订单更新回调函数，为nil时不订阅订单频道
func (w *WebSocketManager) Start(ctx context.Context, symbol string, callback func(interface{})) error {
	w.mu.Lock()
	if w.ctx == nil {
		w.ctx, w.cancel = context.WithCancel(ctx)
	}
	w.orderCallback = callback
	w.subscribedSymbol = symbol // 记录订阅的交易对

	// 🔥 启动公共频道重连循环
	if !w.publicHandlerStarted {
		w.wg.Add(1)
		go w.publicConnectLoop()
		w.publicHandlerStarted = true
	}

	// 🔥 启动私有频道重连循环（如果有订单回调）
	if callback != nil && !w.privateHandlerStarted {
		w.wg.Add(1)
		go w.privateConnectLoop()
		w.privateHandlerStarted = true
	}
	w.mu.Unlock()

	if callback != nil {
		logger.Info("✅ [Bitget WebSocket] 启动成功，将订阅 %s 的价格和订单更新", symbol)
	} else {
		logger.Info("✅ [Bitget WebSocket] 启动成功，将订阅 %s 的价格更新", symbol)
	}
	return nil
}

// Stop 停止 WebSocket
func (w *WebSocketManager) Stop() {
	// 🔥 第一步：取消 context 并关闭连接（需要加锁）
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}

	if w.privateConn != nil {
		w.privateConn.Close()
	}
	if w.publicConn != nil {
		w.publicConn.Close()
	}
	w.mu.Unlock()

	// 🔥 第二步：等待所有 goroutine 退出（不能持有锁，避免死锁）
	w.wg.Wait()
	w.mu.Lock()
	w.privateConn = nil
	w.publicConn = nil
	w.privateHandlerStarted = false
	w.publicHandlerStarted = false
	w.ctx = nil
	w.cancel = nil
	w.mu.Unlock()
	logger.Info("✅ [Bitget WebSocket] 已停止")
}

// connectPrivate 连接私有 WebSocket
func (w *WebSocketManager) connectPrivate() error {
	conn, _, err := websocket.DefaultDialer.Dial(BitgetWSPrivate, nil)
	if err != nil {
		return err
	}
	w.privateConn = conn

	// 发送登录认证
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	sign := w.generateSign(timestamp, "GET", "/user/verify")

	loginMsg := map[string]interface{}{
		"op": "login",
		"args": []map[string]string{
			{
				"apiKey":     w.apiKey,
				"passphrase": w.passphrase,
				"timestamp":  timestamp,
				"sign":       sign,
			},
		},
	}

	if err := conn.WriteJSON(loginMsg); err != nil {
		return fmt.Errorf("发送登录消息失败: %w", err)
	}

	// 等待登录响应
	var resp WSMessage
	if err := conn.ReadJSON(&resp); err != nil {
		return fmt.Errorf("读取登录响应失败: %w", err)
	}

	codeStr := resp.GetCodeString()
	if codeStr != "0" && codeStr != "" {
		return fmt.Errorf("登录失败: code=%s, msg=%s", codeStr, resp.Msg)
	}

	logger.Info("✅ [Bitget WebSocket] 私有频道登录成功")
	return nil
}

// connectPublic 连接公共 WebSocket
func (w *WebSocketManager) connectPublic() error {
	conn, _, err := websocket.DefaultDialer.Dial(BitgetWSPublic, nil)
	if err != nil {
		return err
	}
	w.publicConn = conn
	logger.Info("✅ [Bitget WebSocket] 公共频道连接成功")
	return nil
}

// subscribeOrders 订阅订单更新
func (w *WebSocketManager) subscribeOrders(symbol string) error {
	w.mu.RLock()
	productType := w.productType
	w.mu.RUnlock()
	subMsg := map[string]interface{}{
		"op": "subscribe",
		"args": []WSSubscribeArg{
			{
				InstType: productType,
				Channel:  "orders",
				InstId:   "default", // 订阅所有交易对
			},
		},
	}

	logger.Info("📡 [Bitget WS] 订阅私有频道: orders")
	return w.privateConn.WriteJSON(subMsg)
}

// subscribeTicker 订阅价格更新
func (w *WebSocketManager) subscribeTicker(symbol string) error {
	w.mu.RLock()
	productType := w.productType
	w.mu.RUnlock()
	subMsg := map[string]interface{}{
		"op": "subscribe",
		"args": []WSSubscribeArg{
			{
				InstType: productType,
				Channel:  "ticker",
				InstId:   symbol,
			},
		},
	}

	return w.publicConn.WriteJSON(subMsg)
}

func normalizeWSProductType(productType string) string {
	productType = strings.ToUpper(strings.TrimSpace(productType))
	productType = strings.ReplaceAll(productType, "_", "-")
	if productType == "" {
		return "USDT-FUTURES"
	}
	return productType
}

// handlePrivateMessages 处理私有频道消息（订单更新和成交明细）
func (w *WebSocketManager) handlePrivateMessages(conn *websocket.Conn) {
	// 🔥 设置读取超时：90秒
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))

	for {
		select {
		case <-w.ctx.Done():
			return
		default:
			_, message, err := conn.ReadMessage()
			if err != nil {
				logger.Warn("⚠️ [Bitget WebSocket] 读取私有消息失败: %v", err)
				// 🔥 关键：触发重连
				select {
				case w.privateReconnectChan <- struct{}{}:
				default:
				}
				return
			}

			// 🔥 收到消息后更新读取超时
			conn.SetReadDeadline(time.Now().Add(90 * time.Second))

			// 忽略 pong 响应
			if string(message) == "pong" {
				logger.Debug("💓 [Bitget WS私有] 收到 pong")
				continue
			}

			var msg struct {
				Event  string          `json:"event"`  // subscribe / error / login
				Op     string          `json:"op"`     // trade (下单响应)
				Action string          `json:"action"` // snapshot / update
				Arg    WSSubscribeArg  `json:"arg"`
				Data   json.RawMessage `json:"data"`
				Code   json.RawMessage `json:"code"`
				Msg    string          `json:"msg"`
			}

			if err := json.Unmarshal(message, &msg); err != nil {
				logger.Warn("⚠️ [Bitget WebSocket] 解析私有消息失败: %v", err)
				continue
			}

			// 🔍 调试：打印收到的消息类型
			logger.Debug("🔍 [Bitget WS私有] event=%s, op=%s, action=%s, channel=%s",
				msg.Event, msg.Op, msg.Action, msg.Arg.Channel)

			// 处理订阅确认
			if msg.Event == "subscribe" {
				logger.Debug("✅ [Bitget WS] 订阅成功: %s", msg.Arg.Channel)
				continue
			}

			// 处理错误消息
			if msg.Event == "error" {
				logger.Error("❌ [Bitget WS] 错误: %s", msg.Msg)
				continue
			}

			// 处理订单推送 (channel="orders")
			if msg.Arg.Channel == "orders" && len(msg.Data) > 0 {
				logger.Debug("🔍 [Bitget WS订单] 推送数据: %s", string(msg.Data))
				w.handleOrderUpdate(msg.Data)
				continue
			}
		}
	}
}

// handlePublicMessages 处理公共频道消息（价格更新）
func (w *WebSocketManager) handlePublicMessages(conn *websocket.Conn) {
	// 🔥 设置读取超时：90秒（大于3倍ping间隔）
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))

	for {
		select {
		case <-w.ctx.Done():
			return
		default:
			_, message, err := conn.ReadMessage()
			if err != nil {
				logger.Warn("⚠️ [Bitget WebSocket] 读取公共消息失败: %v", err)
				// 🔥 关键：触发重连
				select {
				case w.publicReconnectChan <- struct{}{}:
				default:
				}
				return
			}

			// 🔥 收到消息后更新读取超时
			conn.SetReadDeadline(time.Now().Add(90 * time.Second))

			// 忽略 pong 响应
			if string(message) == "pong" {
				logger.Debug("💓 [Bitget WS公共] 收到 pong")
				continue
			}

			var msg struct {
				Arg    WSSubscribeArg  `json:"arg"`
				Action string          `json:"action"`
				Data   json.RawMessage `json:"data"`
			}

			if err := json.Unmarshal(message, &msg); err != nil {
				logger.Warn("⚠️ [Bitget WebSocket] 解析公共消息失败: %v", err)
				continue
			}

			// 处理价格更新
			// Bitget V2 推送格式: {"action":"snapshot","arg":{"instType":"USDT-FUTURES","channel":"ticker","instId":"ETHUSDT"},"data":[...]}
			if msg.Arg.Channel == "ticker" && len(msg.Data) > 0 {
				w.handlePriceUpdate(msg.Data)
			}
		}
	}
}

// handleOrderUpdate 处理订单更新
func (w *WebSocketManager) handleOrderUpdate(data json.RawMessage) {
	var updates []map[string]interface{}
	if err := json.Unmarshal(data, &updates); err != nil {
		logger.Warn("⚠️ [Bitget WebSocket] 解析订单更新失败: %v", err)
		return
	}

	//logger.Info("🔍 [Bitget WS] 收到 %d 条订单更新", len(updates))

	for _, update := range updates {
		// 🔍 调试：打印原始订单数据的关键字段
		orderID, _ := update["orderId"].(string)
		status, _ := update["status"].(string)
		side, _ := update["side"].(string)
		accBaseVolume, _ := update["accBaseVolume"].(string)

		// 🔥 关键诊断：如果订单被撤销，打印完整的原始数据
		if status == "cancelled" || status == "canceled" {
			clientOID, _ := update["clientOid"].(string)
			cancelReason, _ := update["cancelReason"].(string)
			price, _ := update["price"].(string)
			instID, _ := update["instId"].(string)
			force, _ := update["force"].(string)
			tradeSide, _ := update["tradeSide"].(string)
			logger.Warn("⚠️ [Bitget 订单被交易所撤销] symbol=%s orderID=%s clientOID=%s side=%s tradeSide=%s price=%s filled=%s force=%s reason=%s",
				instID, orderID, clientOID, side, tradeSide, price, accBaseVolume, force, cancelReason)
		}

		logger.Debug("🔍 [Bitget WS订单] ID=%s, 状态=%s, 方向=%s, 成交量=%s",
			orderID, status, side, accBaseVolume)

		if w.orderCallback != nil {
			// 转换为 OrderUpdate 格式
			orderUpdate := w.parseOrderUpdate(update)
			if orderUpdate != nil {
				logger.Debug("🔍 [Bitget WS订单] 解析后: ID=%d, Status=%s, ExecutedQty=%.4f",
					orderUpdate.OrderID, orderUpdate.Status, orderUpdate.ExecutedQty)
				w.orderCallback(orderUpdate)
			}
		}
	}
}

// handlePriceUpdate 处理价格更新
func (w *WebSocketManager) handlePriceUpdate(data json.RawMessage) {
	var updates []map[string]interface{}
	if err := json.Unmarshal(data, &updates); err != nil {
		logger.Warn("⚠️ [Bitget WebSocket] 解析价格更新失败: %v", err)
		return
	}

	for _, update := range updates {
		// Bitget V2 Ticker 字段是 lastPr
		lastStr, ok := update["lastPr"].(string)
		if !ok {
			// 尝试兼容旧字段
			lastStr, ok = update["last"].(string)
		}

		if ok {
			price, _ := strconv.ParseFloat(lastStr, 64)
			if price > 0 {
				w.priceMu.Lock()
				w.latestPrice = price
				w.priceMu.Unlock()

				if w.priceCallback != nil {
					// instId 是交易对名称
					symbol, _ := update["instId"].(string)
					w.priceCallback(symbol, price)
				}
			}
		}
	}
}

// parseOrderUpdate 解析订单更新
func (w *WebSocketManager) parseOrderUpdate(data map[string]interface{}) *OrderUpdate {
	orderIDStr, _ := data["orderId"].(string)
	orderID, _ := strconv.ParseInt(orderIDStr, 10, 64)

	clientOrderID, _ := data["clientOid"].(string) // 🔥 解析 ClientOrderID

	symbol, _ := data["instId"].(string)
	sideStr, _ := data["side"].(string)
	statusStr, _ := data["status"].(string)
	priceStr, _ := data["price"].(string)
	qtyStr, _ := data["size"].(string)
	filledQtyStr, _ := data["accBaseVolume"].(string)
	baseVolumeStr, _ := data["baseVolume"].(string)
	filledQtyAltStr, _ := data["filledQty"].(string)
	avgPriceStr, _ := data["priceAvg"].(string)
	updateTimeStr, _ := data["uTime"].(string)
	tradeSideStr, _ := data["tradeSide"].(string)
	posSideStr, _ := data["posSide"].(string)

	// 🔍 调试：打印关键字段的原始值
	logger.Debug("🔍 [parseOrderUpdate] accBaseVolume=%v (type=%T), priceAvg=%v (type=%T)",
		data["accBaseVolume"], data["accBaseVolume"], data["priceAvg"], data["priceAvg"])

	price, _ := strconv.ParseFloat(priceStr, 64)
	quantity, _ := strconv.ParseFloat(qtyStr, 64)
	executedQty := parseBitgetExecutedQty(filledQtyStr, baseVolumeStr, filledQtyAltStr)
	avgPrice, _ := strconv.ParseFloat(avgPriceStr, 64)
	updateTime, _ := strconv.ParseInt(updateTimeStr, 10, 64)

	// 🔍 调试：打印解析后的值
	logger.Debug("🔍 [parseOrderUpdate] 解析结果: executedQty=%.4f, avgPrice=%.2f, Price=%.2f", executedQty, avgPrice, price)

	side := SideBuy
	lowerSide := strings.ToLower(strings.TrimSpace(sideStr))
	if lowerSide == "sell" {
		side = SideSell
	} else if lowerSide == "buy" {
		side = SideBuy
	} else {
		lowerTrade := strings.ToLower(strings.TrimSpace(tradeSideStr))
		lowerPos := strings.ToLower(strings.TrimSpace(posSideStr))
		if strings.Contains(lowerTrade, "close") || lowerPos == "short" {
			side = SideSell
		} else if strings.Contains(lowerTrade, "open") || lowerPos == "long" {
			side = SideBuy
		} else if lowerSide != "" {
			logger.Warn("⚠️ [Bitget WS] 未知 side 值: %s (tradeSide=%s, posSide=%s), 默认按买单处理", sideStr, tradeSideStr, posSideStr)
		}
	}

	// 🔥 关键修复：Bitget V2 WebSocket 订单推送的状态值
	// 根据官方文档：live=挂单中, partially_filled=部分成交, filled=完全成交, cancelled=已撤销
	var status OrderStatus = "NEW"
	switch statusStr {
	case "new", "live": // live 表示订单挂单中
		status = "NEW"
	case "partial_filled", "partial-fill", "partially_filled":
		status = "PARTIALLY_FILLED"
	case "filled", "full-fill":
		status = "FILLED"
	case "cancelled", "canceled":
		status = "CANCELED"
	default:
		// 🔍 如果遇到未知状态，记录日志
		logger.Warn("⚠️ [Bitget WS] 未知订单状态: %s, 订单ID: %s", statusStr, orderIDStr)
		status = OrderStatus(statusStr) // 保留原始状态
	}

	return &OrderUpdate{
		OrderID:       orderID,
		ClientOrderID: clientOrderID, // 🔥 包含 ClientOrderID
		Symbol:        symbol,
		Side:          side,
		Type:          OrderTypeLimit,
		Status:        status,
		Price:         price,
		Quantity:      quantity,
		ExecutedQty:   executedQty,
		AvgPrice:      avgPrice,
		UpdateTime:    updateTime,
	}
}

// PlaceOrderWS 已废弃 - Bitget不支持WebSocket下单，请使用REST API
// 保留方法签名以兼容旧代码，但返回错误
func (w *WebSocketManager) PlaceOrderWS(symbol string, side string, price, quantity float64, priceDecimals int) (string, error) {
	return "", fmt.Errorf("Bitget不支持WebSocket下单，请使用REST API")
}

// GetLatestPrice 获取最新价格
func (w *WebSocketManager) GetLatestPrice() float64 {
	w.priceMu.RLock()
	defer w.priceMu.RUnlock()
	return w.latestPrice
}

// keepAlive WebSocket 保活（每15秒发送 ping）
func (w *WebSocketManager) keepAlive(conn *websocket.Conn, connType string, reconnectChan chan struct{}) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			if conn != nil {
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				err := conn.WriteMessage(websocket.TextMessage, []byte("ping"))
				if err != nil {
					logger.Warn("⚠️ [Bitget WS%s] 发送 ping 失败: %v", connType, err)
					// 🔥 关键：ping 失败说明连接已断开，触发重连并退出
					select {
					case reconnectChan <- struct{}{}:
					default:
					}
					return
				}
				logger.Debug("💓 [Bitget WS%s] Ping已发送", connType)
			}
		}
	}
}

// generateSign 生成签名
func (w *WebSocketManager) generateSign(timestamp, method, requestPath string) string {
	message := timestamp + method + requestPath
	mac := hmac.New(sha256.New, []byte(w.secretKey))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
