package hyperliquidex

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidatePositionModeRejectsNeutralFutures(t *testing.T) {
	adapter := &HyperliquidAdapter{marketType: "futures"}

	err := adapter.ValidatePositionMode(context.Background(), "neutral")
	if err == nil || !strings.Contains(err.Error(), "中性") {
		t.Fatalf("expected neutral futures mode to be rejected, got %v", err)
	}
}

func TestValidatePositionModeAllowsDirectionalFutures(t *testing.T) {
	adapter := &HyperliquidAdapter{marketType: "futures"}
	for _, direction := range []string{"long", "short"} {
		if err := adapter.ValidatePositionMode(context.Background(), direction); err != nil {
			t.Fatalf("direction %s should be allowed, got %v", direction, err)
		}
	}
}

func TestHyperliquidCancelOrderGoneErrorClassification(t *testing.T) {
	err := errors.New("Order was never placed, already canceled, or filled. asset=173")
	if !isHyperliquidCancelOrderGoneError(err) {
		t.Fatalf("expected Hyperliquid stale cancel error to be ignored: %v", err)
	}
	if isHyperliquidCancelOrderGoneError(errors.New("insufficient margin")) {
		t.Fatal("margin error must not be treated as cancel success")
	}
}
