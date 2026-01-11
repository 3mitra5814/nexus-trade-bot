package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexus-trade-bot/logger"
)

type BybitAdapter struct {
	client           *Client
	symbol           string
	wsManager        *WebSocketManager
	klineWSManager   *KlineWebSocketManager
	orderIDs         *orderIDMapper
	priceDecimals    int
	quantityDecimals int
	baseAsset        string
	quoteAsset       string
	accountLeverage  int
}

type orderIDMapper struct {
	mu       sync.RWMutex
	byString map[string]int64
	byInt    map[int64]string
	table    *crc64.Table
}

func newOrderIDMapper() *orderIDMapper {
	return &orderIDMapper{
		byString: make(map[string]int64),
		byInt:    make(map[int64]string),
		table:    crc64.MakeTable(crc64.ISO),
	}
}

func (m *orderIDMapper) encode(orderID string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if encoded, ok := m.byString[orderID]; ok {
		return encoded
	}

	if numeric, err := strconv.ParseInt(orderID, 10, 64); err == nil && numeric > 0 {
		if existing, ok := m.byInt[numeric]; !ok || existing == orderID {
			m.byString[orderID] = numeric
			m.byInt[numeric] = orderID
			return numeric
		}
	}

	encoded := int64(crc64.Checksum([]byte(orderID), m.table) & 0x7fffffffffffffff)
	if encoded == 0 {
		encoded = 1
	}

	for {
		if existing, ok := m.byInt[encoded]; !ok {
			m.byString[orderID] = encoded
			m.byInt[encoded] = orderID
			return encoded
		} else if existing == orderID {
			m.byString[orderID] = encoded
			return encoded
		}
		encoded++
		if encoded == 0 {
			encoded = 1
		}
	}
}

func (m *orderIDMapper) lookup(orderID int64) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.byInt[orderID]
	return value, ok
}

func NewBybitAdapter(cfg map[string]string, symbol string) (*BybitAdapter, error) {
	apiKey := cfg["api_key"]
	secretKey := cfg["secret_key"]
	if apiKey == "" || secretKey == "" {
		return nil, fmt.Errorf("Bybit API 配置不完整")
	}

	client := NewClient(apiKey, secretKey)
	orderIDs := newOrderIDMapper()
	adapter := &BybitAdapter{
		client:         client,
		symbol:         strings.ToUpper(symbol),
		orderIDs:       orderIDs,
		klineWSManager: NewKlineWebSocketManager(),
	}
	adapter.wsManager = NewWebSocketManager(client, adapter.symbol, adapter.symbol, orderIDs)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := adapter.fetchExchangeInfo(ctx); err != nil {
		logger.Warn("⚠️ [Bybit] 获取合约信息失败: %v，使用默认精度", err)
		adapter.priceDecimals = 2
		adapter.quantityDecimals = 3
	}

	return adapter, nil
}

func (b *BybitAdapter) GetName() string {
	return "Bybit"
}

func (b *BybitAdapter) fetchExchangeInfo(ctx context.Context) error {
	resp, err := b.client.DoPublicRequest(ctx, "GET", "/v5/market/instruments-info", map[string]string{
		"category": "linear",
		"symbol":   b.symbol,
	})
	if err != nil {
		return err
	}

	var result struct {
		List []struct {
			Symbol      string `json:"symbol"`
			BaseCoin    string `json:"baseCoin"`
			QuoteCoin   string `json:"quoteCoin"`
			PriceScale  string `json:"priceScale"`
			PriceFilter struct {
				TickSize string `json:"tickSize"`
			} `json:"priceFilter"`
			LotSizeFilter struct {
				QtyStep string `json:"qtyStep"`
			} `json:"lotSizeFilter"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("解析 Bybit 合约信息失败: %w", err)
	}
	if len(result.List) == 0 {
		return fmt.Errorf("未找到 Bybit 合约信息: %s", b.symbol)
	}

	item := result.List[0]
	b.baseAsset = item.BaseCoin
	b.quoteAsset = item.QuoteCoin
	b.priceDecimals = decimalsFromStep(item.PriceFilter.TickSize)
	if b.priceDecimals == 0 {
		b.priceDecimals = parseInt(item.PriceScale)
	}
	b.quantityDecimals = decimalsFromStep(item.LotSizeFilter.QtyStep)
	if b.quantityDecimals == 0 {
		b.quantityDecimals = 3
	}

	logger.Debug("ℹ️ [Bybit 合约信息] %s - 数量精度:%d, 价格精度:%d, 基础币种:%s, 计价币种:%s",
		b.symbol, b.quantityDecimals, b.priceDecimals, b.baseAsset, b.quoteAsset)
	return nil
}

func (b *BybitAdapter) PlaceOrder(ctx context.Context, req *OrderRequest) (*Order, error) {
	priceDecimals := req.PriceDecimals
	if priceDecimals <= 0 {
		priceDecimals = b.priceDecimals
	}

	body := map[string]any{
		"category":    "linear",
		"symbol":      strings.ToUpper(req.Symbol),
		"side":        sideToBybit(req.Side),
		"orderType":   orderTypeToBybit(req.Type),
		"qty":         formatWithDecimals(req.Quantity, b.quantityDecimals),
		"positionIdx": 0,
	}
	if req.Type == OrderTypeLimit {
		body["price"] = formatWithDecimals(req.Price, priceDecimals)
	}
	if tif := timeInForceToBybit(req.TimeInForce, req.PostOnly); tif != "" {
		body["timeInForce"] = tif
	}
	if req.ReduceOnly {
		body["reduceOnly"] = true
	}
	if req.ClientOrderID != "" {
		body["orderLinkId"] = req.ClientOrderID
	}

	resp, err := b.client.DoSignedRequest(ctx, "POST", "/v5/order/create", nil, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		OrderID     string `json:"orderId"`
		OrderLinkID string `json:"orderLinkId"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("解析 Bybit 下单响应失败: %w", err)
	}

	return &Order{
		OrderID:       b.orderIDs.encode(result.OrderID),
		ClientOrderID: result.OrderLinkID,
		Symbol:        strings.ToUpper(req.Symbol),
		Side:          req.Side,
		Type:          req.Type,
		Price:         req.Price,
		Quantity:      req.Quantity,
		ExecutedQty:   0,
		AvgPrice:      0,
		Status:        OrderStatusNew,
		CreatedAt:     time.Now(),
		UpdateTime:    resp.Time,
	}, nil
}

func (b *BybitAdapter) BatchPlaceOrders(ctx context.Context, orders []*OrderRequest) ([]*Order, bool) {
	placedOrders := make([]*Order, 0, len(orders))
	hasMarginError := false

	for _, orderReq := range orders {
		order, err := b.PlaceOrder(ctx, orderReq)
		if err != nil {
			logger.Warn("⚠️ [Bybit] 下单失败 %.6f %s: %v", orderReq.Price, orderReq.Side, err)
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "insufficient") || strings.Contains(errMsg, "margin") {
				hasMarginError = true
			}
			continue
		}
		placedOrders = append(placedOrders, order)
	}

	return placedOrders, hasMarginError
}

func (b *BybitAdapter) CancelOrder(ctx context.Context, symbol string, orderID int64) error {
	remoteOrderID, ok := b.orderIDs.lookup(orderID)
	if !ok {
		remoteOrderID = strconv.FormatInt(orderID, 10)
	}

	body := map[string]any{
		"category": "linear",
		"symbol":   strings.ToUpper(symbol),
		"orderId":  remoteOrderID,
	}
	_, err := b.client.DoSignedRequest(ctx, "POST", "/v5/order/cancel", nil, body)
	return err
}

func (b *BybitAdapter) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	for _, orderID := range orderIDs {
		if err := b.CancelOrder(ctx, symbol, orderID); err != nil {
			return err
		}
	}
	return nil
}

func (b *BybitAdapter) CancelAllOrders(ctx context.Context, symbol string) error {
	body := map[string]any{
		"category": "linear",
		"symbol":   strings.ToUpper(symbol),
	}
	_, err := b.client.DoSignedRequest(ctx, "POST", "/v5/order/cancel-all", nil, body)
	return err
}

func (b *BybitAdapter) GetOrder(ctx context.Context, symbol string, orderID int64) (*Order, error) {
	query := map[string]string{
		"category": "linear",
		"symbol":   strings.ToUpper(symbol),
	}
	if remoteOrderID, ok := b.orderIDs.lookup(orderID); ok {
		query["orderId"] = remoteOrderID
	} else {
		query["orderId"] = strconv.FormatInt(orderID, 10)
	}

	items, _, err := b.fetchOrders(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("未找到 Bybit 订单: %d", orderID)
	}

	return b.convertOrder(items[0]), nil
}

func (b *BybitAdapter) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	query := map[string]string{
		"category": "linear",
		"symbol":   strings.ToUpper(symbol),
		"openOnly": "0",
		"limit":    "50",
	}

	orders := make([]*Order, 0)
	for {
		items, cursor, err := b.fetchOrders(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			orders = append(orders, b.convertOrder(item))
		}
		if cursor == "" {
			break
		}
		query["cursor"] = cursor
	}

	sort.SliceStable(orders, func(i, j int) bool {
		return orders[i].Price < orders[j].Price
	})
	return orders, nil
}

func (b *BybitAdapter) fetchOrders(ctx context.Context, query map[string]string) ([]bybitOrder, string, error) {
	resp, err := b.client.DoSignedRequest(ctx, "GET", "/v5/order/realtime", query, nil)
	if err != nil {
		return nil, "", err
	}

	var result struct {
		List           []bybitOrder `json:"list"`
		NextPageCursor string       `json:"nextPageCursor"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, "", fmt.Errorf("解析 Bybit 订单列表失败: %w", err)
	}
	return result.List, result.NextPageCursor, nil
}

func (b *BybitAdapter) GetAccount(ctx context.Context) (*Account, error) {
	resp, err := b.client.DoSignedRequest(ctx, "GET", "/v5/account/wallet-balance", map[string]string{
		"accountType": "UNIFIED",
	}, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		List []struct {
			TotalWalletBalance    string `json:"totalWalletBalance"`
			TotalMarginBalance    string `json:"totalMarginBalance"`
			TotalAvailableBalance string `json:"totalAvailableBalance"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("解析 Bybit 钱包信息失败: %w", err)
	}
	if len(result.List) == 0 {
		return nil, fmt.Errorf("Bybit 钱包信息为空")
	}

	positions, err := b.GetPositions(ctx, b.symbol)
	if err != nil {
		return nil, err
	}

	leverage := 1
	for _, position := range positions {
		if position.Leverage > 0 {
			leverage = position.Leverage
			break
		}
	}
	b.accountLeverage = leverage

	item := result.List[0]
	return &Account{
		TotalWalletBalance: parseFloat(item.TotalWalletBalance),
		TotalMarginBalance: parseFloat(item.TotalMarginBalance),
		AvailableBalance:   parseFloat(item.TotalAvailableBalance),
		Positions:          positions,
		AccountLeverage:    leverage,
	}, nil
}

func (b *BybitAdapter) GetPositions(ctx context.Context, symbol string) ([]*Position, error) {
	query := map[string]string{
		"category": "linear",
	}
	if symbol != "" {
		query["symbol"] = strings.ToUpper(symbol)
	} else if b.quoteAsset != "" {
		query["settleCoin"] = strings.ToUpper(b.quoteAsset)
	}

	resp, err := b.client.DoSignedRequest(ctx, "GET", "/v5/position/list", query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		List []struct {
			Symbol        string `json:"symbol"`
			Side          string `json:"side"`
			Size          string `json:"size"`
			AvgPrice      string `json:"avgPrice"`
			MarkPrice     string `json:"markPrice"`
			Leverage      string `json:"leverage"`
			TradeMode     int    `json:"tradeMode"`
			PositionIM    string `json:"positionIM"`
			UnrealisedPnl string `json:"unrealisedPnl"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("解析 Bybit 持仓失败: %w", err)
	}

	positions := make([]*Position, 0, len(result.List))
	for _, item := range result.List {
		size := parseFloat(item.Size)
		if strings.EqualFold(item.Side, "Sell") {
			size = -size
		}
		if size == 0 && symbol == "" {
			continue
		}

		marginType := "cross"
		if item.TradeMode == 1 {
			marginType = "isolated"
		}

		positions = append(positions, &Position{
			Symbol:         item.Symbol,
			Size:           size,
			EntryPrice:     parseFloat(item.AvgPrice),
			MarkPrice:      parseFloat(item.MarkPrice),
			UnrealizedPNL:  parseFloat(item.UnrealisedPnl),
			Leverage:       parseInt(item.Leverage),
			MarginType:     marginType,
			IsolatedMargin: parseFloat(item.PositionIM),
		})
	}

	return positions, nil
}

func (b *BybitAdapter) GetBalance(ctx context.Context, asset string) (float64, error) {
	resp, err := b.client.DoSignedRequest(ctx, "GET", "/v5/account/wallet-balance", map[string]string{
		"accountType": "UNIFIED",
		"coin":        strings.ToUpper(asset),
	}, nil)
	if err != nil {
		return 0, err
	}

	var result struct {
		List []struct {
			Coin []struct {
				Coin          string `json:"coin"`
				WalletBalance string `json:"walletBalance"`
				Equity        string `json:"equity"`
			} `json:"coin"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return 0, fmt.Errorf("解析 Bybit 余额失败: %w", err)
	}

	for _, wallet := range result.List {
		for _, coin := range wallet.Coin {
			if strings.EqualFold(coin.Coin, asset) {
				balance := parseFloat(coin.Equity)
				if balance == 0 {
					balance = parseFloat(coin.WalletBalance)
				}
				return balance, nil
			}
		}
	}

	return 0, nil
}

func (b *BybitAdapter) StartOrderStream(ctx context.Context, callback func(interface{})) error {
	return b.wsManager.Start(ctx, func(update OrderUpdate) {
		callback(update)
	})
}

func (b *BybitAdapter) StopOrderStream() error {
	b.wsManager.Stop()
	return nil
}

func (b *BybitAdapter) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	resp, err := b.client.DoPublicRequest(ctx, "GET", "/v5/market/tickers", map[string]string{
		"category": "linear",
		"symbol":   strings.ToUpper(symbol),
	})
	if err != nil {
		return 0, err
	}

	var result struct {
		List []struct {
			LastPrice string `json:"lastPrice"`
			MarkPrice string `json:"markPrice"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return 0, fmt.Errorf("解析 Bybit ticker 失败: %w", err)
	}
	if len(result.List) == 0 {
		return 0, fmt.Errorf("Bybit ticker 为空")
	}

	price := parseFloat(result.List[0].LastPrice)
	if price <= 0 {
		price = parseFloat(result.List[0].MarkPrice)
	}
	return price, nil
}

func (b *BybitAdapter) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	return b.wsManager.StartPriceStream(ctx, strings.ToUpper(symbol), callback)
}

func (b *BybitAdapter) StartKlineStream(ctx context.Context, symbols []string, interval string, callback func(candle interface{})) error {
	upperSymbols := make([]string, len(symbols))
	for i, symbol := range symbols {
		upperSymbols[i] = strings.ToUpper(symbol)
	}
	return b.klineWSManager.Start(ctx, upperSymbols, interval, callback)
}

func (b *BybitAdapter) StopKlineStream() error {
	b.klineWSManager.Stop()
	return nil
}

func (b *BybitAdapter) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*Candle, error) {
	bybitInterval, err := intervalToBybit(interval)
	if err != nil {
		return nil, err
	}

	resp, err := b.client.DoPublicRequest(ctx, "GET", "/v5/market/kline", map[string]string{
		"category": "linear",
		"symbol":   strings.ToUpper(symbol),
		"interval": bybitInterval,
		"limit":    strconv.Itoa(limit),
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		List [][]string `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("解析 Bybit 历史K线失败: %w", err)
	}

	candles := make([]*Candle, 0, len(result.List))
	for i := len(result.List) - 1; i >= 0; i-- {
		item := result.List[i]
		if len(item) < 6 {
			continue
		}
		candles = append(candles, &Candle{
			Symbol:    strings.ToUpper(symbol),
			Timestamp: parseInt64(item[0]),
			Open:      parseFloat(item[1]),
			High:      parseFloat(item[2]),
			Low:       parseFloat(item[3]),
			Close:     parseFloat(item[4]),
			Volume:    parseFloat(item[5]),
			IsClosed:  true,
		})
	}

	return candles, nil
}

func (b *BybitAdapter) GetPriceDecimals() int {
	return b.priceDecimals
}

func (b *BybitAdapter) GetQuantityDecimals() int {
	return b.quantityDecimals
}

func (b *BybitAdapter) GetBaseAsset() string {
	return b.baseAsset
}

func (b *BybitAdapter) GetQuoteAsset() string {
	return b.quoteAsset
}

type bybitOrder struct {
	OrderID     string `json:"orderId"`
	OrderLinkID string `json:"orderLinkId"`
	Symbol      string `json:"symbol"`
	Price       string `json:"price"`
	Qty         string `json:"qty"`
	Side        string `json:"side"`
	OrderStatus string `json:"orderStatus"`
	AvgPrice    string `json:"avgPrice"`
	CumExecQty  string `json:"cumExecQty"`
	OrderType   string `json:"orderType"`
	CreatedTime string `json:"createdTime"`
	UpdatedTime string `json:"updatedTime"`
}

func (b *BybitAdapter) convertOrder(item bybitOrder) *Order {
	return &Order{
		OrderID:       b.orderIDs.encode(item.OrderID),
		ClientOrderID: item.OrderLinkID,
		Symbol:        item.Symbol,
		Side:          mapSideFromBybit(item.Side),
		Type:          mapOrderTypeFromBybit(item.OrderType),
		Price:         parseFloat(item.Price),
		Quantity:      parseFloat(item.Qty),
		ExecutedQty:   parseFloat(item.CumExecQty),
		AvgPrice:      parseFloat(item.AvgPrice),
		Status:        mapStatusFromBybit(item.OrderStatus),
		CreatedAt:     time.UnixMilli(parseInt64(item.CreatedTime)),
		UpdateTime:    parseInt64(item.UpdatedTime),
	}
}

func decimalsFromStep(step string) int {
	if step == "" || !strings.Contains(step, ".") {
		return 0
	}
	trimmed := strings.TrimRight(step, "0")
	parts := strings.Split(trimmed, ".")
	if len(parts) != 2 {
		return 0
	}
	return len(parts[1])
}

func formatWithDecimals(value float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	return strconv.FormatFloat(value, 'f', decimals, 64)
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func sideToBybit(side Side) string {
	if side == SideSell {
		return "Sell"
	}
	return "Buy"
}

func mapSideFromBybit(side string) Side {
	if strings.EqualFold(side, "Sell") {
		return SideSell
	}
	return SideBuy
}

func orderTypeToBybit(orderType OrderType) string {
	if orderType == OrderTypeMarket {
		return "Market"
	}
	return "Limit"
}

func mapOrderTypeFromBybit(orderType string) OrderType {
	if strings.EqualFold(orderType, "Market") {
		return OrderTypeMarket
	}
	return OrderTypeLimit
}

func timeInForceToBybit(timeInForce TimeInForce, postOnly bool) string {
	if postOnly || timeInForce == TimeInForceGTX {
		return "PostOnly"
	}
	switch timeInForce {
	case TimeInForceIOC:
		return "IOC"
	case TimeInForceFOK:
		return "FOK"
	default:
		return "GTC"
	}
}

func mapStatusFromBybit(status string) OrderStatus {
	switch strings.ToLower(status) {
	case "new", "created", "untriggered":
		return OrderStatusNew
	case "partiallyfilled":
		return OrderStatusPartiallyFilled
	case "filled":
		return OrderStatusFilled
	case "cancelled", "deactivated", "partiallyfilledcanceled":
		return OrderStatusCanceled
	case "rejected":
		return OrderStatusRejected
	case "triggered":
		return OrderStatusNew
	default:
		return OrderStatusNew
	}
}

func intervalToBybit(interval string) (string, error) {
	switch interval {
	case "1m":
		return "1", nil
	case "3m":
		return "3", nil
	case "5m":
		return "5", nil
	case "15m":
		return "15", nil
	case "30m":
		return "30", nil
	case "1h":
		return "60", nil
	case "2h":
		return "120", nil
	case "4h":
		return "240", nil
	case "6h":
		return "360", nil
	case "12h":
		return "720", nil
	case "1d":
		return "D", nil
	case "1w":
		return "W", nil
	case "1month":
		return "M", nil
	default:
		return "", fmt.Errorf("不支持的 Bybit K线周期: %s", interval)
	}
}
