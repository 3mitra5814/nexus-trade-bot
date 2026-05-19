package bybit

import (
	"errors"
	"testing"
)

func TestPositionIdxForBybitOrder(t *testing.T) {
	tests := []struct {
		name       string
		hedgeMode  bool
		side       Side
		reduceOnly bool
		want       int
	}{
		{name: "one way", hedgeMode: false, side: SideBuy, reduceOnly: false, want: 0},
		{name: "open long", hedgeMode: true, side: SideBuy, reduceOnly: false, want: 1},
		{name: "close long", hedgeMode: true, side: SideSell, reduceOnly: true, want: 1},
		{name: "open short", hedgeMode: true, side: SideSell, reduceOnly: false, want: 2},
		{name: "close short", hedgeMode: true, side: SideBuy, reduceOnly: true, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := positionIdxForBybitOrder(tt.hedgeMode, tt.side, tt.reduceOnly); got != tt.want {
				t.Fatalf("positionIdxForBybitOrder() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBybitCancelOrderGoneErrorClassification(t *testing.T) {
	tests := []error{
		errors.New("Bybit API 错误: code=110001, msg=order not exists or too late to cancel"),
		errors.New("order does not exist"),
		errors.New("already filled"),
	}
	for _, err := range tests {
		if !isBybitCancelOrderGoneError(err) {
			t.Fatalf("expected cancel gone error: %v", err)
		}
	}
	if isBybitCancelOrderGoneError(errors.New("Bybit API 错误: code=10006, msg=Too many visits")) {
		t.Fatalf("rate limit error must not be treated as cancel success")
	}
}
