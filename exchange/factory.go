package exchange

import (
	"fmt"
	"nexus-trade-bot/config"
	"nexus-trade-bot/exchange/binance"
	"nexus-trade-bot/exchange/bitget"
	"nexus-trade-bot/exchange/bybit"
	"nexus-trade-bot/exchange/gate"
	hyperliquidex "nexus-trade-bot/exchange/hyperliquid"
	"nexus-trade-bot/exchange/okx"
)

// NewExchange 创建交易所实例
func NewExchange(cfg *config.Config) (IExchange, error) {
	exchangeName := config.NormalizeExchangeName(cfg.App.CurrentExchange)
	if exchangeName == "" {
		return nil, fmt.Errorf("不支持的交易所: %s", cfg.App.CurrentExchange)
	}
	marketType := cfg.App.MarketType
	if marketType == "" {
		marketType = "futures"
	}
	cfg.App.CurrentExchange = exchangeName
	if cfg.Exchanges != nil {
		if _, exists := cfg.Exchanges[exchangeName]; !exists {
			for name, exchangeCfg := range cfg.Exchanges {
				if config.NormalizeExchangeName(name) == exchangeName {
					cfg.Exchanges[exchangeName] = exchangeCfg
					break
				}
			}
		}
	}

	switch exchangeName {
	case "bitget":
		exchangeCfg, exists := cfg.Exchanges["bitget"]
		if !exists {
			return nil, fmt.Errorf("bitget 配置不存在")
		}
		// 将 ExchangeConfig 转换为 map[string]string
		cfgMap := map[string]string{
			"api_key":    exchangeCfg.APIKey,
			"secret_key": exchangeCfg.SecretKey,
			"passphrase": exchangeCfg.Passphrase,
		}
		if marketType == "spot" {
			return newCEXSpotAdapter("bitget", cfgMap, cfg.Trading.Symbol)
		}
		adapter, err := bitget.NewBitgetAdapter(cfgMap, cfg.Trading.Symbol)
		if err != nil {
			return nil, err
		}
		return &bitgetWrapper{adapter: adapter}, nil

	case "binance":
		exchangeCfg, exists := cfg.Exchanges["binance"]
		if !exists {
			return nil, fmt.Errorf("binance 配置不存在")
		}
		cfgMap := map[string]string{
			"api_key":    exchangeCfg.APIKey,
			"secret_key": exchangeCfg.SecretKey,
		}
		if marketType == "spot" {
			adapter, err := binance.NewBinanceSpotAdapter(cfgMap, cfg.Trading.Symbol)
			if err != nil {
				return nil, err
			}
			return &binanceWrapper{adapter: adapter}, nil
		}
		adapter, err := binance.NewBinanceAdapter(cfgMap, cfg.Trading.Symbol)
		if err != nil {
			return nil, err
		}
		return &binanceWrapper{adapter: adapter}, nil

	case "gate":
		exchangeCfg, exists := cfg.Exchanges["gate"]
		if !exists {
			return nil, fmt.Errorf("gate 配置不存在")
		}
		cfgMap := map[string]string{
			"api_key":    exchangeCfg.APIKey,
			"secret_key": exchangeCfg.SecretKey,
			"settle":     "usdt", // 默认 USDT 永续合约
		}
		if marketType == "spot" {
			return newCEXSpotAdapter("gate", cfgMap, cfg.Trading.Symbol)
		}
		adapter, err := gate.NewGateAdapter(cfgMap, cfg.Trading.Symbol)
		if err != nil {
			return nil, err
		}
		return &gateWrapper{adapter: adapter}, nil

	case "bybit":
		exchangeCfg, exists := cfg.Exchanges["bybit"]
		if !exists {
			return nil, fmt.Errorf("bybit 配置不存在")
		}
		cfgMap := map[string]string{
			"api_key":    exchangeCfg.APIKey,
			"secret_key": exchangeCfg.SecretKey,
		}
		if marketType == "spot" {
			return newCEXSpotAdapter("bybit", cfgMap, cfg.Trading.Symbol)
		}
		adapter, err := bybit.NewBybitAdapter(cfgMap, cfg.Trading.Symbol)
		if err != nil {
			return nil, err
		}
		return &bybitWrapper{adapter: adapter}, nil

	case "okx":
		exchangeCfg, exists := cfg.Exchanges["okx"]
		if !exists {
			return nil, fmt.Errorf("okx 配置不存在")
		}
		cfgMap := map[string]string{
			"api_key":    exchangeCfg.APIKey,
			"secret_key": exchangeCfg.SecretKey,
			"passphrase": exchangeCfg.Passphrase,
		}
		if marketType == "spot" {
			return newCEXSpotAdapter("okx", cfgMap, cfg.Trading.Symbol)
		}
		adapter, err := okx.NewOKXAdapter(cfgMap, cfg.Trading.Symbol)
		if err != nil {
			return nil, err
		}
		return &okxWrapper{adapter: adapter}, nil

	case "hyperliquid":
		exchangeCfg, exists := cfg.Exchanges["hyperliquid"]
		if !exists {
			return nil, fmt.Errorf("hyperliquid 配置不存在")
		}
		cfgMap := map[string]string{
			"api_key":    exchangeCfg.APIKey,
			"secret_key": exchangeCfg.SecretKey,
		}
		if marketType == "spot" {
			adapter, err := hyperliquidex.NewHyperliquidSpotAdapter(cfgMap, cfg.Trading.Symbol)
			if err != nil {
				return nil, err
			}
			return &hyperliquidWrapper{adapter: adapter}, nil
		}
		adapter, err := hyperliquidex.NewHyperliquidAdapter(cfgMap, cfg.Trading.Symbol)
		if err != nil {
			return nil, err
		}
		return &hyperliquidWrapper{adapter: adapter}, nil

	default:
		return nil, fmt.Errorf("不支持的交易所: %s", exchangeName)
	}
}
