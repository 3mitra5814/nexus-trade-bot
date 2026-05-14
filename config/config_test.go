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
