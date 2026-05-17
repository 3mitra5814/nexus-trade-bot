package main

import "testing"

func TestAdjustRequestSchedulerPreservesRebalanceWhenQueueIsFull(t *testing.T) {
	scheduler := newAdjustRequestScheduler()

	scheduler.Schedule("order_update", false)
	scheduler.Schedule("price-grid-shift", true)

	select {
	case <-scheduler.Signals():
	default:
		t.Fatal("expected pending adjust signal")
	}

	req, ok := scheduler.Pop()
	if !ok {
		t.Fatal("expected pending adjust request")
	}
	if !req.allowWindowRebalance {
		t.Fatalf("merged adjust request lost rebalance=true: %+v", req)
	}
	if req.reason != "order_update+price-grid-shift" {
		t.Fatalf("unexpected merged reason %q", req.reason)
	}
}

func TestAdjustRequestSchedulerSignalsAgainAfterPop(t *testing.T) {
	scheduler := newAdjustRequestScheduler()

	scheduler.Schedule("initial", true)
	<-scheduler.Signals()
	if _, ok := scheduler.Pop(); !ok {
		t.Fatal("expected first request")
	}

	scheduler.Schedule("order_update", false)
	select {
	case <-scheduler.Signals():
	default:
		t.Fatal("expected second signal after first request was popped")
	}
	req, ok := scheduler.Pop()
	if !ok {
		t.Fatal("expected second request")
	}
	if req.reason != "order_update" || req.allowWindowRebalance {
		t.Fatalf("unexpected second request: %+v", req)
	}
}
