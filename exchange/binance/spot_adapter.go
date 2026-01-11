package binance

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexus-trade-bot/logger"
	"nexus-trade-bot/utils"

	spot "github.com/adshao/go-binance/v2"
)

// BinanceSpotAdapter 币安现货交易适配器
type BinanceSpotAdapter struct {
	client           *spot.Client
	symbol           string
	priceDecimals    int
	quantityDecimals int
	baseAsset        string
	quoteAsset       string

	mu             sync.Mutex
	priceStopC     chan struct{}
	klineStopC     chan struct{}
	orderStopC     chan struct{}
	orderListenKey string
	latestPrice    float64
}

// NewBinanceSpotAdapter 创建币安现货适配器
func NewBinanceSpotAdapter(cfg map[string]string, symbol string) (*BinanceSpotAdapter, error) {
	apiKey := cfg["api_key"]
	secretKey := cfg["secret_key"]
	if apiKey == "" || secretKey == "" {
		return nil, fmt.Errorf("Binance API 配置不完整")
	}

	adapter := &BinanceSpotAdapter{
		client:           spot.NewClient(apiKey, secretKey),
		symbol:           strings.ToUpper(symbol),
		priceDecimals:    2,
		quantityDecimals: 6,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := adapter.fetchExchangeInfo(ctx); err != nil {
		logger.Warn("⚠️ [Binance Spot] 获取现货交易对信息失败: %v，使用默认精度", err)
	}
	return adapter, nil
}

func (b *BinanceSpotAdapter) GetName() string {
	return "Binance Spot"
}

func (b *BinanceSpotAdapter) fetchExchangeInfo(ctx context.Context) error {
	info, err := b.client.NewExchangeInfoService().Symbol(b.symbol).Do(ctx)
	if err != nil {
		return fmt.Errorf("获取现货交易所信息失败: %w", err)
	}
	for _, item := range info.Symbols {
		if item.Symbol != b.symbol {
			continue
		}
		b.baseAsset = item.BaseAsset
		b.quoteAsset = item.QuoteAsset
		b.priceDecimals = item.QuoteAssetPrecision
		b.quantityDecimals = item.BaseAssetPrecision
		for _, filter := range item.Filters {
			filterType, _ := filter["filterType"].(string)
			if filterType == "PRICE_FILTER" {
				if tick, _ := filter["tickSize"].(string); tick != "" {
					b.priceDecimals = decimalsFromStep(tick)
				}
			}
			if filterType == "LOT_SIZE" {
				if step, _ := filter["stepSize"].(string); step != "" {
					b.quantityDecimals = decimalsFromStep(step)
				}
			}
		}
		logger.Debug("ℹ️ [Binance Spot 信息] %s - 数量精度:%d, 价格精度:%d, 基础币种:%s, 计价币种:%s",
			b.symbol, b.quantityDecimals, b.priceDecimals, b.baseAsset, b.quoteAsset)
		return nil
	}
	return fmt.Errorf("未找到现货交易对: %s", b.symbol)
}

func decimalsFromStep(step string) int {
	step = strings.TrimRight(strings.TrimSpace(step), "0")
	if step == "" || !strings.Contains(step, ".") {
		return 0
	}
	return len(step) - strings.Index(step, ".") - 1
}

func (b *BinanceSpotAdapter) PlaceOrder(ctx context.Context, req *OrderRequest) (*Order, error) {
	priceDecimals := req.PriceDecimals
	if priceDecimals <= 0 {
		priceDecimals = b.priceDecimals
	}
	priceStr := fmt.Sprintf("%.*f", priceDecimals, req.Price)
	quantityStr := fmt.Sprintf("%.*f", b.quantityDecimals, req.Quantity)

	orderType := spot.OrderTypeLimit
	orderService := b.client.NewCreateOrderService().
		Symbol(req.Symbol).
		Side(spot.SideType(req.Side)).
		Quantity(quantityStr).
		Price(priceStr)
	if req.PostOnly {
		orderType = spot.OrderTypeLimitMaker
	} else {
		orderService = orderService.TimeInForce(spot.TimeInForceTypeGTC)
	}
	orderService = orderService.Type(orderType)

	clientOrderID := req.ClientOrderID
	if clientOrderID != "" {
		clientOrderID = utils.AddBrokerPrefix("binance", clientOrderID)
		orderService = orderService.NewClientOrderID(clientOrderID)
	}

	resp, err := orderService.Do(ctx)
	if err != nil {
		return nil, err
	}
	executedQty, _ := strconv.ParseFloat(resp.ExecutedQuantity, 64)
	avgPrice := avgPrice(resp.CummulativeQuoteQuantity, resp.ExecutedQuantity)
	return &Order{
		OrderID:       resp.OrderID,
		ClientOrderID: resp.ClientOrderID,
		Symbol:        req.Symbol,
		Side:          Side(resp.Side),
		Type:          OrderType(resp.Type),
		Price:         req.Price,
		Quantity:      req.Quantity,
		ExecutedQty:   executedQty,
		AvgPrice:      avgPrice,
		Status:        OrderStatus(resp.Status),
		CreatedAt:     time.Now(),
		UpdateTime:    resp.TransactTime,
	}, nil
}

func (b *BinanceSpotAdapter) BatchPlaceOrders(ctx context.Context, orders []*OrderRequest) ([]*Order, bool) {
	placed := make([]*Order, 0, len(orders))
	hasBalanceError := false
	for _, orderReq := range orders {
		order, err := b.PlaceOrder(ctx, orderReq)
		if err != nil {
			logger.Warn("⚠️ [Binance Spot] 下单失败 %.8f %s: %v", orderReq.Price, orderReq.Side, err)
			if isInsufficientBalanceError(err) {
				hasBalanceError = true
			}
			continue
		}
		placed = append(placed, order)
	}
	return placed, hasBalanceError
}

func isInsufficientBalanceError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "-2010") ||
		strings.Contains(errStr, "-2019") ||
		strings.Contains(errStr, "insufficient") ||
		strings.Contains(errStr, "balance")
}

func (b *BinanceSpotAdapter) CancelOrder(ctx context.Context, symbol string, orderID int64) error {
	_, err := b.client.NewCancelOrderService().Symbol(symbol).OrderID(orderID).Do(ctx)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "-2011") || strings.Contains(errStr, "Unknown order") {
			logger.Info("ℹ️ [Binance Spot] 订单 %d 已不存在，跳过取消", orderID)
			return nil
		}
		return err
	}
	logger.Info("✅ [Binance Spot] 取消订单成功: %d", orderID)
	return nil
}

func (b *BinanceSpotAdapter) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	for _, orderID := range orderIDs {
		if err := b.CancelOrder(ctx, symbol, orderID); err != nil {
			logger.Warn("⚠️ [Binance Spot] 取消订单失败 %d: %v", orderID, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (b *BinanceSpotAdapter) CancelAllOrders(ctx context.Context, symbol string) error {
	orders, err := b.GetOpenOrders(ctx, symbol)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(orders))
	for _, order := range orders {
		ids = append(ids, order.OrderID)
	}
	return b.BatchCancelOrders(ctx, symbol, ids)
}

func (b *BinanceSpotAdapter) GetOrder(ctx context.Context, symbol string, orderID int64) (*Order, error) {
	order, err := b.client.NewGetOrderService().Symbol(symbol).OrderID(orderID).Do(ctx)
	if err != nil {
		return nil, err
	}
	return convertSpotOrder(order), nil
}

func (b *BinanceSpotAdapter) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	orders, err := b.client.NewListOpenOrdersService().Symbol(symbol).Do(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*Order, 0, len(orders))
	for _, order := range orders {
		result = append(result, convertSpotOrder(order))
	}
	return result, nil
}

func convertSpotOrder(order *spot.Order) *Order {
	price, _ := strconv.ParseFloat(order.Price, 64)
	quantity, _ := strconv.ParseFloat(order.OrigQuantity, 64)
	executedQty, _ := strconv.ParseFloat(order.ExecutedQuantity, 64)
	return &Order{
		OrderID:       order.OrderID,
		ClientOrderID: order.ClientOrderID,
		Symbol:        order.Symbol,
		Side:          Side(order.Side),
		Type:          OrderType(order.Type),
		Price:         price,
		Quantity:      quantity,
		ExecutedQty:   executedQty,
		AvgPrice:      avgPrice(order.CummulativeQuoteQuantity, order.ExecutedQuantity),
		Status:        OrderStatus(order.Status),
		CreatedAt:     time.UnixMilli(order.Time),
		UpdateTime:    order.UpdateTime,
	}
}

func avgPrice(quoteQty, executedQty string) float64 {
	quote, _ := strconv.ParseFloat(quoteQty, 64)
	qty, _ := strconv.ParseFloat(executedQty, 64)
	if qty <= 0 {
		return 0
	}
	return quote / qty
}

func (b *BinanceSpotAdapter) GetAccount(ctx context.Context) (*Account, error) {
	account, err := b.client.NewGetAccountService().OmitZeroBalances(true).Do(ctx)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "Service unavailable from a restricted location") {
			return nil, fmt.Errorf("你的网络连接在限制服务区域，请检查网络或使用代理")
		}
		return nil, err
	}

	var quoteFree, quoteLocked, baseFree, baseLocked float64
	for _, balance := range account.Balances {
		free, _ := strconv.ParseFloat(balance.Free, 64)
		locked, _ := strconv.ParseFloat(balance.Locked, 64)
		switch balance.Asset {
		case b.quoteAsset:
			quoteFree += free
			quoteLocked += locked
		case b.baseAsset:
			baseFree += free
			baseLocked += locked
		}
	}
	positions := []*Position{}
	baseTotal := baseFree + baseLocked
	if baseTotal > 0 {
		markPrice, _ := b.GetLatestPrice(ctx, b.symbol)
		positions = append(positions, &Position{
			Symbol:        b.symbol,
			Size:          baseTotal,
			EntryPrice:    markPrice,
			MarkPrice:     markPrice,
			Leverage:      1,
			MarginType:    "spot",
			UnrealizedPNL: 0,
		})
	}
	quoteTotal := quoteFree + quoteLocked
	return &Account{
		TotalWalletBalance: quoteTotal,
		TotalMarginBalance: quoteTotal,
		AvailableBalance:   quoteFree,
		Positions:          positions,
	}, nil
}

func (b *BinanceSpotAdapter) GetPositions(ctx context.Context, symbol string) ([]*Position, error) {
	account, err := b.GetAccount(ctx)
	if err != nil {
		return nil, err
	}
	return account.Positions, nil
}

func (b *BinanceSpotAdapter) GetBalance(ctx context.Context, asset string) (float64, error) {
	account, err := b.client.NewGetAccountService().OmitZeroBalances(true).Do(ctx)
	if err != nil {
		return 0, err
	}
	for _, balance := range account.Balances {
		if strings.EqualFold(balance.Asset, asset) {
			free, _ := strconv.ParseFloat(balance.Free, 64)
			return free, nil
		}
	}
	return 0, nil
}

func (b *BinanceSpotAdapter) StartOrderStream(ctx context.Context, callback func(interface{})) error {
	b.mu.Lock()
	if b.orderStopC != nil {
		b.mu.Unlock()
		return fmt.Errorf("订单流已在运行")
	}
	b.mu.Unlock()

	listenKey, err := b.client.NewStartUserStreamService().Do(ctx)
	if err != nil {
		return fmt.Errorf("获取现货 listenKey 失败: %w", err)
	}
	doneC, stopC, err := spot.WsUserDataServe(listenKey, func(event *spot.WsUserDataEvent) {
		if event == nil || event.OrderUpdate.Id == 0 {
			return
		}
		update := event.OrderUpdate
		price, _ := strconv.ParseFloat(update.Price, 64)
		quantity, _ := strconv.ParseFloat(update.Volume, 64)
		executedQty, _ := strconv.ParseFloat(update.FilledVolume, 64)
		callback(struct {
			OrderID       int64
			ClientOrderID string
			Symbol        string
			Side          string
			Type          string
			Status        string
			Price         float64
			Quantity      float64
			ExecutedQty   float64
			AvgPrice      float64
			UpdateTime    int64
		}{
			OrderID:       update.Id,
			ClientOrderID: update.ClientOrderId,
			Symbol:        update.Symbol,
			Side:          update.Side,
			Type:          update.Type,
			Status:        update.Status,
			Price:         price,
			Quantity:      quantity,
			ExecutedQty:   executedQty,
			AvgPrice:      avgPrice(update.FilledQuoteVolume, update.FilledVolume),
			UpdateTime:    update.TransactionTime,
		})
	}, func(err error) {
		logger.Warn("⚠️ [Binance Spot] 订单流错误: %v", err)
	})
	if err != nil {
		return err
	}

	b.mu.Lock()
	b.orderListenKey = listenKey
	b.orderStopC = stopC
	b.mu.Unlock()

	go b.keepAliveOrderStream(ctx, listenKey)
	go func() {
		<-doneC
		b.mu.Lock()
		b.orderStopC = nil
		b.mu.Unlock()
	}()
	return nil
}

func (b *BinanceSpotAdapter) keepAliveOrderStream(ctx context.Context, listenKey string) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.client.NewKeepaliveUserStreamService().ListenKey(listenKey).Do(ctx); err != nil {
				logger.Warn("⚠️ [Binance Spot] listenKey 保活失败: %v", err)
			}
		}
	}
}

func (b *BinanceSpotAdapter) StopOrderStream() error {
	b.mu.Lock()
	stopC := b.orderStopC
	listenKey := b.orderListenKey
	b.orderStopC = nil
	b.orderListenKey = ""
	b.mu.Unlock()
	if stopC != nil {
		close(stopC)
	}
	if listenKey != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = b.client.NewCloseUserStreamService().ListenKey(listenKey).Do(ctx)
	}
	return nil
}

func (b *BinanceSpotAdapter) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	b.mu.Lock()
	cached := b.latestPrice
	b.mu.Unlock()
	if cached > 0 {
		return cached, nil
	}
	prices, err := b.client.NewListPricesService().Symbol(symbol).Do(ctx)
	if err != nil {
		return 0, err
	}
	if len(prices) == 0 {
		return 0, fmt.Errorf("未获取到现货价格: %s", symbol)
	}
	price, err := strconv.ParseFloat(prices[0].Price, 64)
	if err != nil {
		return 0, err
	}
	return price, nil
}

func (b *BinanceSpotAdapter) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	firstPriceCh := make(chan struct{})
	var once sync.Once
	doneC, stopC, err := spot.WsAggTradeServe(symbol, func(event *spot.WsAggTradeEvent) {
		price, err := strconv.ParseFloat(event.Price, 64)
		if err != nil || price <= 0 {
			return
		}
		b.mu.Lock()
		b.latestPrice = price
		b.mu.Unlock()
		once.Do(func() { close(firstPriceCh) })
		callback(price)
	}, func(err error) {
		logger.Warn("⚠️ [Binance Spot] 价格流错误: %v", err)
	})
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.priceStopC = stopC
	b.mu.Unlock()
	go func() {
		<-doneC
		b.mu.Lock()
		b.priceStopC = nil
		b.mu.Unlock()
	}()

	select {
	case <-firstPriceCh:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("等待首个现货价格超时（10秒）")
	case <-ctx.Done():
		return fmt.Errorf("上下文已取消")
	}
}

func (b *BinanceSpotAdapter) StartKlineStream(ctx context.Context, symbols []string, interval string, callback func(candle interface{})) error {
	pairs := make(map[string]string, len(symbols))
	for _, symbol := range symbols {
		pairs[symbol] = interval
	}
	doneC, stopC, err := spot.WsCombinedKlineServe(pairs, func(event *spot.WsKlineEvent) {
		open, _ := strconv.ParseFloat(event.Kline.Open, 64)
		high, _ := strconv.ParseFloat(event.Kline.High, 64)
		low, _ := strconv.ParseFloat(event.Kline.Low, 64)
		closePrice, _ := strconv.ParseFloat(event.Kline.Close, 64)
		volume, _ := strconv.ParseFloat(event.Kline.Volume, 64)
		callback(&Candle{
			Symbol:    event.Symbol,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closePrice,
			Volume:    volume,
			Timestamp: event.Kline.StartTime,
			IsClosed:  event.Kline.IsFinal,
		})
	}, func(err error) {
		logger.Warn("⚠️ [Binance Spot] K线流错误: %v", err)
	})
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.klineStopC = stopC
	b.mu.Unlock()
	go func() {
		<-doneC
		b.mu.Lock()
		b.klineStopC = nil
		b.mu.Unlock()
	}()
	return nil
}

func (b *BinanceSpotAdapter) StopKlineStream() error {
	b.mu.Lock()
	stopC := b.klineStopC
	b.klineStopC = nil
	b.mu.Unlock()
	if stopC != nil {
		close(stopC)
	}
	return nil
}

func (b *BinanceSpotAdapter) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*Candle, error) {
	klines, err := b.client.NewKlinesService().Symbol(symbol).Interval(interval).Limit(limit).Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取现货历史K线失败: %w", err)
	}
	candles := make([]*Candle, 0, len(klines))
	for _, k := range klines {
		open, _ := strconv.ParseFloat(k.Open, 64)
		high, _ := strconv.ParseFloat(k.High, 64)
		low, _ := strconv.ParseFloat(k.Low, 64)
		closePrice, _ := strconv.ParseFloat(k.Close, 64)
		volume, _ := strconv.ParseFloat(k.Volume, 64)
		candles = append(candles, &Candle{
			Symbol:    symbol,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closePrice,
			Volume:    volume,
			Timestamp: k.OpenTime,
			IsClosed:  true,
		})
	}
	return candles, nil
}

func (b *BinanceSpotAdapter) GetPriceDecimals() int {
	return int(math.Max(float64(b.priceDecimals), 0))
}

func (b *BinanceSpotAdapter) GetQuantityDecimals() int {
	return int(math.Max(float64(b.quantityDecimals), 0))
}

func (b *BinanceSpotAdapter) GetBaseAsset() string {
	return b.baseAsset
}

func (b *BinanceSpotAdapter) GetQuoteAsset() string {
	return b.quoteAsset
}
