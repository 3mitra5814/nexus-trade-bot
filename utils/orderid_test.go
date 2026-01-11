package utils

import "testing"

func TestGenerateOrderIDRoundTrip(t *testing.T) {
	id := GenerateOrderID(3027.11, "BUY", "LONG", 2)
	if len(id) > 26 {
		t.Fatalf("generated id should fit broker-prefixed exchange limits, got %q len=%d", id, len(id))
	}
	price, side, book, _, ok := ParseOrderID(id, 2)
	if !ok {
		t.Fatalf("failed to parse generated id %q", id)
	}
	if price != 3027.11 || side != "BUY" || book != "LONG" {
		t.Fatalf("parsed mismatch: price=%v side=%s book=%s id=%s", price, side, book, id)
	}
}

func TestGenerateOrderIDWithTagRoundTrip(t *testing.T) {
	id := GenerateOrderIDWithTag(3027.11, "SELL", "SHORT", 2, "Bot_ABC123")
	if len(id) > 26 {
		t.Fatalf("tagged id should fit broker-prefixed exchange limits, got %q len=%d", id, len(id))
	}
	price, side, book, _, tag, ok := ParseOrderIDWithTag(id, 2)
	if !ok {
		t.Fatalf("failed to parse generated id %q", id)
	}
	if price != 3027.11 || side != "SELL" || book != "SHORT" || tag != "botabc" {
		t.Fatalf("parsed mismatch: price=%v side=%s book=%s tag=%s id=%s", price, side, book, tag, id)
	}
}

func TestParseLegacyOrderID(t *testing.T) {
	price, side, book, _, ok := ParseOrderID("302711_B_L_1765101259001", 2)
	if !ok {
		t.Fatal("legacy decimal order id should still parse")
	}
	if price != 3027.11 || side != "BUY" || book != "LONG" {
		t.Fatalf("parsed legacy mismatch: price=%v side=%s book=%s", price, side, book)
	}
}

func TestRemoveBrokerPrefixAcceptsDisplayNames(t *testing.T) {
	if got := RemoveBrokerPrefix("Binance Spot", "x-zdfVM8vYabc"); got != "abc" {
		t.Fatalf("binance display name prefix removal = %q", got)
	}
	if got := RemoveBrokerPrefix("Gate.io", "t-abc"); got != "abc" {
		t.Fatalf("gate display name prefix removal = %q", got)
	}
}
