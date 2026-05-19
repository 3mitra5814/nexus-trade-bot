package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	cfg := &Config{}
	cfg.App.CurrentExchange = "binance"
	cfg.Exchanges = map[string]ExchangeConfig{
		"binance": {APIKey: "key", SecretKey: "secret", FeeRate: 0.0002},
	}
	cfg.Trading.Symbol = "BTCUSDT"
	cfg.Trading.Direction = "long"
	cfg.Trading.OrderQuantity = 20
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.PriceInterval = 1
	return cfg
}

func TestValidateDefaultsToFutures(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.App.MarketType != "futures" {
		t.Fatalf("expected default market type futures, got %q", cfg.App.MarketType)
	}
}

func TestValidateNormalizesExchangeNameAndConfigKey(t *testing.T) {
	cfg := validConfig()
	cfg.App.CurrentExchange = " Gate.io "
	cfg.Exchanges = map[string]ExchangeConfig{
		"Gate.io": {APIKey: "key", SecretKey: "secret", FeeRate: 0.0002},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.App.CurrentExchange != "gate" {
		t.Fatalf("expected exchange gate, got %q", cfg.App.CurrentExchange)
	}
	if _, ok := cfg.Exchanges["gate"]; !ok {
		t.Fatalf("expected normalized gate config key, got %#v", cfg.Exchanges)
	}
}

func TestValidateRejectsDuplicateNormalizedExchangeKeys(t *testing.T) {
	cfg := validConfig()
	cfg.App.CurrentExchange = "gate"
	cfg.Exchanges = map[string]ExchangeConfig{
		"gate":    {APIKey: "key", SecretKey: "secret", FeeRate: 0.0002},
		"Gate.io": {APIKey: "key2", SecretKey: "secret2", FeeRate: 0.0002},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "配置重复") {
		t.Fatalf("expected duplicate exchange config error, got %v", err)
	}
}

func TestValidateRejectsSpotShort(t *testing.T) {
	cfg := validConfig()
	cfg.App.MarketType = "spot"
	cfg.Trading.Direction = "short"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "现货模式仅支持 long") {
		t.Fatalf("expected spot short validation error, got %v", err)
	}
}

func TestValidateNormalizesDirectionCase(t *testing.T) {
	cfg := validConfig()
	cfg.Trading.Direction = " LONG "
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Trading.Direction != "long" {
		t.Fatalf("expected normalized direction long, got %q", cfg.Trading.Direction)
	}
}

func TestValidateAcceptsClassicMode(t *testing.T) {
	cfg := validConfig()
	cfg.Trading.Mode = " classic "
	cfg.App.MarketType = "spot"
	cfg.Trading.Direction = "long"
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Trading.Mode != "classic" {
		t.Fatalf("expected normalized mode classic, got %q", cfg.Trading.Mode)
	}
	if cfg.App.MarketType != "futures" || cfg.Trading.Direction != "neutral" {
		t.Fatalf("expected classic mode to force futures/neutral, got market=%q direction=%q", cfg.App.MarketType, cfg.Trading.Direction)
	}
	if cfg.Trading.BuyWindowSize != ClassicGridWindowSize || cfg.Trading.SellWindowSize != ClassicGridWindowSize {
		t.Fatalf("expected classic mode to force %d/%d windows, got buy=%d sell=%d",
			ClassicGridWindowSize, ClassicGridWindowSize, cfg.Trading.BuyWindowSize, cfg.Trading.SellWindowSize)
	}
}

func TestValidateRejectsHyperliquidClassicMode(t *testing.T) {
	cfg := validConfig()
	cfg.App.CurrentExchange = "hyperliquid"
	cfg.Exchanges = map[string]ExchangeConfig{
		"hyperliquid": {APIKey: "wallet", SecretKey: "secret", FeeRate: 0.0002},
	}
	cfg.Trading.Mode = "classic"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "Hyperliquid") {
		t.Fatalf("expected Hyperliquid classic mode validation error, got %v", err)
	}
}

func TestValidateClassicTargetCapacityUsesSingleHundredOrderBook(t *testing.T) {
	cfg := validConfig()
	cfg.Trading.Mode = "classic"
	cfg.Trading.BuyWindowSize = 10
	cfg.Trading.SellWindowSize = 10
	cfg.Trading.OrderCleanupThreshold = 1

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got := TargetOrderCapacity(cfg); got != 100 {
		t.Fatalf("expected classic target capacity 100, got %d", got)
	}
	if cfg.Trading.OrderCleanupThreshold != 100 {
		t.Fatalf("expected classic cleanup threshold to be raised to 100, got %d", cfg.Trading.OrderCleanupThreshold)
	}
}

func TestValidateDefaultsExchangeFeeRate(t *testing.T) {
	cfg := validConfig()
	exchangeCfg := cfg.Exchanges["binance"]
	exchangeCfg.FeeRate = 0
	cfg.Exchanges["binance"] = exchangeCfg

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Exchanges["binance"].FeeRate != DefaultFeeRate {
		t.Fatalf("expected default fee rate %.8f, got %.8f", DefaultFeeRate, cfg.Exchanges["binance"].FeeRate)
	}
}

func TestValidateClampsDefaultRecoveryThresholdToMonitorSymbolCount(t *testing.T) {
	cfg := validConfig()
	cfg.RiskControl.MonitorSymbols = []string{"BTCUSDT"}
	cfg.RiskControl.RecoveryThreshold = 0

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.RiskControl.RecoveryThreshold != 1 {
		t.Fatalf("expected recovery threshold to be clamped to 1, got %d", cfg.RiskControl.RecoveryThreshold)
	}
}

func TestValidateRaisesNeutralOrderCleanupThresholdToDualBookCapacity(t *testing.T) {
	cfg := validConfig()
	cfg.Trading.Direction = "neutral"
	cfg.Trading.BuyWindowSize = 4
	cfg.Trading.SellWindowSize = 3
	cfg.Trading.OrderCleanupThreshold = 7

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Trading.OrderCleanupThreshold != 14 {
		t.Fatalf("expected neutral cleanup threshold to cover both books, got %d", cfg.Trading.OrderCleanupThreshold)
	}
}
