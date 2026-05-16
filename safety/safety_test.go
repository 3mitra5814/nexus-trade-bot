package safety

import (
	"context"
	"strings"
	"testing"

	"nexus-trade-bot/exchange"
)

type safetyFakeExchange struct {
	account          *exchange.Account
	positions        []*exchange.Position
	modeErrDirection string
	modeErr          error
}

func (e *safetyFakeExchange) GetName() string { return "Fake" }
func (e *safetyFakeExchange) PlaceOrder(context.Context, *exchange.OrderRequest) (*exchange.Order, error) {
	return nil, nil
}
func (e *safetyFakeExchange) BatchPlaceOrders(context.Context, []*exchange.OrderRequest) ([]*exchange.Order, bool) {
	return nil, false
}
func (e *safetyFakeExchange) CancelOrder(context.Context, string, int64) error { return nil }
func (e *safetyFakeExchange) BatchCancelOrders(context.Context, string, []int64) error {
	return nil
}
func (e *safetyFakeExchange) CancelAllOrders(context.Context, string) error { return nil }
func (e *safetyFakeExchange) GetOrder(context.Context, string, int64) (*exchange.Order, error) {
	return nil, nil
}
func (e *safetyFakeExchange) GetOpenOrders(context.Context, string) ([]*exchange.Order, error) {
	return nil, nil
}
func (e *safetyFakeExchange) GetAccount(context.Context) (*exchange.Account, error) {
	if e.account != nil {
		return e.account, nil
	}
	return &exchange.Account{AvailableBalance: 30, AccountLeverage: 1}, nil
}
func (e *safetyFakeExchange) GetPositions(context.Context, string) ([]*exchange.Position, error) {
	return e.positions, nil
}
func (e *safetyFakeExchange) GetBalance(context.Context, string) (float64, error) { return 0, nil }
func (e *safetyFakeExchange) StartOrderStream(context.Context, func(interface{})) error {
	return nil
}
func (e *safetyFakeExchange) StopOrderStream() error { return nil }
func (e *safetyFakeExchange) GetLatestPrice(context.Context, string) (float64, error) {
	return 100, nil
}
func (e *safetyFakeExchange) StartPriceStream(context.Context, string, func(float64)) error {
	return nil
}
func (e *safetyFakeExchange) StartKlineStream(context.Context, []string, string, exchange.CandleUpdateCallback) error {
	return nil
}
func (e *safetyFakeExchange) StopKlineStream() error { return nil }
func (e *safetyFakeExchange) GetHistoricalKlines(context.Context, string, string, int) ([]*exchange.Candle, error) {
	return nil, nil
}
func (e *safetyFakeExchange) GetPriceDecimals() int    { return 2 }
func (e *safetyFakeExchange) GetQuantityDecimals() int { return 3 }
func (e *safetyFakeExchange) GetBaseAsset() string     { return "ETH" }
func (e *safetyFakeExchange) GetQuoteAsset() string    { return "USDT" }
func (e *safetyFakeExchange) ValidatePositionMode(ctx context.Context, direction string) error {
	e.modeErrDirection = direction
	return e.modeErr
}

func TestCheckAccountSafetyCountsBothLongAndShortExistingPositions(t *testing.T) {
	ex := &safetyFakeExchange{
		account: &exchange.Account{AvailableBalance: 30, AccountLeverage: 1},
		positions: []*exchange.Position{
			{Symbol: "ETHUSDT", Size: 1, Leverage: 1},
			{Symbol: "ETHUSDT", Size: -1, Leverage: 1},
		},
	}

	err := CheckAccountSafety(ex, "ETHUSDT", 100, 10, 2, 0.0002, 1, 2, "neutral")
	if err == nil || !strings.Contains(err.Error(), "余额不足") {
		t.Fatalf("expected combined long/short positions to fail margin budget, got %v", err)
	}
}

func TestCheckAccountSafetyDelegatesPositionModeValidation(t *testing.T) {
	modeErr := context.DeadlineExceeded
	ex := &safetyFakeExchange{
		account: &exchange.Account{AvailableBalance: 1000, AccountLeverage: 1},
		modeErr: modeErr,
	}

	err := CheckAccountSafety(ex, "ETHUSDT", 100, 10, 2, 0.0002, 1, 2, "neutral")
	if err != modeErr {
		t.Fatalf("expected position mode validation error, got %v", err)
	}
	if ex.modeErrDirection != "neutral" {
		t.Fatalf("expected direction to be passed to position mode validator, got %q", ex.modeErrDirection)
	}
}

var _ exchange.IExchange = (*safetyFakeExchange)(nil)
var _ positionModeChecker = (*safetyFakeExchange)(nil)
