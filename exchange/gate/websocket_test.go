package gate

import "testing"

func TestConvertStatusUsesFinishReason(t *testing.T) {
	if got := convertStatus("finished", "cancelled", 0, 10); got != OrderStatus("CANCELED") {
		t.Fatalf("cancelled finished order = %s, want CANCELED", got)
	}
	if got := convertStatus("finished", "filled", 10, 10); got != OrderStatus("FILLED") {
		t.Fatalf("filled order = %s, want FILLED", got)
	}
	if got := convertStatus("finished", "ioc", 10, 10); got != OrderStatus("FILLED") {
		t.Fatalf("fully executed ioc order = %s, want FILLED", got)
	}
	if got := convertStatus("finished", "ioc", 3, 10); got != OrderStatus("CANCELED") {
		t.Fatalf("partially executed ioc final state = %s, want CANCELED", got)
	}
}
