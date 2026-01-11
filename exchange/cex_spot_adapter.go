package exchange

import (
	"context"
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

	"nexus-trade-bot/exchange/bitget"
	"nexus-trade-bot/exchange/bybit"
	"nexus-trade-bot/exchange/gate"
	"nexus-trade-bot/exchange/okx"
	"nexus-trade-bot/logger"
	"nexus-trade-bot/utils"
)

type cexSpotAdapter struct {
	exchangeName     string
	displayName      string
	symbol           string
	venueSymbol      string
	baseAsset        string
	quoteAsset       string
	priceDecimals    int
	quantityDecimals int

	bitget *bitget.Client
	bybit  *bybit.Client
	gate   *gate.Client
	okx    *okx.Client

	ids     *spotOrderIDMapper
	trackMu sync.RWMutex
	tracked map[int64]string
}

type spotOrderIDMapper struct {
	mu       sync.RWMutex
	byString map[string]int64
	byInt    map[int64]string
	table    *crc64.Table
}

func newSpotOrderIDMapper() *spotOrderIDMapper {
	return &spotOrderIDMapper{
		byString: make(map[string]int64),
		byInt:    make(map[int64]string),
		table:    crc64.MakeTable(crc64.ISO),
	}
}

func (m *spotOrderIDMapper) encode(orderID string) int64 {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return 0
	}
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

func (m *spotOrderIDMapper) lookup(orderID int64) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.byInt[orderID]
	return value, ok
}

func newCEXSpotAdapter(exchangeName string, cfg map[string]string, symbol string) (*cexSpotAdapter, error) {
	apiKey := strings.TrimSpace(cfg["api_key"])
	secretKey := strings.TrimSpace(cfg["secret_key"])
	passphrase := strings.TrimSpace(cfg["passphrase"])
	if apiKey == "" || secretKey == "" {
		return nil, fmt.Errorf("%s API 配置不完整", strings.ToUpper(exchangeName))
	}
	adapter := &cexSpotAdapter{
		exchangeName:     strings.ToLower(exchangeName),
		symbol:           normalizeSpotSymbol(symbol),
		priceDecimals:    2,
		quantityDecimals: 4,
		ids:              newSpotOrderIDMapper(),
		tracked:          make(map[int64]string),
	}
	adapter.baseAsset, adapter.quoteAsset = splitSpotSymbol(adapter.symbol)

	switch adapter.exchangeName {
	case "bitget":
		if passphrase == "" {
			return nil, fmt.Errorf("Bitget 需要填写 Passphrase")
		}
		adapter.displayName = "Bitget Spot"
		adapter.venueSymbol = adapter.symbol
		adapter.bitget = bitget.NewClient(apiKey, secretKey, passphrase)
	case "bybit":
		adapter.displayName = "Bybit Spot"
		adapter.venueSymbol = adapter.symbol
		adapter.bybit = bybit.NewClient(apiKey, secretKey)
	case "gate":
		adapter.displayName = "Gate.io Spot"
		adapter.venueSymbol = toGateSpotSymbol(adapter.symbol)
		adapter.gate = gate.NewClient(apiKey, secretKey)
	case "okx":
		if passphrase == "" {
			return nil, fmt.Errorf("OKX 需要填写 Passphrase")
		}
		adapter.displayName = "OKX Spot"
		adapter.venueSymbol = toOKXSpotInstrument(adapter.symbol)
		adapter.okx = okx.NewClient(apiKey, secretKey, passphrase)
	default:
		return nil, fmt.Errorf("现货模式暂不支持交易所: %s", exchangeName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := adapter.fetchInstrumentInfo(ctx); err != nil {
		logger.Warn("⚠️ [%s] 获取现货交易对信息失败: %v，使用默认精度", adapter.displayName, err)
	}
	return adapter, nil
}

func (a *cexSpotAdapter) GetName() string { return a.displayName }

func (a *cexSpotAdapter) PlaceOrder(ctx context.Context, req *OrderRequest) (*Order, error) {
	priceDecimals := req.PriceDecimals
	if priceDecimals <= 0 {
		priceDecimals = a.priceDecimals
	}
	side := strings.ToLower(string(req.Side))
	if side != "buy" && side != "sell" {
		return nil, fmt.Errorf("现货订单方向仅支持 BUY/SELL: %s", req.Side)
	}

	var order *Order
	var err error
	switch a.exchangeName {
	case "bitget":
		order, err = a.placeBitgetOrder(ctx, req, side, priceDecimals)
	case "bybit":
		order, err = a.placeBybitOrder(ctx, req, side, priceDecimals)
	case "gate":
		order, err = a.placeGateOrder(ctx, req, side, priceDecimals)
	case "okx":
		order, err = a.placeOKXOrder(ctx, req, side, priceDecimals)
	default:
		err = fmt.Errorf("现货模式暂不支持交易所: %s", a.exchangeName)
	}
	if err != nil {
		return nil, err
	}
	a.trackOrder(order)
	return order, nil
}

func (a *cexSpotAdapter) BatchPlaceOrders(ctx context.Context, orders []*OrderRequest) ([]*Order, bool) {
	placed := make([]*Order, 0, len(orders))
	hasBalanceError := false
	for _, req := range orders {
		order, err := a.PlaceOrder(ctx, req)
		if err != nil {
			logger.Warn("⚠️ [%s] 现货下单失败 %.6f %s: %v", a.displayName, req.Price, req.Side, err)
			if isSpotBalanceError(err) {
				hasBalanceError = true
			}
			continue
		}
		placed = append(placed, order)
	}
	return placed, hasBalanceError
}

func (a *cexSpotAdapter) CancelOrder(ctx context.Context, symbol string, orderID int64) error {
	remoteID, ok := a.ids.lookup(orderID)
	if !ok {
		remoteID = strconv.FormatInt(orderID, 10)
	}
	var err error
	switch a.exchangeName {
	case "bitget":
		_, err = a.bitget.DoRequest(ctx, http.MethodPost, "/api/v2/spot/trade/cancel-order", map[string]any{
			"symbol":  a.venueSymbol,
			"orderId": remoteID,
		})
	case "bybit":
		_, err = a.bybit.DoSignedRequest(ctx, http.MethodPost, "/v5/order/cancel", nil, map[string]any{
			"category": "spot",
			"symbol":   a.venueSymbol,
			"orderId":  remoteID,
		})
	case "gate":
		_, err = a.gate.DoRequest(ctx, http.MethodDelete, "/spot/orders/"+remoteID, "currency_pair="+a.venueSymbol, nil)
	case "okx":
		_, err = a.okx.DoSignedRequest(ctx, http.MethodPost, "/api/v5/trade/cancel-order", nil, map[string]any{
			"instId": a.venueSymbol,
			"ordId":  remoteID,
		})
	}
	if err != nil && isOrderGoneError(err) {
		return nil
	}
	return err
}

func (a *cexSpotAdapter) BatchCancelOrders(ctx context.Context, symbol string, orderIDs []int64) error {
	for _, orderID := range orderIDs {
		if err := a.CancelOrder(ctx, symbol, orderID); err != nil {
			return err
		}
	}
	return nil
}

func (a *cexSpotAdapter) CancelAllOrders(ctx context.Context, symbol string) error {
	orders, err := a.GetOpenOrders(ctx, symbol)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(orders))
	for _, order := range orders {
		ids = append(ids, order.OrderID)
	}
	return a.BatchCancelOrders(ctx, symbol, ids)
}

func (a *cexSpotAdapter) GetOrder(ctx context.Context, symbol string, orderID int64) (*Order, error) {
	remoteID, ok := a.ids.lookup(orderID)
	if !ok {
		remoteID = strconv.FormatInt(orderID, 10)
	}
	switch a.exchangeName {
	case "bitget":
		resp, err := a.bitget.DoRequest(ctx, http.MethodGet, "/api/v2/spot/trade/orderInfo?symbol="+a.venueSymbol+"&orderId="+remoteID, nil)
		if err != nil {
			return nil, err
		}
		var items []bitgetSpotOrder
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			var item bitgetSpotOrder
			if err2 := json.Unmarshal(resp.Data, &item); err2 != nil {
				return nil, fmt.Errorf("解析 Bitget 现货订单失败: %w", err)
			}
			items = []bitgetSpotOrder{item}
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("未找到 Bitget 现货订单: %d", orderID)
		}
		return a.convertBitgetOrder(items[0]), nil
	case "bybit":
		resp, err := a.bybit.DoSignedRequest(ctx, http.MethodGet, "/v5/order/realtime", map[string]string{
			"category": "spot",
			"symbol":   a.venueSymbol,
			"orderId":  remoteID,
		}, nil)
		if err != nil {
			return nil, err
		}
		items, err := parseBybitSpotOrders(resp.Result)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("未找到 Bybit 现货订单: %d", orderID)
		}
		return a.convertBybitOrder(items[0]), nil
	case "gate":
		body, err := a.gate.DoRequest(ctx, http.MethodGet, "/spot/orders/"+remoteID, "currency_pair="+a.venueSymbol, nil)
		if err != nil {
			return nil, err
		}
		var item gateSpotOrder
		if err := json.Unmarshal(body, &item); err != nil {
			return nil, fmt.Errorf("解析 Gate 现货订单失败: %w", err)
		}
		return a.convertGateOrder(item), nil
	case "okx":
		resp, err := a.okx.DoSignedRequest(ctx, http.MethodGet, "/api/v5/trade/order", map[string]string{
			"instId": a.venueSymbol,
			"ordId":  remoteID,
		}, nil)
		if err != nil {
			return nil, err
		}
		var items []okxSpotOrder
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			return nil, fmt.Errorf("解析 OKX 现货订单失败: %w", err)
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("未找到 OKX 现货订单: %d", orderID)
		}
		return a.convertOKXOrder(items[0]), nil
	}
	return nil, fmt.Errorf("现货模式暂不支持交易所: %s", a.exchangeName)
}

func (a *cexSpotAdapter) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	var orders []*Order
	switch a.exchangeName {
	case "bitget":
		resp, err := a.bitget.DoRequest(ctx, http.MethodGet, "/api/v2/spot/trade/unfilled-orders?symbol="+a.venueSymbol, nil)
		if err != nil {
			return nil, err
		}
		var items []bitgetSpotOrder
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			return nil, fmt.Errorf("解析 Bitget 现货未成交订单失败: %w", err)
		}
		for _, item := range items {
			orders = append(orders, a.convertBitgetOrder(item))
		}
	case "bybit":
		resp, err := a.bybit.DoSignedRequest(ctx, http.MethodGet, "/v5/order/realtime", map[string]string{
			"category": "spot",
			"symbol":   a.venueSymbol,
			"openOnly": "0",
			"limit":    "50",
		}, nil)
		if err != nil {
			return nil, err
		}
		items, err := parseBybitSpotOrders(resp.Result)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			orders = append(orders, a.convertBybitOrder(item))
		}
	case "gate":
		body, err := a.gate.DoRequest(ctx, http.MethodGet, "/spot/orders", "currency_pair="+a.venueSymbol+"&status=open", nil)
		if err != nil {
			return nil, err
		}
		var items []gateSpotOrder
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("解析 Gate 现货未成交订单失败: %w", err)
		}
		for _, item := range items {
			orders = append(orders, a.convertGateOrder(item))
		}
	case "okx":
		resp, err := a.okx.DoSignedRequest(ctx, http.MethodGet, "/api/v5/trade/orders-pending", map[string]string{
			"instType": "SPOT",
			"instId":   a.venueSymbol,
		}, nil)
		if err != nil {
			return nil, err
		}
		var items []okxSpotOrder
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			return nil, fmt.Errorf("解析 OKX 现货未成交订单失败: %w", err)
		}
		for _, item := range items {
			orders = append(orders, a.convertOKXOrder(item))
		}
	}
	sort.SliceStable(orders, func(i, j int) bool { return orders[i].Price < orders[j].Price })
	for _, order := range orders {
		a.trackOrder(order)
	}
	return orders, nil
}

func (a *cexSpotAdapter) GetAccount(ctx context.Context) (*Account, error) {
	quoteBalance, err := a.GetBalance(ctx, a.quoteAsset)
	if err != nil {
		return nil, err
	}
	baseBalance, _ := a.GetBalance(ctx, a.baseAsset)
	total := quoteBalance
	if baseBalance > 0 {
		if price, err := a.GetLatestPrice(ctx, a.symbol); err == nil && price > 0 {
			total += baseBalance * price
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

func (a *cexSpotAdapter) GetPositions(ctx context.Context, symbol string) ([]*Position, error) {
	baseBalance, err := a.getSpotAssetTotal(ctx, a.baseAsset)
	if err != nil {
		return nil, err
	}
	if baseBalance <= 0 {
		return []*Position{}, nil
	}

	markPrice, err := a.GetLatestPrice(ctx, a.symbol)
	if err != nil {
		logger.Warn("⚠️ [%s] 查询现货持仓参考价失败: %v，将使用启动锚点恢复", a.displayName, err)
		markPrice = 0
	}
	return []*Position{{
		Symbol:        a.symbol,
		Size:          baseBalance,
		EntryPrice:    markPrice,
		MarkPrice:     markPrice,
		Leverage:      1,
		MarginType:    "spot",
		UnrealizedPNL: 0,
	}}, nil
}

func (a *cexSpotAdapter) getSpotAssetTotal(ctx context.Context, asset string) (float64, error) {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	switch a.exchangeName {
	case "bitget":
		resp, err := a.bitget.DoRequest(ctx, http.MethodGet, "/api/v2/spot/account/assets?coin="+asset, nil)
		if err != nil {
			return 0, err
		}
		var items []struct {
			Coin       string `json:"coin"`
			Available  string `json:"available"`
			Available2 string `json:"availableBalance"`
			Frozen     string `json:"frozen"`
			Locked     string `json:"locked"`
		}
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			return 0, fmt.Errorf("解析 Bitget 现货余额失败: %w", err)
		}
		for _, item := range items {
			if strings.EqualFold(item.Coin, asset) {
				return parseSpotFloat(firstNonEmpty(item.Available, item.Available2)) + parseSpotFloat(firstNonEmpty(item.Frozen, item.Locked)), nil
			}
		}
	case "gate":
		body, err := a.gate.DoRequest(ctx, http.MethodGet, "/spot/accounts", "currency="+asset, nil)
		if err != nil {
			return 0, err
		}
		var items []struct {
			Currency  string `json:"currency"`
			Available string `json:"available"`
			Locked    string `json:"locked"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return 0, fmt.Errorf("解析 Gate 现货余额失败: %w", err)
		}
		for _, item := range items {
			if strings.EqualFold(item.Currency, asset) {
				return parseSpotFloat(item.Available) + parseSpotFloat(item.Locked), nil
			}
		}
	default:
		return a.GetBalance(ctx, asset)
	}
	return 0, nil
}

func (a *cexSpotAdapter) GetBalance(ctx context.Context, asset string) (float64, error) {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	switch a.exchangeName {
	case "bitget":
		resp, err := a.bitget.DoRequest(ctx, http.MethodGet, "/api/v2/spot/account/assets?coin="+asset, nil)
		if err != nil {
			return 0, err
		}
		var items []struct {
			Coin       string `json:"coin"`
			Available  string `json:"available"`
			Available2 string `json:"availableBalance"`
			Frozen     string `json:"frozen"`
		}
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			return 0, fmt.Errorf("解析 Bitget 现货余额失败: %w", err)
		}
		for _, item := range items {
			if strings.EqualFold(item.Coin, asset) {
				return parseSpotFloat(firstNonEmpty(item.Available, item.Available2)), nil
			}
		}
	case "bybit":
		resp, err := a.bybit.DoSignedRequest(ctx, http.MethodGet, "/v5/account/wallet-balance", map[string]string{
			"accountType": "UNIFIED",
			"coin":        asset,
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
			return 0, fmt.Errorf("解析 Bybit 现货余额失败: %w", err)
		}
		for _, wallet := range result.List {
			for _, coin := range wallet.Coin {
				if strings.EqualFold(coin.Coin, asset) {
					return parseSpotFloat(firstNonEmpty(coin.Equity, coin.WalletBalance)), nil
				}
			}
		}
	case "gate":
		body, err := a.gate.DoRequest(ctx, http.MethodGet, "/spot/accounts", "currency="+asset, nil)
		if err != nil {
			return 0, err
		}
		var items []struct {
			Currency  string `json:"currency"`
			Available string `json:"available"`
			Locked    string `json:"locked"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return 0, fmt.Errorf("解析 Gate 现货余额失败: %w", err)
		}
		for _, item := range items {
			if strings.EqualFold(item.Currency, asset) {
				return parseSpotFloat(item.Available), nil
			}
		}
	case "okx":
		resp, err := a.okx.DoSignedRequest(ctx, http.MethodGet, "/api/v5/account/balance", map[string]string{"ccy": asset}, nil)
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
			return 0, fmt.Errorf("解析 OKX 现货余额失败: %w", err)
		}
		for _, wallet := range result {
			for _, detail := range wallet.Details {
				if strings.EqualFold(detail.Ccy, asset) {
					return parseSpotFloat(firstNonEmpty(detail.Eq, detail.CashBal)), nil
				}
			}
		}
	}
	return 0, nil
}

func (a *cexSpotAdapter) StartOrderStream(ctx context.Context, callback func(interface{})) error {
	go func() {
		lastSeen := make(map[int64]spotOrderSnapshot)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				openOrders, err := a.GetOpenOrders(ctx, a.symbol)
				if err == nil {
					for _, order := range openOrders {
						a.trackOrder(order)
					}
				} else {
					logger.Warn("⚠️ [%s] 现货订单扫描失败: %v", a.displayName, err)
				}
				for _, id := range a.trackedOrderIDs() {
					order, err := a.GetOrder(ctx, a.symbol, id)
					if err != nil {
						continue
					}
					next := spotOrderSnapshot{Status: order.Status, ExecutedQty: order.ExecutedQty, AvgPrice: order.AvgPrice}
					if lastSeen[id] == next && !isFinalSpotStatus(order.Status) {
						continue
					}
					lastSeen[id] = next
					callback(OrderUpdate{
						OrderID:       order.OrderID,
						ClientOrderID: order.ClientOrderID,
						Symbol:        order.Symbol,
						Side:          order.Side,
						Type:          order.Type,
						Status:        order.Status,
						Price:         order.Price,
						Quantity:      order.Quantity,
						ExecutedQty:   order.ExecutedQty,
						AvgPrice:      order.AvgPrice,
						UpdateTime:    order.UpdateTime,
					})
					if isFinalSpotStatus(order.Status) {
						a.untrackOrder(order.OrderID)
						delete(lastSeen, id)
					}
				}
			}
		}
	}()
	return nil
}

func (a *cexSpotAdapter) StopOrderStream() error { return nil }

type spotOrderSnapshot struct {
	Status      OrderStatus
	ExecutedQty float64
	AvgPrice    float64
}

func (a *cexSpotAdapter) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	switch a.exchangeName {
	case "bitget":
		var payload struct {
			Code string `json:"code"`
			Data []struct {
				LastPr string `json:"lastPr"`
				Close  string `json:"close"`
			} `json:"data"`
		}
		if err := fetchPublicSpotJSON(ctx, "https://api.bitget.com/api/v2/spot/market/tickers?symbol="+a.venueSymbol, &payload); err != nil {
			return 0, err
		}
		if len(payload.Data) == 0 {
			return 0, fmt.Errorf("Bitget 现货 ticker 为空")
		}
		return parseSpotFloat(firstNonEmpty(payload.Data[0].LastPr, payload.Data[0].Close)), nil
	case "bybit":
		resp, err := a.bybit.DoPublicRequest(ctx, http.MethodGet, "/v5/market/tickers", map[string]string{
			"category": "spot",
			"symbol":   a.venueSymbol,
		})
		if err != nil {
			return 0, err
		}
		var result struct {
			List []struct {
				LastPrice string `json:"lastPrice"`
			} `json:"list"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return 0, fmt.Errorf("解析 Bybit 现货 ticker 失败: %w", err)
		}
		if len(result.List) == 0 {
			return 0, fmt.Errorf("Bybit 现货 ticker 为空")
		}
		return parseSpotFloat(result.List[0].LastPrice), nil
	case "gate":
		body, err := a.gate.DoRequest(ctx, http.MethodGet, "/spot/tickers", "currency_pair="+a.venueSymbol, nil)
		if err != nil {
			return 0, err
		}
		var items []struct {
			Last string `json:"last"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return 0, fmt.Errorf("解析 Gate 现货 ticker 失败: %w", err)
		}
		if len(items) == 0 {
			return 0, fmt.Errorf("Gate 现货 ticker 为空")
		}
		return parseSpotFloat(items[0].Last), nil
	case "okx":
		resp, err := a.okx.DoPublicRequest(ctx, http.MethodGet, "/api/v5/market/ticker", map[string]string{"instId": a.venueSymbol})
		if err != nil {
			return 0, err
		}
		var items []struct {
			Last string `json:"last"`
		}
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			return 0, fmt.Errorf("解析 OKX 现货 ticker 失败: %w", err)
		}
		if len(items) == 0 {
			return 0, fmt.Errorf("OKX 现货 ticker 为空")
		}
		return parseSpotFloat(items[0].Last), nil
	}
	return 0, fmt.Errorf("现货模式暂不支持交易所: %s", a.exchangeName)
}

func (a *cexSpotAdapter) StartPriceStream(ctx context.Context, symbol string, callback func(price float64)) error {
	firstPriceCh := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			price, err := a.GetLatestPrice(ctx, symbol)
			if err == nil && price > 0 {
				once.Do(func() { close(firstPriceCh) })
				callback(price)
			} else if err != nil {
				logger.Warn("⚠️ [%s] 现货价格轮询失败: %v", a.displayName, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return fmt.Errorf("上下文已取消")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("等待 %s 首个现货价格超时", a.displayName)
	case <-firstPriceCh:
		return nil
	}
}

func (a *cexSpotAdapter) StartKlineStream(ctx context.Context, symbols []string, interval string, callback CandleUpdateCallback) error {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			for _, symbol := range symbols {
				candles, err := a.GetHistoricalKlines(ctx, symbol, interval, 2)
				if err != nil {
					logger.Warn("⚠️ [%s] 现货K线轮询失败: %v", a.displayName, err)
					continue
				}
				if len(candles) > 0 {
					callback(candles[len(candles)-1])
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (a *cexSpotAdapter) StopKlineStream() error { return nil }

func (a *cexSpotAdapter) GetHistoricalKlines(ctx context.Context, symbol string, interval string, limit int) ([]*Candle, error) {
	if limit <= 0 {
		limit = 100
	}
	switch a.exchangeName {
	case "bybit":
		resp, err := a.bybit.DoPublicRequest(ctx, http.MethodGet, "/v5/market/kline", map[string]string{
			"category": "spot",
			"symbol":   normalizeSpotSymbol(symbol),
			"interval": bybitSpotInterval(interval),
			"limit":    strconv.Itoa(limit),
		})
		if err != nil {
			return nil, err
		}
		var result struct {
			List [][]string `json:"list"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, fmt.Errorf("解析 Bybit 现货K线失败: %w", err)
		}
		return candlesFromRows(normalizeSpotSymbol(symbol), result.List, true), nil
	case "okx":
		resp, err := a.okx.DoPublicRequest(ctx, http.MethodGet, "/api/v5/market/candles", map[string]string{
			"instId": toOKXSpotInstrument(symbol),
			"bar":    okxSpotInterval(interval),
			"limit":  strconv.Itoa(limit),
		})
		if err != nil {
			return nil, err
		}
		var rows [][]string
		if err := json.Unmarshal(resp.Data, &rows); err != nil {
			return nil, fmt.Errorf("解析 OKX 现货K线失败: %w", err)
		}
		return candlesFromRows(normalizeSpotSymbol(symbol), rows, true), nil
	case "gate":
		body, err := a.gate.DoRequest(ctx, http.MethodGet, "/spot/candlesticks", "currency_pair="+toGateSpotSymbol(symbol)+"&interval="+gateSpotInterval(interval)+"&limit="+strconv.Itoa(limit), nil)
		if err != nil {
			return nil, err
		}
		var rows [][]string
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, fmt.Errorf("解析 Gate 现货K线失败: %w", err)
		}
		return candlesFromRows(normalizeSpotSymbol(symbol), rows, false), nil
	default:
		var payload struct {
			Code string     `json:"code"`
			Data [][]string `json:"data"`
		}
		url := "https://api.bitget.com/api/v2/spot/market/candles?symbol=" + normalizeSpotSymbol(symbol) + "&granularity=" + bitgetSpotInterval(interval) + "&limit=" + strconv.Itoa(limit)
		if err := fetchPublicSpotJSON(ctx, url, &payload); err != nil {
			return nil, err
		}
		return candlesFromRows(normalizeSpotSymbol(symbol), payload.Data, true), nil
	}
}

func (a *cexSpotAdapter) GetPriceDecimals() int    { return a.priceDecimals }
func (a *cexSpotAdapter) GetQuantityDecimals() int { return a.quantityDecimals }
func (a *cexSpotAdapter) GetBaseAsset() string     { return a.baseAsset }
func (a *cexSpotAdapter) GetQuoteAsset() string    { return a.quoteAsset }

func (a *cexSpotAdapter) fetchInstrumentInfo(ctx context.Context) error {
	switch a.exchangeName {
	case "bitget":
		var payload struct {
			Code string `json:"code"`
			Data []struct {
				Symbol            string `json:"symbol"`
				BaseCoin          string `json:"baseCoin"`
				QuoteCoin         string `json:"quoteCoin"`
				PricePrecision    string `json:"pricePrecision"`
				QuantityPrecision string `json:"quantityPrecision"`
			} `json:"data"`
		}
		if err := fetchPublicSpotJSON(ctx, "https://api.bitget.com/api/v2/spot/public/symbols?symbol="+a.venueSymbol, &payload); err != nil {
			return err
		}
		if len(payload.Data) == 0 {
			return fmt.Errorf("未找到 Bitget 现货交易对: %s", a.venueSymbol)
		}
		item := payload.Data[0]
		a.baseAsset, a.quoteAsset = item.BaseCoin, item.QuoteCoin
		a.priceDecimals = parseSpotInt(item.PricePrecision, a.priceDecimals)
		a.quantityDecimals = parseSpotInt(item.QuantityPrecision, a.quantityDecimals)
	case "bybit":
		resp, err := a.bybit.DoPublicRequest(ctx, http.MethodGet, "/v5/market/instruments-info", map[string]string{
			"category": "spot",
			"symbol":   a.venueSymbol,
		})
		if err != nil {
			return err
		}
		var result struct {
			List []struct {
				BaseCoin    string `json:"baseCoin"`
				QuoteCoin   string `json:"quoteCoin"`
				PriceFilter struct {
					TickSize string `json:"tickSize"`
				} `json:"priceFilter"`
				LotSizeFilter struct {
					BasePrecision string `json:"basePrecision"`
					QtyStep       string `json:"qtyStep"`
				} `json:"lotSizeFilter"`
			} `json:"list"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return fmt.Errorf("解析 Bybit 现货交易对失败: %w", err)
		}
		if len(result.List) == 0 {
			return fmt.Errorf("未找到 Bybit 现货交易对: %s", a.venueSymbol)
		}
		item := result.List[0]
		a.baseAsset, a.quoteAsset = item.BaseCoin, item.QuoteCoin
		a.priceDecimals = decimalsFromSpotStep(item.PriceFilter.TickSize, a.priceDecimals)
		a.quantityDecimals = decimalsFromSpotStep(firstNonEmpty(item.LotSizeFilter.QtyStep, item.LotSizeFilter.BasePrecision), a.quantityDecimals)
	case "gate":
		body, err := a.gate.DoRequest(ctx, http.MethodGet, "/spot/currency_pairs/"+a.venueSymbol, "", nil)
		if err != nil {
			return err
		}
		var item struct {
			Base            string `json:"base"`
			Quote           string `json:"quote"`
			AmountPrecision int    `json:"amount_precision"`
			Precision       int    `json:"precision"`
		}
		if err := json.Unmarshal(body, &item); err != nil {
			return fmt.Errorf("解析 Gate 现货交易对失败: %w", err)
		}
		a.baseAsset, a.quoteAsset = item.Base, item.Quote
		if item.Precision > 0 {
			a.priceDecimals = item.Precision
		}
		if item.AmountPrecision > 0 {
			a.quantityDecimals = item.AmountPrecision
		}
	case "okx":
		resp, err := a.okx.DoPublicRequest(ctx, http.MethodGet, "/api/v5/public/instruments", map[string]string{
			"instType": "SPOT",
			"instId":   a.venueSymbol,
		})
		if err != nil {
			return err
		}
		var items []struct {
			BaseCcy  string `json:"baseCcy"`
			QuoteCcy string `json:"quoteCcy"`
			TickSz   string `json:"tickSz"`
			LotSz    string `json:"lotSz"`
		}
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			return fmt.Errorf("解析 OKX 现货交易对失败: %w", err)
		}
		if len(items) == 0 {
			return fmt.Errorf("未找到 OKX 现货交易对: %s", a.venueSymbol)
		}
		item := items[0]
		a.baseAsset, a.quoteAsset = item.BaseCcy, item.QuoteCcy
		a.priceDecimals = decimalsFromSpotStep(item.TickSz, a.priceDecimals)
		a.quantityDecimals = decimalsFromSpotStep(item.LotSz, a.quantityDecimals)
	}
	return nil
}

func (a *cexSpotAdapter) placeBitgetOrder(ctx context.Context, req *OrderRequest, side string, priceDecimals int) (*Order, error) {
	body := map[string]any{
		"symbol":    a.venueSymbol,
		"side":      side,
		"orderType": "limit",
		"force":     "gtc",
		"size":      formatSpot(req.Quantity, a.quantityDecimals),
		"price":     formatSpot(req.Price, priceDecimals),
	}
	if req.PostOnly {
		body["force"] = "post_only"
	}
	if req.ClientOrderID != "" {
		body["clientOid"] = req.ClientOrderID
	}
	resp, err := a.bitget.DoRequest(ctx, http.MethodPost, "/api/v2/spot/trade/place-order", body)
	if err != nil {
		return nil, err
	}
	var result struct {
		OrderID   string `json:"orderId"`
		ClientOID string `json:"clientOid"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("解析 Bitget 现货下单响应失败: %w", err)
	}
	return a.newOrder(result.OrderID, firstNonEmpty(result.ClientOID, req.ClientOrderID), Side(strings.ToUpper(side)), req.Price, req.Quantity, 0, 0, OrderStatusNew), nil
}

func (a *cexSpotAdapter) placeBybitOrder(ctx context.Context, req *OrderRequest, side string, priceDecimals int) (*Order, error) {
	body := map[string]any{
		"category":    "spot",
		"symbol":      a.venueSymbol,
		"side":        strings.Title(side),
		"orderType":   "Limit",
		"qty":         formatSpot(req.Quantity, a.quantityDecimals),
		"price":       formatSpot(req.Price, priceDecimals),
		"timeInForce": "GTC",
	}
	if req.PostOnly {
		body["timeInForce"] = "PostOnly"
	}
	if req.ClientOrderID != "" {
		body["orderLinkId"] = req.ClientOrderID
	}
	resp, err := a.bybit.DoSignedRequest(ctx, http.MethodPost, "/v5/order/create", nil, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		OrderID     string `json:"orderId"`
		OrderLinkID string `json:"orderLinkId"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("解析 Bybit 现货下单响应失败: %w", err)
	}
	return a.newOrder(result.OrderID, firstNonEmpty(result.OrderLinkID, req.ClientOrderID), Side(strings.ToUpper(side)), req.Price, req.Quantity, 0, 0, OrderStatusNew), nil
}

func (a *cexSpotAdapter) placeGateOrder(ctx context.Context, req *OrderRequest, side string, priceDecimals int) (*Order, error) {
	body := map[string]any{
		"currency_pair": a.venueSymbol,
		"type":          "limit",
		"account":       "spot",
		"side":          side,
		"amount":        formatSpot(req.Quantity, a.quantityDecimals),
		"price":         formatSpot(req.Price, priceDecimals),
		"time_in_force": "gtc",
	}
	if req.PostOnly {
		body["time_in_force"] = "poc"
	}
	if req.ClientOrderID != "" {
		body["text"] = utils.AddBrokerPrefix("gate", req.ClientOrderID)
	}
	respBody, err := a.gate.DoRequest(ctx, http.MethodPost, "/spot/orders", "", body)
	if err != nil {
		return nil, err
	}
	var result gateSpotOrder
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析 Gate 现货下单响应失败: %w", err)
	}
	return a.convertGateOrder(result), nil
}

func (a *cexSpotAdapter) placeOKXOrder(ctx context.Context, req *OrderRequest, side string, priceDecimals int) (*Order, error) {
	body := map[string]any{
		"instId":  a.venueSymbol,
		"tdMode":  "cash",
		"side":    side,
		"ordType": "limit",
		"sz":      formatSpot(req.Quantity, a.quantityDecimals),
		"px":      formatSpot(req.Price, priceDecimals),
	}
	if req.PostOnly {
		body["ordType"] = "post_only"
	}
	if req.ClientOrderID != "" {
		body["clOrdId"] = encodeSpotClientOrderID(req.ClientOrderID)
	}
	resp, err := a.okx.DoSignedRequest(ctx, http.MethodPost, "/api/v5/trade/order", nil, body)
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
		return nil, fmt.Errorf("解析 OKX 现货下单响应失败: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("OKX 现货下单响应为空")
	}
	if result[0].SCode != "" && result[0].SCode != "0" {
		return nil, fmt.Errorf("OKX 现货下单失败: code=%s, msg=%s", result[0].SCode, result[0].SMsg)
	}
	clientID := decodeSpotClientOrderID(result[0].ClOrdID)
	return a.newOrder(result[0].OrdID, firstNonEmpty(clientID, req.ClientOrderID), Side(strings.ToUpper(side)), req.Price, req.Quantity, 0, 0, OrderStatusNew), nil
}

func (a *cexSpotAdapter) newOrder(remoteID, clientID string, side Side, price, quantity, executedQty, avgPrice float64, status OrderStatus) *Order {
	return &Order{
		OrderID:       a.ids.encode(remoteID),
		ClientOrderID: clientID,
		Symbol:        a.symbol,
		Side:          side,
		Type:          OrderTypeLimit,
		Price:         price,
		Quantity:      quantity,
		ExecutedQty:   executedQty,
		AvgPrice:      avgPrice,
		Status:        status,
		CreatedAt:     time.Now(),
		UpdateTime:    time.Now().UnixMilli(),
	}
}

func (a *cexSpotAdapter) trackOrder(order *Order) {
	if order == nil || order.OrderID == 0 || isFinalSpotStatus(order.Status) {
		return
	}
	a.trackMu.Lock()
	a.tracked[order.OrderID] = ""
	a.trackMu.Unlock()
}

func (a *cexSpotAdapter) untrackOrder(orderID int64) {
	a.trackMu.Lock()
	delete(a.tracked, orderID)
	a.trackMu.Unlock()
}

func (a *cexSpotAdapter) trackedOrderIDs() []int64 {
	a.trackMu.RLock()
	ids := make([]int64, 0, len(a.tracked))
	for id := range a.tracked {
		ids = append(ids, id)
	}
	a.trackMu.RUnlock()
	return ids
}

type bitgetSpotOrder struct {
	OrderID    string `json:"orderId"`
	ClientOID  string `json:"clientOid"`
	Symbol     string `json:"symbol"`
	Side       string `json:"side"`
	OrderType  string `json:"orderType"`
	Price      string `json:"price"`
	Size       string `json:"size"`
	BaseVolume string `json:"baseVolume"`
	FillPrice  string `json:"fillPrice"`
	PriceAvg   string `json:"priceAvg"`
	Status     string `json:"status"`
	CTime      string `json:"cTime"`
	UTime      string `json:"uTime"`
}

func (a *cexSpotAdapter) convertBitgetOrder(item bitgetSpotOrder) *Order {
	status := mapSpotStatus(item.Status)
	return &Order{
		OrderID:       a.ids.encode(item.OrderID),
		ClientOrderID: item.ClientOID,
		Symbol:        normalizeSpotSymbol(firstNonEmpty(item.Symbol, a.symbol)),
		Side:          Side(strings.ToUpper(item.Side)),
		Type:          OrderTypeLimit,
		Price:         parseSpotFloat(item.Price),
		Quantity:      parseSpotFloat(item.Size),
		ExecutedQty:   parseSpotFloat(item.BaseVolume),
		AvgPrice:      parseSpotFloat(firstNonEmpty(item.PriceAvg, item.FillPrice)),
		Status:        status,
		CreatedAt:     time.UnixMilli(parseSpotInt64(item.CTime)),
		UpdateTime:    parseSpotInt64(item.UTime),
	}
}

type bybitSpotOrder struct {
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

func parseBybitSpotOrders(data json.RawMessage) ([]bybitSpotOrder, error) {
	var result struct {
		List []bybitSpotOrder `json:"list"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("解析 Bybit 现货订单失败: %w", err)
	}
	return result.List, nil
}

func (a *cexSpotAdapter) convertBybitOrder(item bybitSpotOrder) *Order {
	return &Order{
		OrderID:       a.ids.encode(item.OrderID),
		ClientOrderID: item.OrderLinkID,
		Symbol:        normalizeSpotSymbol(item.Symbol),
		Side:          Side(strings.ToUpper(item.Side)),
		Type:          OrderTypeLimit,
		Price:         parseSpotFloat(item.Price),
		Quantity:      parseSpotFloat(item.Qty),
		ExecutedQty:   parseSpotFloat(item.CumExecQty),
		AvgPrice:      parseSpotFloat(item.AvgPrice),
		Status:        mapSpotStatus(item.OrderStatus),
		CreatedAt:     time.UnixMilli(parseSpotInt64(item.CreatedTime)),
		UpdateTime:    parseSpotInt64(item.UpdatedTime),
	}
}

type gateSpotOrder struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	CurrencyPair string `json:"currency_pair"`
	Type         string `json:"type"`
	Side         string `json:"side"`
	Amount       string `json:"amount"`
	Price        string `json:"price"`
	FilledAmount string `json:"filled_amount"`
	FilledTotal  string `json:"filled_total"`
	AvgDealPrice string `json:"avg_deal_price"`
	Status       string `json:"status"`
	CreateTimeMS string `json:"create_time_ms"`
	UpdateTimeMS string `json:"update_time_ms"`
}

func (a *cexSpotAdapter) convertGateOrder(item gateSpotOrder) *Order {
	avgPrice := parseSpotFloat(item.AvgDealPrice)
	if avgPrice == 0 && parseSpotFloat(item.FilledAmount) > 0 {
		avgPrice = parseSpotFloat(item.FilledTotal) / parseSpotFloat(item.FilledAmount)
	}
	return &Order{
		OrderID:       a.ids.encode(item.ID),
		ClientOrderID: utils.RemoveBrokerPrefix("gate", item.Text),
		Symbol:        fromGateSpotSymbol(firstNonEmpty(item.CurrencyPair, a.venueSymbol)),
		Side:          Side(strings.ToUpper(item.Side)),
		Type:          OrderTypeLimit,
		Price:         parseSpotFloat(item.Price),
		Quantity:      parseSpotFloat(item.Amount),
		ExecutedQty:   parseSpotFloat(item.FilledAmount),
		AvgPrice:      avgPrice,
		Status:        mapSpotStatus(item.Status),
		CreatedAt:     time.UnixMilli(parseSpotInt64(item.CreateTimeMS)),
		UpdateTime:    parseSpotInt64(item.UpdateTimeMS),
	}
}

type okxSpotOrder struct {
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

func (a *cexSpotAdapter) convertOKXOrder(item okxSpotOrder) *Order {
	return &Order{
		OrderID:       a.ids.encode(item.OrdID),
		ClientOrderID: decodeSpotClientOrderID(item.ClOrdID),
		Symbol:        fromOKXSpotInstrument(firstNonEmpty(item.InstID, a.venueSymbol)),
		Side:          Side(strings.ToUpper(item.Side)),
		Type:          OrderTypeLimit,
		Price:         parseSpotFloat(item.Px),
		Quantity:      parseSpotFloat(item.Sz),
		ExecutedQty:   parseSpotFloat(item.AccFillSz),
		AvgPrice:      parseSpotFloat(item.AvgPx),
		Status:        mapSpotStatus(item.State),
		CreatedAt:     time.UnixMilli(parseSpotInt64(item.CTime)),
		UpdateTime:    parseSpotInt64(item.UTime),
	}
}

func fetchPublicSpotJSON(ctx context.Context, url string, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dst)
}

func normalizeSpotSymbol(symbol string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(symbol), "/", ""), "_", ""))
}

func splitSpotSymbol(symbol string) (string, string) {
	symbol = normalizeSpotSymbol(symbol)
	for _, quote := range []string{"USDT", "USDC", "USD", "BTC", "ETH"} {
		if strings.HasSuffix(symbol, quote) && len(symbol) > len(quote) {
			return symbol[:len(symbol)-len(quote)], quote
		}
	}
	return symbol, "USDT"
}

func toGateSpotSymbol(symbol string) string {
	base, quote := splitSpotSymbol(symbol)
	return base + "_" + quote
}

func fromGateSpotSymbol(symbol string) string {
	return normalizeSpotSymbol(symbol)
}

func toOKXSpotInstrument(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if strings.Count(symbol, "-") == 1 {
		return symbol
	}
	base, quote := splitSpotSymbol(symbol)
	return base + "-" + quote
}

func fromOKXSpotInstrument(instID string) string {
	return normalizeSpotSymbol(instID)
}

func decimalsFromSpotStep(step string, fallback int) int {
	step = strings.TrimSpace(step)
	if step == "" {
		return fallback
	}
	if strings.Contains(step, "e-") || strings.Contains(step, "E-") {
		parts := strings.FieldsFunc(step, func(r rune) bool { return r == '-' })
		if len(parts) > 1 {
			return parseSpotInt(parts[len(parts)-1], fallback)
		}
	}
	if !strings.Contains(step, ".") {
		return 0
	}
	trimmed := strings.TrimRight(step, "0")
	parts := strings.Split(trimmed, ".")
	if len(parts) != 2 {
		return fallback
	}
	return len(parts[1])
}

func formatSpot(value float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	scale := math.Pow10(decimals)
	value = math.Floor(value*scale) / scale
	return strconv.FormatFloat(value, 'f', decimals, 64)
}

func parseSpotFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed
}

func parseSpotInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseSpotInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func mapSpotStatus(status string) OrderStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "new", "live", "open", "partially_filled", "partial-fill", "partial_fill":
		if strings.Contains(strings.ToLower(status), "partial") {
			return OrderStatusPartiallyFilled
		}
		return OrderStatusNew
	case "filled", "fill", "full-fill", "full_fill", "closed":
		return OrderStatusFilled
	case "cancelled", "canceled", "cancel", "rejected":
		return OrderStatusCanceled
	default:
		return OrderStatusNew
	}
}

func isFinalSpotStatus(status OrderStatus) bool {
	return status == OrderStatusFilled || status == OrderStatusCanceled || status == OrderStatusRejected || status == OrderStatusExpired
}

func isSpotBalanceError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "insufficient") || strings.Contains(msg, "balance") || strings.Contains(msg, "资金不足") || strings.Contains(msg, "余额")
}

func isOrderGoneError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "unknown order") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "order_not_found")
}

func bybitSpotInterval(interval string) string {
	switch interval {
	case "1m":
		return "1"
	case "3m":
		return "3"
	case "5m":
		return "5"
	case "15m":
		return "15"
	case "30m":
		return "30"
	case "1h", "1H":
		return "60"
	case "4h", "4H":
		return "240"
	case "1d", "1D":
		return "D"
	default:
		return strings.TrimSuffix(interval, "m")
	}
}

func okxSpotInterval(interval string) string {
	switch interval {
	case "1h":
		return "1H"
	case "4h":
		return "4H"
	case "1d":
		return "1D"
	default:
		return interval
	}
}

func gateSpotInterval(interval string) string {
	switch interval {
	case "1m", "5m", "15m", "30m", "1h", "4h", "8h", "1d", "7d":
		return interval
	case "1H":
		return "1h"
	case "4H":
		return "4h"
	case "1D":
		return "1d"
	default:
		return "1m"
	}
}

func bitgetSpotInterval(interval string) string {
	switch interval {
	case "1m":
		return "1min"
	case "5m":
		return "5min"
	case "15m":
		return "15min"
	case "30m":
		return "30min"
	case "1h", "1H":
		return "1h"
	case "4h", "4H":
		return "4h"
	case "1d", "1D":
		return "1day"
	default:
		return "1min"
	}
}

func candlesFromRows(symbol string, rows [][]string, reverse bool) []*Candle {
	candles := make([]*Candle, 0, len(rows))
	appendRow := func(row []string) {
		if len(row) < 6 {
			return
		}
		candles = append(candles, &Candle{
			Symbol:    normalizeSpotSymbol(symbol),
			Timestamp: parseSpotInt64(row[0]),
			Open:      parseSpotFloat(row[1]),
			High:      parseSpotFloat(row[2]),
			Low:       parseSpotFloat(row[3]),
			Close:     parseSpotFloat(row[4]),
			Volume:    parseSpotFloat(row[5]),
			IsClosed:  true,
		})
	}
	if reverse {
		for i := len(rows) - 1; i >= 0; i-- {
			appendRow(rows[i])
		}
		return candles
	}
	for _, row := range rows {
		appendRow(row)
	}
	return candles
}

func encodeSpotClientOrderID(clientOrderID string) string {
	encoded := strings.ReplaceAll(clientOrderID, "_", "X")
	if len(encoded) > 32 {
		encoded = encoded[:32]
	}
	return encoded
}

func decodeSpotClientOrderID(clientOrderID string) string {
	if strings.Contains(clientOrderID, "X") {
		return strings.ReplaceAll(clientOrderID, "X", "_")
	}
	return clientOrderID
}
