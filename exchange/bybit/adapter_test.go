package bybit

import "testing"

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
