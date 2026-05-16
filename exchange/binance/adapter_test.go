package binance

import (
	"testing"

	"github.com/adshao/go-binance/v2/futures"
)

func TestBinancePositionSide(t *testing.T) {
	tests := []struct {
		name       string
		side       Side
		reduceOnly bool
		want       futures.PositionSideType
	}{
		{name: "open long", side: SideBuy, reduceOnly: false, want: futures.PositionSideTypeLong},
		{name: "close long", side: SideSell, reduceOnly: true, want: futures.PositionSideTypeLong},
		{name: "open short", side: SideSell, reduceOnly: false, want: futures.PositionSideTypeShort},
		{name: "close short", side: SideBuy, reduceOnly: true, want: futures.PositionSideTypeShort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := binancePositionSide(tt.side, tt.reduceOnly); got != tt.want {
				t.Fatalf("binancePositionSide() = %q, want %q", got, tt.want)
			}
		})
	}
}
