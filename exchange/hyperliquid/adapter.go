package hyperliquidex

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	hl "github.com/sonirico/go-hyperliquid"

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
	Symbol           string
	Size             float64
	EntryPrice       float64
	MarkPrice        float64
	UnrealizedPNL    float64
	HasUnrealizedPNL bool
	RealizedPNL      float64
	HasRealizedPNL   bool
	ClosedPNL        float64
	FundingFee       float64
	TradingFee       float64
	Leverage         int
	MarginType       string
	IsolatedMargin   float64
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

type HyperliquidAdapter struct {
	exchange         *hl.Exchange
	info             *hl.Info
	wsManager        *WebSocketManager
	klineWSManager   *KlineWebSocketManager
	address          string
	coin             string
	symbol           string
	priceDecimals    int
	quantityDecimals int
	accountLeverage  int
	marketType       string
	spotCoinToBase   map[string]string
	spotBaseToCoin   map[string]string

	cloidMu       sync.RWMutex
	clientToCloid map[string]string
	cloidToClient map[string]string
}

func NewHyperliquidAdapter(cfg map[string]string, symbol string) (*HyperliquidAdapter, error) {
	return newHyperliquidAdapter(cfg, symbol, "futures")
}

func NewHyperliquidSpotAdapter(cfg map[string]string, symbol string) (*HyperliquidAdapter, error) {
	return newHyperliquidAdapter(cfg, symbol, "spot")
}

func newHyperliquidAdapter(cfg map[string]string, symbol string, marketType string) (*HyperliquidAdapter, error) {
	address := strings.TrimSpace(cfg["api_key"])
	privateKeyHex := strings.TrimPrefix(strings.TrimSpace(cfg["secret_key"]), "0x")
	if address == "" || privateKeyHex == "" {
		return nil, fmt.Errorf("Hyperliquid API 配置不完整：API Key 填钱包地址，Secret Key 填 API 钱包私钥")
	}

	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("解析 Hyperliquid 私钥失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info := hl.NewInfo(ctx, hl.MainnetAPIURL, true, nil, nil, nil)
	meta, err := info.Meta(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 Hyperliquid 市场信息失败: %w", err)
	}
	spotMeta, err := info.SpotMeta(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 Hyperliquid 现货市场信息失败: %w", err)
	}

	adapter := &HyperliquidAdapter{
		exchange:         hl.NewExchange(ctx, privateKey, hl.MainnetAPIURL, meta, "", address, spotMeta, nil),
		info:             info,
		address:          address,
		priceDecimals:    2,
		quantityDecimals: 4,
		marketType:       marketType,
		spotCoinToBase:   make(map[string]string),
		spotBaseToCoin:   make(map[string]string),
		clientToCloid:    make(map[string]string),
		cloidToClient:    make(map[string]string),
	}
	adapter.loadCloidCache()
	adapter.applySpotMeta(spotMeta)
	if marketType == "spot" {
		adapter.coin = adapter.toHyperliquidSpotCoin(symbol)
		adapter.symbol = adapter.toDisplaySymbol(adapter.coin)
		adapter.applySpotPrecision(spotMeta)
	} else {
		adapter.coin = toHyperliquidCoin(symbol)
		adapter.symbol = toDisplaySymbol(adapter.coin)
	}
	adapter.wsManager = NewWebSocketManager(address, adapter.coin, adapter.decodeCloid)
	adapter.klineWSManager = NewKlineWebSocketManager()
	adapter.applyMeta(meta)
	return adapter, nil
}

func (h *HyperliquidAdapter) applySpotPrecision(meta *hl.SpotMeta) {
	if meta == nil {
		return
	}
	tokensByIndex := make(map[int]hl.SpotTokenInfo, len(meta.Tokens))
	for _, token := range meta.Tokens {
		tokensByIndex[token.Index] = token
	}
	for _, asset := range meta.Universe {
		if asset.Name != h.coin || len(asset.Tokens) == 0 {
			continue
		}
		if token, ok := tokensByIndex[asset.Tokens[0]]; ok {
			h.quantityDecimals = token.SzDecimals
			return
		}
	}
}

func (h *HyperliquidAdapter) applyMeta(meta *hl.Meta) {
	if meta == nil {
		return
	}
	for _, asset := range meta.Universe {
		if strings.EqualFold(asset.Name, h.coin) {
			h.quantityDecimals = asset.SzDecimals
			h.accountLeverage = asset.MaxLeverage
			return
		}
	}
}

func (h *HyperliquidAdapter) applySpotMeta(meta *hl.SpotMeta) {
	if meta == nil {
		return
	}
	tokensByIndex := make(map[int]hl.SpotTokenInfo, len(meta.Tokens))
	for _, token := range meta.Tokens {
		tokensByIndex[token.Index] = token
	}
	for _, asset := range meta.Universe {
		if len(asset.Tokens) < 2 {
			continue
		}
		baseToken, ok := tokensByIndex[asset.Tokens[0]]
		if !ok {
			continue
		}
		quoteToken, ok := tokensByIndex[asset.Tokens[1]]
		if !ok {
			continue
		}
		base := strings.ToUpper(baseToken.Name)
		quote := strings.ToUpper(quoteToken.Name)
		coin := asset.Name
		h.spotCoinToBase[coin] = base
		h.spotBaseToCoin[base+quote] = coin
		h.spotBaseToCoin[base] = coin
		if strings.EqualFold(coin, h.coin) {
			h.quantityDecimals = baseToken.SzDecimals
		}
	}
}

func (h *HyperliquidAdapter) GetName() string {
	return "Hyperliquid"
}

func (h *HyperliquidAdapter) ValidatePositionMode(ctx context.Context, direction string) error {
	if h.marketType != "spot" && strings.EqualFold(strings.TrimSpace(direction), "neutral") {
		return fmt.Errorf("Hyperliquid 适配器当前为净仓位模型，不支持中性双向持仓，已拒绝启动")
	}
	return nil
}

func (h *HyperliquidAdapter) PlaceOrder(ctx context.Context, req *OrderRequest) (*Order, error) {
	orderType := hl.OrderType{Limit: &hl.LimitOrderType{Tif: tifToHyperliquid(req.TimeInForce, req.PostOnly)}}
	clientOrderID := h.encodeCloid(req.ClientOrderID)

	status, err := h.exchange.Order(ctx, hl.CreateOrderRequest{
		Coin:          h.marketCoin(req.Symbol),
		IsBuy:         req.Side == SideBuy,
		Price:         req.Price,
		Size:          req.Quantity,
		ReduceOnly:    req.ReduceOnly,
		OrderType:     orderType,
		ClientOrderID: clientOrderID,
	}, nil)
	if err != nil {
		return nil, err
	}
	if status.Error != nil {
		return nil, fmt.Errorf("Hyperliquid 下单失败: %s", *status.Error)
	}

	now := time.Now()
	order := &Order{
		ClientOrderID: req.ClientOrderID,
		Symbol:        strings.ToUpper(req.Symbol),
		Side:          req.Side,
		Type:          req.Type,
		Price:         req.Price,
		Quantity:      req.Quantity,
		Status:        OrderStatusNew,
		CreatedAt:     now,
		UpdateTime:    now.UnixMilli(),
	}
	if status.Resting != nil {
		order.OrderID = status.Resting.Oid
		order.ClientOrderID = h.decodeCloid(status.Resting.ClientID)
	}
	if status.Filled != nil {
		order.OrderID = int64(status.Filled.Oid)
		order.ExecutedQty = parseFloat(status.Filled.TotalSz)
		order.AvgPrice = parseFloat(status.Filled.AvgPx)
		order.Status = OrderStatusFilled
	}
	return order, nil
}

func (h *HyperliquidAdapter) BatchPlaceOrders(ctx context.Context, orders []*OrderRequest) ([]*Order, bool) {
	placedOrders := make([]*Order, 0, len(orders))
	hasMarginError := false
	for _, orderReq := range orders {
		order, err := h.PlaceOrder(ctx, orderReq)
		if err != nil {
			logger.Warn("⚠️ [Hyperliquid] 下单失败 %.6f %s: %v", orderReq.Price, orderReq.Side, err)
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "margin") || strings.Contains(errMsg, "insufficient") || strings.Contains(errMsg, "balance") {
				hasMarginError = true
			}
			continue
		}
		placedOrders = append(placedOrders, order)
	}
	return placedOrders, hasMarginError
}

func (h *HyperliquidAdapter) CancelOrder(ctx context.Context, symbol string, orderID int64) error {
	resp, err := h.exchange.Cancel(ctx, h.marketCoin(symbol), orderID)
	if err != nil {
		if isHyperliquidCancelOrderGoneError(err) {
			logger.Info("ℹ️ [Hyperliquid] 订单 %d 已不存在或已完成，跳过取消", orderID)
			return nil
		}
		return err
	}
	if resp != nil && !resp.Ok {
		if isHyperliquidCancelOrderGoneMessage(resp.Err) {
			logger.Info("ℹ️ [Hyperliquid] 订单 %d 已不存在或已完成，跳过取消", orderID)
			return nil
		}
		return fmt.Errorf("Hyperliquid 撤单失败: %s", resp.Err)
	}
	return nil
}

func (h *HyperliquidAdapter) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	requests := make([]hl.CancelOrderRequest, 0, len(orderIDs))
	coin := h.marketCoin(symbol)
	for _, orderID := range orderIDs {
		requests = append(requests, hl.CancelOrderRequest{Coin: coin, OrderID: orderID})
	}
	resp, err := h.exchange.BulkCancel(ctx, requests)
	if err != nil {
		if isHyperliquidCancelOrderGoneError(err) {
			logger.Info("ℹ️ [Hyperliquid] 批量撤单目标已不存在或已完成，跳过取消: %v", orderIDs)
			return nil
		}
		return err
	}
	if resp != nil && !resp.Ok {
		if isHyperliquidCancelOrderGoneMessage(resp.Err) {
			logger.Info("ℹ️ [Hyperliquid] 批量撤单目标已不存在或已完成，跳过取消: %v", orderIDs)
			return nil
		}
		return fmt.Errorf("Hyperliquid 批量撤单失败: %s", resp.Err)
	}
	return nil
}

func (h *HyperliquidAdapter) CancelAllOrders(ctx context.Context, symbol string) error {
	openOrders, err := h.GetOpenOrders(ctx, symbol)
	if err != nil {
		return err
	}
	orderIDs := make([]int64, 0, len(openOrders))
	for _, order := range openOrders {
		orderIDs = append(orderIDs, order.OrderID)
	}
	if len(orderIDs) == 0 {
		return nil
	}
	return h.BatchCancelOrders(ctx, symbol, orderIDs)
}

func (h *HyperliquidAdapter) GetOrder(ctx context.Context, symbol string, orderID int64) (*Order, error) {
	result, err := h.info.QueryOrderByOid(ctx, h.address, orderID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("未找到 Hyperliquid 订单: %d", orderID)
	}
	q := result.Order.Order
	return &Order{
		OrderID:       q.Oid,
		ClientOrderID: h.decodeCloid(q.Cloid),
		Symbol:        h.toDisplaySymbol(q.Coin),
		Side:          mapSideFromHyperliquid(string(q.Side)),
		Type:          OrderTypeLimit,
		Price:         parseFloat(q.LimitPx),
		Quantity:      parseFloat(q.OrigSz),
		ExecutedQty:   math.Max(parseFloat(q.OrigSz)-parseFloat(q.Sz), 0),
		Status:        mapOrderStatusFromHyperliquid(result.Order.Status),
		CreatedAt:     time.UnixMilli(q.Timestamp),
		UpdateTime:    result.Order.StatusTimestamp,
	}, nil
}

func (h *HyperliquidAdapter) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	coin := h.marketCoin(symbol)
	openOrders, err := h.info.OpenOrders(ctx, h.address)
	if err != nil {
		return nil, err
	}
	orders := make([]*Order, 0, len(openOrders))
	for _, item := range openOrders {
		if !strings.EqualFold(item.Coin, coin) {
			continue
		}
		orders = append(orders, &Order{
			OrderID:       item.Oid,
			ClientOrderID: h.decodeCloid(item.Cloid),
			Symbol:        h.toDisplaySymbol(item.Coin),
			Side:          mapSideFromHyperliquid(item.Side),
			Type:          OrderTypeLimit,
			Price:         item.LimitPx,
			Quantity:      item.OrigSz,
			ExecutedQty:   math.Max(item.OrigSz-item.Size, 0),
			Status:        OrderStatusNew,
			CreatedAt:     time.UnixMilli(item.Timestamp),
			UpdateTime:    item.Timestamp,
		})
	}
	return orders, nil
}

func (h *HyperliquidAdapter) GetAccount(ctx context.Context) (*Account, error) {
	if h.marketType == "spot" {
		state, err := h.info.SpotUserState(ctx, h.address)
		if err != nil {
			return nil, err
		}
		quoteBalance := 0.0
		total := 0.0
		for _, balance := range state.Balances {
			amount := parseFloat(balance.Total)
			if strings.EqualFold(balance.Coin, "USDC") {
				quoteBalance = amount
				total += amount
				continue
			}
			coin := h.toHyperliquidSpotCoin(balance.Coin + "USDC")
			if coin == h.coin && amount > 0 {
				price, _ := h.GetLatestPrice(ctx, h.symbol)
				total += amount * price
			}
		}
		return &Account{
			TotalWalletBalance: total,
			TotalMarginBalance: total,
			AvailableBalance:   quoteBalance,
			Positions:          nil,
			AccountLeverage:    1,
		}, nil
	}
	state, err := h.info.UserState(ctx, h.address)
	if err != nil {
		return nil, err
	}
	positions := h.convertPositions(state.AssetPositions, "")
	accountValue := parseFloat(state.MarginSummary.AccountValue)
	return &Account{
		TotalWalletBalance: accountValue,
		TotalMarginBalance: accountValue,
		AvailableBalance:   parseFloat(state.Withdrawable),
		Positions:          positions,
		AccountLeverage:    h.accountLeverage,
	}, nil
}

func (h *HyperliquidAdapter) GetPositions(ctx context.Context, symbol string) ([]*Position, error) {
	if h.marketType == "spot" {
		// Spot balances may include assets unrelated to this bot. Runtime fills are tracked
		// locally through order updates, so startup recovery must not infer inventory here.
		return nil, nil
	}
	state, err := h.info.UserState(ctx, h.address)
	if err != nil {
		return nil, err
	}
	return h.convertPositions(state.AssetPositions, h.marketCoin(symbol)), nil
}

func (h *HyperliquidAdapter) convertPositions(assetPositions []hl.AssetPosition, coinFilter string) []*Position {
	positions := make([]*Position, 0, len(assetPositions))
	for _, item := range assetPositions {
		pos := item.Position
		if coinFilter != "" && !strings.EqualFold(pos.Coin, coinFilter) {
			continue
		}
		size := parseFloat(pos.Szi)
		if size == 0 && coinFilter == "" {
			continue
		}
		entryPrice := 0.0
		if pos.EntryPx != nil {
			entryPrice = parseFloat(*pos.EntryPx)
		}
		positions = append(positions, &Position{
			Symbol:           h.toDisplaySymbol(pos.Coin),
			Size:             size,
			EntryPrice:       entryPrice,
			UnrealizedPNL:    parseFloat(pos.UnrealizedPnl),
			HasUnrealizedPNL: true,
			Leverage:         pos.Leverage.Value,
			MarginType:       pos.Leverage.Type,
			IsolatedMargin:   parseFloat(pos.MarginUsed),
		})
	}
	return positions
}

func (h *HyperliquidAdapter) GetBalance(ctx context.Context, asset string) (float64, error) {
	if h.marketType == "spot" {
		state, err := h.info.SpotUserState(ctx, h.address)
		if err != nil {
			return 0, err
		}
		for _, balance := range state.Balances {
			if strings.EqualFold(balance.Coin, asset) {
				return parseFloat(balance.Total), nil
			}
		}
		return 0, nil
	}
	account, err := h.GetAccount(ctx)
	if err != nil {
		return 0, err
	}
	return account.TotalWalletBalance, nil
}

func (h *HyperliquidAdapter) StartOrderStream(ctx context.Context, callback func(interface{})) error {
	return h.wsManager.StartOrderStream(ctx, func(update OrderUpdate) {
		callback(update)
	})
}

func (h *HyperliquidAdapter) StopOrderStream() error {
	h.wsManager.StopOrderStream()
	return nil
}

func (h *HyperliquidAdapter) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	mids, err := h.info.AllMids(ctx)
	if err != nil {
		return 0, err
	}
	price := parseFloat(mids[h.marketCoin(symbol)])
	if price <= 0 {
		return 0, fmt.Errorf("Hyperliquid ticker 为空: %s", symbol)
	}
	return price, nil
}

func (h *HyperliquidAdapter) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	return h.wsManager.StartPriceStream(ctx, h.marketCoin(symbol), callback)
}

func (h *HyperliquidAdapter) StartKlineStream(ctx context.Context, symbols []string, interval string, callback func(candle interface{})) error {
	coins := make([]string, len(symbols))
	for i, symbol := range symbols {
		coins[i] = h.marketCoin(symbol)
	}
	return h.klineWSManager.Start(ctx, coins, interval, callback)
}

func (h *HyperliquidAdapter) StopKlineStream() error {
	h.klineWSManager.Stop()
	return nil
}

func (h *HyperliquidAdapter) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*Candle, error) {
	duration := intervalDuration(interval)
	end := time.Now()
	start := end.Add(-duration * time.Duration(limit+2))
	candles, err := h.info.CandlesSnapshot(ctx, h.marketCoin(symbol), interval, start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return nil, err
	}
	result := make([]*Candle, 0, len(candles))
	if len(candles) > limit {
		candles = candles[len(candles)-limit:]
	}
	for _, item := range candles {
		result = append(result, &Candle{
			Symbol:    h.toDisplaySymbol(item.Symbol),
			Open:      parseFloat(item.Open),
			High:      parseFloat(item.High),
			Low:       parseFloat(item.Low),
			Close:     parseFloat(item.Close),
			Volume:    parseFloat(item.Volume),
			Timestamp: item.TimeOpen,
			IsClosed:  true,
		})
	}
	return result, nil
}

func (h *HyperliquidAdapter) GetPriceDecimals() int {
	return h.priceDecimals
}

func (h *HyperliquidAdapter) GetQuantityDecimals() int {
	return h.quantityDecimals
}

func (h *HyperliquidAdapter) GetBaseAsset() string {
	if h.marketType == "spot" {
		if base := h.spotCoinToBase[h.coin]; base != "" {
			return base
		}
		if strings.Contains(h.coin, "/") {
			return strings.Split(h.coin, "/")[0]
		}
	}
	return h.coin
}

func (h *HyperliquidAdapter) GetQuoteAsset() string {
	return "USDC"
}

func (h *HyperliquidAdapter) encodeCloid(clientOrderID string) *string {
	if clientOrderID == "" {
		return nil
	}
	if cached, ok := h.lookupCloid(clientOrderID); ok {
		return &cached
	}
	sum := md5.Sum([]byte(clientOrderID))
	cloid := "0x" + hex.EncodeToString(sum[:])
	h.cloidMu.Lock()
	h.clientToCloid[clientOrderID] = cloid
	h.cloidToClient[cloid] = clientOrderID
	h.cloidMu.Unlock()
	h.saveCloidCache()
	return &cloid
}

func (h *HyperliquidAdapter) decodeCloid(cloid *string) string {
	if cloid == nil || *cloid == "" {
		return ""
	}
	h.cloidMu.RLock()
	defer h.cloidMu.RUnlock()
	if clientOrderID, ok := h.cloidToClient[*cloid]; ok {
		return clientOrderID
	}
	return *cloid
}

func (h *HyperliquidAdapter) lookupCloid(clientOrderID string) (string, bool) {
	h.cloidMu.RLock()
	defer h.cloidMu.RUnlock()
	cloid, ok := h.clientToCloid[clientOrderID]
	return cloid, ok
}

func (h *HyperliquidAdapter) cloidCachePath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		cacheDir = os.TempDir()
	}
	addressPart := strings.ToLower(strings.TrimPrefix(h.address, "0x"))
	if len(addressPart) > 12 {
		addressPart = addressPart[:12]
	}
	if addressPart == "" {
		addressPart = "default"
	}
	return filepath.Join(cacheDir, "nexus-trade-bot", "hyperliquid-cloids-"+addressPart+".json")
}

func (h *HyperliquidAdapter) loadCloidCache() {
	data, err := os.ReadFile(h.cloidCachePath())
	if err != nil {
		return
	}
	var cached map[string]string
	if err := json.Unmarshal(data, &cached); err != nil {
		return
	}
	h.cloidMu.Lock()
	defer h.cloidMu.Unlock()
	for cloid, clientOrderID := range cached {
		if cloid == "" || clientOrderID == "" {
			continue
		}
		h.cloidToClient[cloid] = clientOrderID
		h.clientToCloid[clientOrderID] = cloid
	}
}

func (h *HyperliquidAdapter) saveCloidCache() {
	h.cloidMu.RLock()
	cached := make(map[string]string, len(h.cloidToClient))
	for cloid, clientOrderID := range h.cloidToClient {
		cached[cloid] = clientOrderID
	}
	h.cloidMu.RUnlock()
	path := h.cloidCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		logger.Warn("⚠️ Hyperliquid cloid 缓存目录创建失败: %v", err)
		return
	}
	data, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		logger.Warn("⚠️ Hyperliquid cloid 缓存序列化失败: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		logger.Warn("⚠️ Hyperliquid cloid 缓存写入失败: %v", err)
	}
}

func toHyperliquidCoin(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	for _, quote := range []string{"USDT", "USDC", "USD"} {
		if strings.HasSuffix(symbol, quote) && len(symbol) > len(quote) {
			return symbol[:len(symbol)-len(quote)]
		}
	}
	return symbol
}

func (h *HyperliquidAdapter) marketCoin(symbol string) string {
	if h.marketType == "spot" {
		return h.toHyperliquidSpotCoin(symbol)
	}
	return toHyperliquidCoin(symbol)
}

func (h *HyperliquidAdapter) toHyperliquidSpotCoin(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if strings.Contains(symbol, "/") || strings.HasPrefix(symbol, "@") {
		return symbol
	}
	if coin := h.spotBaseToCoin[symbol]; coin != "" {
		return coin
	}
	base := toHyperliquidCoin(symbol)
	if coin := h.spotBaseToCoin[base]; coin != "" {
		return coin
	}
	return base + "/USDC"
}

func toDisplaySymbol(coin string) string {
	if coin == "" {
		return ""
	}
	return strings.ToUpper(coin) + "USDC"
}

func (h *HyperliquidAdapter) toDisplaySymbol(coin string) string {
	if h.marketType == "spot" {
		if base := h.spotCoinToBase[coin]; base != "" {
			return base + "USDC"
		}
		if strings.Contains(coin, "/") {
			return strings.ReplaceAll(strings.ToUpper(coin), "/", "")
		}
	}
	return toDisplaySymbol(coin)
}

func tifToHyperliquid(timeInForce TimeInForce, postOnly bool) hl.Tif {
	if postOnly || timeInForce == TimeInForceGTX {
		return hl.TifAlo
	}
	switch timeInForce {
	case TimeInForceIOC:
		return hl.TifIoc
	default:
		return hl.TifGtc
	}
}

func mapSideFromHyperliquid(side string) Side {
	switch strings.ToUpper(side) {
	case "A", "ASK", "SELL":
		return SideSell
	default:
		return SideBuy
	}
}

func mapOrderStatusFromHyperliquid(status hl.OrderStatusValue) OrderStatus {
	switch status {
	case hl.OrderStatusValueFilled:
		return OrderStatusFilled
	case hl.OrderStatusValueOpen:
		return OrderStatusNew
	case hl.OrderStatusValueCanceled, hl.OrderStatusValueMarginCanceled, hl.OrderStatusValueReduceOnlyCanceled:
		return OrderStatusCanceled
	default:
		if strings.Contains(strings.ToLower(string(status)), "rejected") {
			return OrderStatusRejected
		}
		return OrderStatusNew
	}
}

func isHyperliquidCancelOrderGoneError(err error) bool {
	if err == nil {
		return false
	}
	return isHyperliquidCancelOrderGoneMessage(err.Error())
}

func isHyperliquidCancelOrderGoneMessage(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	return strings.Contains(lower, "never placed") ||
		strings.Contains(lower, "already canceled") ||
		strings.Contains(lower, "already cancelled") ||
		strings.Contains(lower, "or filled") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "not found")
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func intervalDuration(interval string) time.Duration {
	switch interval {
	case "1m":
		return time.Minute
	case "3m":
		return 3 * time.Minute
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "30m":
		return 30 * time.Minute
	case "1h":
		return time.Hour
	case "4h":
		return 4 * time.Hour
	case "1d":
		return 24 * time.Hour
	default:
		return time.Minute
	}
}
