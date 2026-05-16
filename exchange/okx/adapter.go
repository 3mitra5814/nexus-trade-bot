package okx

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexus-trade-bot/logger"
)

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeMarket OrderType = "MARKET"
)

type OrderStatus string

const (
	OrderStatusNew             OrderStatus = "NEW"
	OrderStatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	OrderStatusFilled          OrderStatus = "FILLED"
	OrderStatusCanceled        OrderStatus = "CANCELED"
	OrderStatusRejected        OrderStatus = "REJECTED"
	OrderStatusExpired         OrderStatus = "EXPIRED"
)

type TimeInForce string

const (
	TimeInForceGTC TimeInForce = "GTC"
	TimeInForceIOC TimeInForce = "IOC"
	TimeInForceFOK TimeInForce = "FOK"
	TimeInForceGTX TimeInForce = "GTX"
)

type OrderRequest struct {
	Symbol        string
	Side          Side
	Type          OrderType
	TimeInForce   TimeInForce
	Quantity      float64
	Price         float64
	ReduceOnly    bool
	PostOnly      bool
	PriceDecimals int
	ClientOrderID string
}

type Order struct {
	OrderID       int64
	ClientOrderID string
	Symbol        string
	Side          Side
	Type          OrderType
	Price         float64
	Quantity      float64
	ExecutedQty   float64
	AvgPrice      float64
	Status        OrderStatus
	CreatedAt     time.Time
	UpdateTime    int64
}

type Position struct {
	Symbol         string
	Size           float64
	EntryPrice     float64
	MarkPrice      float64
	UnrealizedPNL  float64
	Leverage       int
	MarginType     string
	IsolatedMargin float64
}

type Account struct {
	TotalWalletBalance float64
	TotalMarginBalance float64
	AvailableBalance   float64
	Positions          []*Position
	AccountLeverage    int
}

type OrderUpdate struct {
	OrderID       int64
	ClientOrderID string
	Symbol        string
	Side          Side
	Type          OrderType
	Status        OrderStatus
	Price         float64
	Quantity      float64
	ExecutedQty   float64
	AvgPrice      float64
	UpdateTime    int64
}

type OrderUpdateCallback func(update OrderUpdate)

type Candle struct {
	Symbol    string
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Timestamp int64
	IsClosed  bool
}

type OKXAdapter struct {
	client           *Client
	symbol           string
	instID           string
	wsManager        *WebSocketManager
	klineWSManager   *KlineWebSocketManager
	orderIDs         *orderIDMapper
	priceDecimals    int
	quantityDecimals int
	baseAsset        string
	quoteAsset       string
	contractValue    float64
	lotSize          float64
	accountLeverage  int
	mu               sync.RWMutex
	posMode          string
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

func NewOKXAdapter(cfg map[string]string, symbol string) (*OKXAdapter, error) {
	apiKey := cfg["api_key"]
	secretKey := cfg["secret_key"]
	passphrase := cfg["passphrase"]
	if apiKey == "" || secretKey == "" || passphrase == "" {
		return nil, fmt.Errorf("OKX API 配置不完整")
	}

	orderIDs := newOrderIDMapper()
	adapter := &OKXAdapter{
		client:           NewClient(apiKey, secretKey, passphrase),
		symbol:           strings.ToUpper(symbol),
		instID:           toOKXInstrument(symbol),
		orderIDs:         orderIDs,
		klineWSManager:   NewKlineWebSocketManager(),
		priceDecimals:    2,
		quantityDecimals: 4,
		contractValue:    1,
		lotSize:          1,
	}
	adapter.wsManager = NewWebSocketManager(adapter.client, adapter.instID, adapter.symbol, orderIDs, adapter.baseQuantity)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := adapter.fetchExchangeInfo(ctx); err != nil {
		logger.Warn("⚠️ [OKX] 获取合约信息失败: %v，使用默认精度", err)
	}

	return adapter, nil
}

func (o *OKXAdapter) GetName() string {
	return "OKX"
}

func (o *OKXAdapter) fetchExchangeInfo(ctx context.Context) error {
	resp, err := o.client.DoPublicRequest(ctx, http.MethodGet, "/api/v5/public/instruments", map[string]string{
		"instType": "SWAP",
		"instId":   o.instID,
	})
	if err != nil {
		return err
	}

	var items []struct {
		InstID   string `json:"instId"`
		BaseCcy  string `json:"baseCcy"`
		QuoteCcy string `json:"quoteCcy"`
		TickSz   string `json:"tickSz"`
		LotSz    string `json:"lotSz"`
		MinSz    string `json:"minSz"`
		CtVal    string `json:"ctVal"`
	}
	if err := json.Unmarshal(resp.Data, &items); err != nil {
		return fmt.Errorf("解析 OKX 合约信息失败: %w", err)
	}
	if len(items) == 0 {
		return fmt.Errorf("未找到 OKX 合约信息: %s", o.instID)
	}

	item := items[0]
	o.baseAsset = item.BaseCcy
	o.quoteAsset = item.QuoteCcy
	o.priceDecimals = decimalsFromStep(item.TickSz)
	o.lotSize = parseFloat(item.LotSz)
	o.contractValue = parseFloat(item.CtVal)
	if o.contractValue <= 0 {
		o.contractValue = 1
	}
	if o.lotSize <= 0 {
		o.lotSize = 1
	}
	o.quantityDecimals = decimalsFromStep(formatFloat(o.lotSize * o.contractValue))
	if o.quantityDecimals <= 0 {
		o.quantityDecimals = 4
	}

	logger.Debug("ℹ️ [OKX 合约信息] %s - 数量精度:%d, 价格精度:%d, 合约面值:%s, 基础币种:%s, 计价币种:%s",
		o.instID, o.quantityDecimals, o.priceDecimals, item.CtVal, o.baseAsset, o.quoteAsset)
	return nil
}

func (o *OKXAdapter) PlaceOrder(ctx context.Context, req *OrderRequest) (*Order, error) {
	priceDecimals := req.PriceDecimals
	if priceDecimals <= 0 {
		priceDecimals = o.priceDecimals
	}

	body := map[string]any{
		"instId":  o.instID,
		"tdMode":  "cross",
		"side":    sideToOKX(req.Side),
		"ordType": orderTypeToOKX(req.Type, req.TimeInForce, req.PostOnly),
		"sz":      o.contractSize(req.Quantity),
	}
	if req.Type == OrderTypeLimit {
		body["px"] = formatWithDecimals(req.Price, priceDecimals)
	}
	if req.ReduceOnly {
		body["reduceOnly"] = "true"
	}
	if req.ClientOrderID != "" {
		body["clOrdId"] = encodeClientOrderID(req.ClientOrderID)
	}
	if o.isLongShortMode() {
		body["posSide"] = posSideForOKXOrder(req.Side, req.ReduceOnly)
	}

	resp, err := o.client.DoSignedRequest(ctx, http.MethodPost, "/api/v5/trade/order", nil, body)
	if err != nil {
		return nil, err
	}

	var result []struct {
		OrdID   string `json:"ordId"`
		ClOrdID string `json:"clOrdId"`
		SCode   string `json:"sCode"`
		SMsg    string `json:"sMsg"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("解析 OKX 下单响应失败: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("OKX 下单响应为空")
	}
	if result[0].SCode != "" && result[0].SCode != "0" {
		return nil, fmt.Errorf("OKX 下单失败: code=%s, msg=%s", result[0].SCode, result[0].SMsg)
	}

	clientOrderID := decodeClientOrderID(result[0].ClOrdID)
	if clientOrderID == "" {
		clientOrderID = req.ClientOrderID
	}
	return &Order{
		OrderID:       o.orderIDs.encode(result[0].OrdID),
		ClientOrderID: clientOrderID,
		Symbol:        o.symbol,
		Side:          req.Side,
		Type:          req.Type,
		Price:         req.Price,
		Quantity:      req.Quantity,
		Status:        OrderStatusNew,
		CreatedAt:     time.Now(),
		UpdateTime:    time.Now().UnixMilli(),
	}, nil
}

func (o *OKXAdapter) BatchPlaceOrders(ctx context.Context, orders []*OrderRequest) ([]*Order, bool) {
	placedOrders := make([]*Order, 0, len(orders))
	hasMarginError := false
	for _, orderReq := range orders {
		order, err := o.PlaceOrder(ctx, orderReq)
		if err != nil {
			logger.Warn("⚠️ [OKX] 下单失败 %.6f %s: %v", orderReq.Price, orderReq.Side, err)
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "insufficient") || strings.Contains(errMsg, "margin") || strings.Contains(errMsg, "balance") {
				hasMarginError = true
			}
			continue
		}
		placedOrders = append(placedOrders, order)
	}
	return placedOrders, hasMarginError
}

func (o *OKXAdapter) CancelOrder(ctx context.Context, symbol string, orderID int64) error {
	body := map[string]any{"instId": toOKXInstrument(symbol)}
	if remoteOrderID, ok := o.orderIDs.lookup(orderID); ok {
		body["ordId"] = remoteOrderID
	} else {
		body["ordId"] = strconv.FormatInt(orderID, 10)
	}

	resp, err := o.client.DoSignedRequest(ctx, http.MethodPost, "/api/v5/trade/cancel-order", nil, body)
	if err != nil {
		return err
	}
	var result []struct {
		SCode string `json:"sCode"`
		SMsg  string `json:"sMsg"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return fmt.Errorf("解析 OKX 撤单响应失败: %w", err)
	}
	if len(result) > 0 && result[0].SCode != "" && result[0].SCode != "0" {
		return fmt.Errorf("OKX 撤单失败: code=%s, msg=%s", result[0].SCode, result[0].SMsg)
	}
	return nil
}

func (o *OKXAdapter) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	for _, orderID := range orderIDs {
		if err := o.CancelOrder(ctx, symbol, orderID); err != nil {
			return err
		}
	}
	return nil
}

func (o *OKXAdapter) CancelAllOrders(ctx context.Context, symbol string) error {
	orders, err := o.GetOpenOrders(ctx, symbol)
	if err != nil {
		return err
	}
	for _, order := range orders {
		if err := o.CancelOrder(ctx, symbol, order.OrderID); err != nil {
			return err
		}
	}
	return nil
}

func (o *OKXAdapter) GetOrder(ctx context.Context, symbol string, orderID int64) (*Order, error) {
	query := map[string]string{"instId": toOKXInstrument(symbol)}
	if remoteOrderID, ok := o.orderIDs.lookup(orderID); ok {
		query["ordId"] = remoteOrderID
	} else {
		query["ordId"] = strconv.FormatInt(orderID, 10)
	}

	resp, err := o.client.DoSignedRequest(ctx, http.MethodGet, "/api/v5/trade/order", query, nil)
	if err != nil {
		return nil, err
	}
	items, err := o.parseOrders(resp.Data)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("未找到 OKX 订单: %d", orderID)
	}
	return o.convertOrder(items[0]), nil
}

func (o *OKXAdapter) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	resp, err := o.client.DoSignedRequest(ctx, http.MethodGet, "/api/v5/trade/orders-pending", map[string]string{
		"instType": "SWAP",
		"instId":   toOKXInstrument(symbol),
	}, nil)
	if err != nil {
		return nil, err
	}

	items, err := o.parseOrders(resp.Data)
	if err != nil {
		return nil, err
	}
	orders := make([]*Order, 0, len(items))
	for _, item := range items {
		orders = append(orders, o.convertOrder(item))
	}
	sort.SliceStable(orders, func(i, j int) bool {
		return orders[i].Price < orders[j].Price
	})
	return orders, nil
}

func (o *OKXAdapter) GetAccount(ctx context.Context) (*Account, error) {
	resp, err := o.client.DoSignedRequest(ctx, http.MethodGet, "/api/v5/account/balance", nil, nil)
	if err != nil {
		return nil, err
	}

	var result []struct {
		TotalEq string `json:"totalEq"`
		AdjEq   string `json:"adjEq"`
		Details []struct {
			Ccy      string `json:"ccy"`
			Eq       string `json:"eq"`
			CashBal  string `json:"cashBal"`
			AvailBal string `json:"availBal"`
			AvailEq  string `json:"availEq"`
		} `json:"details"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("解析 OKX 账户信息失败: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("OKX 账户信息为空")
	}

	positions, err := o.GetPositions(ctx, o.symbol)
	if err != nil {
		return nil, err
	}

	available := 0.0
	for _, detail := range result[0].Details {
		if strings.EqualFold(detail.Ccy, o.quoteAsset) || (o.quoteAsset == "" && strings.EqualFold(detail.Ccy, "USDT")) {
			available = parseFloat(detail.AvailEq)
			if available == 0 {
				available = parseFloat(detail.AvailBal)
			}
			if available == 0 {
				available = parseFloat(detail.CashBal)
			}
			break
		}
	}

	leverage := 1
	for _, position := range positions {
		if position.Leverage > 0 {
			leverage = position.Leverage
			break
		}
	}
	o.accountLeverage = leverage

	total := parseFloat(result[0].TotalEq)
	if total == 0 {
		total = parseFloat(result[0].AdjEq)
	}
	return &Account{
		TotalWalletBalance: total,
		TotalMarginBalance: total,
		AvailableBalance:   available,
		Positions:          positions,
		AccountLeverage:    leverage,
	}, nil
}

func (o *OKXAdapter) GetPositions(ctx context.Context, symbol string) ([]*Position, error) {
	query := map[string]string{"instType": "SWAP"}
	if symbol != "" {
		query["instId"] = toOKXInstrument(symbol)
	}
	resp, err := o.client.DoSignedRequest(ctx, http.MethodGet, "/api/v5/account/positions", query, nil)
	if err != nil {
		return nil, err
	}

	var result []struct {
		InstID  string `json:"instId"`
		Pos     string `json:"pos"`
		PosSide string `json:"posSide"`
		AvgPx   string `json:"avgPx"`
		MarkPx  string `json:"markPx"`
		Upl     string `json:"upl"`
		Lever   string `json:"lever"`
		MgnMode string `json:"mgnMode"`
		Imr     string `json:"imr"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("解析 OKX 持仓失败: %w", err)
	}

	positions := make([]*Position, 0, len(result))
	for _, item := range result {
		size := o.baseQuantity(parseFloat(item.Pos))
		if strings.EqualFold(item.PosSide, "short") {
			size = -math.Abs(size)
		}
		if size == 0 && symbol == "" {
			continue
		}
		positions = append(positions, &Position{
			Symbol:         fromOKXInstrument(item.InstID),
			Size:           size,
			EntryPrice:     parseFloat(item.AvgPx),
			MarkPrice:      parseFloat(item.MarkPx),
			UnrealizedPNL:  parseFloat(item.Upl),
			Leverage:       parseInt(item.Lever),
			MarginType:     item.MgnMode,
			IsolatedMargin: parseFloat(item.Imr),
		})
	}
	return positions, nil
}

func (o *OKXAdapter) ValidatePositionMode(ctx context.Context, direction string) error {
	posMode, err := o.fetchPositionMode(ctx)
	if err != nil {
		return err
	}
	o.setPositionMode(posMode)
	if strings.EqualFold(strings.TrimSpace(direction), "neutral") && posMode != "long_short_mode" {
		return fmt.Errorf("OKX 中性模式需要账户持仓模式为 long_short_mode，当前为 %s，请切换双向持仓后再启动", posMode)
	}
	return nil
}

func (o *OKXAdapter) fetchPositionMode(ctx context.Context) (string, error) {
	resp, err := o.client.DoSignedRequest(ctx, http.MethodGet, "/api/v5/account/config", nil, nil)
	if err != nil {
		return "", fmt.Errorf("获取 OKX 账户持仓模式失败: %w", err)
	}
	var result []struct {
		PosMode string `json:"posMode"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return "", fmt.Errorf("解析 OKX 账户持仓模式失败: %w", err)
	}
	if len(result) == 0 || strings.TrimSpace(result[0].PosMode) == "" {
		return "", fmt.Errorf("OKX 账户持仓模式为空")
	}
	return strings.TrimSpace(result[0].PosMode), nil
}

func (o *OKXAdapter) setPositionMode(posMode string) {
	o.mu.Lock()
	o.posMode = strings.TrimSpace(posMode)
	o.mu.Unlock()
}

func (o *OKXAdapter) isLongShortMode() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.posMode == "long_short_mode"
}

func (o *OKXAdapter) GetBalance(ctx context.Context, asset string) (float64, error) {
	resp, err := o.client.DoSignedRequest(ctx, http.MethodGet, "/api/v5/account/balance", map[string]string{
		"ccy": strings.ToUpper(asset),
	}, nil)
	if err != nil {
		return 0, err
	}

	var result []struct {
		Details []struct {
			Ccy     string `json:"ccy"`
			Eq      string `json:"eq"`
			CashBal string `json:"cashBal"`
		} `json:"details"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return 0, fmt.Errorf("解析 OKX 余额失败: %w", err)
	}
	for _, wallet := range result {
		for _, detail := range wallet.Details {
			if strings.EqualFold(detail.Ccy, asset) {
				balance := parseFloat(detail.Eq)
				if balance == 0 {
					balance = parseFloat(detail.CashBal)
				}
				return balance, nil
			}
		}
	}
	return 0, nil
}

func (o *OKXAdapter) StartOrderStream(ctx context.Context, callback func(interface{})) error {
	return o.wsManager.Start(ctx, func(update OrderUpdate) {
		callback(update)
	})
}

func (o *OKXAdapter) StopOrderStream() error {
	o.wsManager.Stop()
	return nil
}

func (o *OKXAdapter) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	resp, err := o.client.DoPublicRequest(ctx, http.MethodGet, "/api/v5/market/ticker", map[string]string{
		"instId": toOKXInstrument(symbol),
	})
	if err != nil {
		return 0, err
	}

	var result []struct {
		Last string `json:"last"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return 0, fmt.Errorf("解析 OKX ticker 失败: %w", err)
	}
	if len(result) == 0 {
		return 0, fmt.Errorf("OKX ticker 为空")
	}
	return parseFloat(result[0].Last), nil
}

func (o *OKXAdapter) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	return o.wsManager.StartPriceStream(ctx, toOKXInstrument(symbol), callback)
}

func (o *OKXAdapter) StartKlineStream(ctx context.Context, symbols []string, interval string, callback func(candle interface{})) error {
	instIDs := make([]string, len(symbols))
	for i, symbol := range symbols {
		instIDs[i] = toOKXInstrument(symbol)
	}
	return o.klineWSManager.Start(ctx, instIDs, interval, callback)
}

func (o *OKXAdapter) StopKlineStream() error {
	o.klineWSManager.Stop()
	return nil
}

func (o *OKXAdapter) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*Candle, error) {
	resp, err := o.client.DoPublicRequest(ctx, http.MethodGet, "/api/v5/market/candles", map[string]string{
		"instId": toOKXInstrument(symbol),
		"bar":    intervalToOKX(interval),
		"limit":  strconv.Itoa(limit),
	})
	if err != nil {
		return nil, err
	}

	var result [][]string
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("解析 OKX 历史K线失败: %w", err)
	}

	candles := make([]*Candle, 0, len(result))
	for i := len(result) - 1; i >= 0; i-- {
		item := result[i]
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

func (o *OKXAdapter) GetPriceDecimals() int {
	return o.priceDecimals
}

func (o *OKXAdapter) GetQuantityDecimals() int {
	return o.quantityDecimals
}

func (o *OKXAdapter) GetBaseAsset() string {
	return o.baseAsset
}

func (o *OKXAdapter) GetQuoteAsset() string {
	return o.quoteAsset
}

type okxOrder struct {
	OrdID     string `json:"ordId"`
	ClOrdID   string `json:"clOrdId"`
	InstID    string `json:"instId"`
	Px        string `json:"px"`
	Sz        string `json:"sz"`
	Side      string `json:"side"`
	OrdType   string `json:"ordType"`
	State     string `json:"state"`
	AvgPx     string `json:"avgPx"`
	AccFillSz string `json:"accFillSz"`
	CTime     string `json:"cTime"`
	UTime     string `json:"uTime"`
}

func (o *OKXAdapter) parseOrders(data json.RawMessage) ([]okxOrder, error) {
	var items []okxOrder
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("解析 OKX 订单列表失败: %w", err)
	}
	return items, nil
}

func (o *OKXAdapter) convertOrder(item okxOrder) *Order {
	return &Order{
		OrderID:       o.orderIDs.encode(item.OrdID),
		ClientOrderID: decodeClientOrderID(item.ClOrdID),
		Symbol:        fromOKXInstrument(item.InstID),
		Side:          mapSideFromOKX(item.Side),
		Type:          mapOrderTypeFromOKX(item.OrdType),
		Price:         parseFloat(item.Px),
		Quantity:      o.baseQuantity(parseFloat(item.Sz)),
		ExecutedQty:   o.baseQuantity(parseFloat(item.AccFillSz)),
		AvgPrice:      parseFloat(item.AvgPx),
		Status:        mapStatusFromOKX(item.State),
		CreatedAt:     time.UnixMilli(parseInt64(item.CTime)),
		UpdateTime:    parseInt64(item.UTime),
	}
}

func (o *OKXAdapter) contractSize(baseQuantity float64) string {
	if o.contractValue <= 0 {
		return formatWithDecimals(baseQuantity, o.quantityDecimals)
	}
	contracts := baseQuantity / o.contractValue
	if o.lotSize > 0 {
		contracts = math.Floor(contracts/o.lotSize) * o.lotSize
	}
	if contracts <= 0 && baseQuantity > 0 {
		contracts = o.lotSize
	}
	return formatFloat(contracts)
}

func (o *OKXAdapter) baseQuantity(contracts float64) float64 {
	return contracts * o.contractValue
}

func toOKXInstrument(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if strings.Contains(symbol, "-") {
		if strings.HasSuffix(symbol, "-SWAP") {
			return symbol
		}
		return symbol + "-SWAP"
	}
	for _, quote := range []string{"USDT", "USDC", "USD"} {
		if strings.HasSuffix(symbol, quote) && len(symbol) > len(quote) {
			return symbol[:len(symbol)-len(quote)] + "-" + quote + "-SWAP"
		}
	}
	return symbol
}

func fromOKXInstrument(instID string) string {
	parts := strings.Split(strings.ToUpper(instID), "-")
	if len(parts) >= 2 {
		return parts[0] + parts[1]
	}
	return strings.ToUpper(instID)
}

func encodeClientOrderID(clientOrderID string) string {
	if clientOrderID == "" {
		return ""
	}
	encoded := strings.ReplaceAll(clientOrderID, "_", "X")
	if len(encoded) > 32 {
		encoded = encoded[:32]
	}
	return encoded
}

func decodeClientOrderID(clientOrderID string) string {
	if strings.HasPrefix(clientOrderID, "O") && len(clientOrderID) > 1 {
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clientOrderID[1:])
		if err == nil {
			return string(decoded)
		}
	}
	if strings.Contains(clientOrderID, "X") {
		return strings.ReplaceAll(clientOrderID, "X", "_")
	}
	return clientOrderID
}

func decimalsFromStep(step string) int {
	step = strings.TrimSpace(step)
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

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
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

func sideToOKX(side Side) string {
	if side == SideSell {
		return "sell"
	}
	return "buy"
}

func posSideForOKXOrder(side Side, reduceOnly bool) string {
	if (side == SideBuy && !reduceOnly) || (side == SideSell && reduceOnly) {
		return "long"
	}
	return "short"
}

func mapSideFromOKX(side string) Side {
	if strings.EqualFold(side, "sell") {
		return SideSell
	}
	return SideBuy
}

func orderTypeToOKX(orderType OrderType, timeInForce TimeInForce, postOnly bool) string {
	if orderType == OrderTypeMarket {
		return "market"
	}
	if postOnly || timeInForce == TimeInForceGTX {
		return "post_only"
	}
	switch timeInForce {
	case TimeInForceIOC:
		return "ioc"
	case TimeInForceFOK:
		return "fok"
	default:
		return "limit"
	}
}

func mapOrderTypeFromOKX(orderType string) OrderType {
	if strings.EqualFold(orderType, "market") {
		return OrderTypeMarket
	}
	return OrderTypeLimit
}

func mapStatusFromOKX(status string) OrderStatus {
	switch strings.ToLower(status) {
	case "live":
		return OrderStatusNew
	case "partially_filled":
		return OrderStatusPartiallyFilled
	case "filled":
		return OrderStatusFilled
	case "canceled":
		return OrderStatusCanceled
	default:
		return OrderStatusNew
	}
}

func intervalToOKX(interval string) string {
	switch interval {
	case "1m", "3m", "5m", "15m", "30m", "1H", "2H", "4H", "6H", "12H", "1D", "1W":
		return interval
	case "1h":
		return "1H"
	case "2h":
		return "2H"
	case "4h":
		return "4H"
	case "6h":
		return "6H"
	case "12h":
		return "12H"
	case "1d":
		return "1D"
	case "1w":
		return "1W"
	default:
		return interval
	}
}
