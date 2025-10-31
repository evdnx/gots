package strategy

import (
	"testing"

	"github.com/evdnx/gots/types"
)

func TestEventDriven_NoEvent(t *testing.T) {
	ev, exec := buildEventDriven(t, 1000, 5)

	// Feed bars – even if HMA crosses, eventActive is false.
	var bars []candle
	for i := 0; i < 12; i++ {
		price := 100.0 + float64(i)
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, ev, bars)

	if len(exec.Orders()) != 0 {
		t.Fatalf("expected no orders when eventActive is false, got %d", len(exec.Orders()))
	}
}

func TestEventDriven_LongEntry(t *testing.T) {
	ev, exec := buildEventDriven(t, 0.5, 5)
	ev.SetEventActive(true)

	// Upward ramp → bullish HMA + ATSO magnitude > 0.5
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1200,
		})
	}
	feedBars(t, ev, bars)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one BUY order after event‑driven long entry, got %d", len(exec.Orders()))
	}
	o := exec.Orders()[0]
	if o.Side != types.Buy {
		t.Fatalf("expected BUY, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("quantity must be positive, got %f", o.Qty)
	}
}

func TestEventDriven_ShortEntry(t *testing.T) {
	ev, exec := buildEventDriven(t, 0.5, 5)
	ev.SetEventActive(true)

	// Downward ramp → bearish HMA + ATSO magnitude > 0.5
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 115.0 - float64(i)
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1200,
		})
	}
	feedBars(t, ev, bars)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one SELL order after event‑driven short entry, got %d", len(exec.Orders()))
	}
	o := exec.Orders()[0]
	if o.Side != types.Sell {
		t.Fatalf("expected SELL, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("quantity must be positive, got %f", o.Qty)
	}
}

func TestEventDriven_TrailingStop(t *testing.T) {
	ev, exec := buildEventDriven(t, 0.5, 5)
	ev.SetEventActive(true)
	ev.Cfg.TrailingPct = 0.02 // 2 %

	// ---- entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1200,
		})
	}
	feedBars(t, ev, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entry := exec.Orders()[0].Price

	// ---- price crosses trailing level ----
	trail := entry * 1.02
	ev.ProcessBar(trail+0.5, trail-0.5, trail+0.1, 1300)

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected trailing‑stop close order, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL to close trailing stop, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[1].Price < trail {
		t.Fatalf("trailing‑stop price %f below expected %f", exec.Orders()[1].Price, trail)
	}
}

func TestEventDriven_TakeProfit(t *testing.T) {
	ev, exec := buildEventDriven(t, 0.5, 5)
	ev.SetEventActive(true)
	ev.Cfg.TakeProfitPct = 2.0 // ATR‑multiple TP

	// ---- entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1200,
		})
	}
	feedBars(t, ev, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entry := exec.Orders()[0].Price

	// TP = entry + 2*ATR (ATSO≈2 for this series → TP ≈ entry+4)
	tp := entry + 4.0
	ev.ProcessBar(tp+0.5, tp-0.5, tp+0.1, 1400)

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected TP close order, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL to close TP, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[1].Price < tp {
		t.Fatalf("TP price %f below expected %f", exec.Orders()[1].Price, tp)
	}
}

func TestEventDriven_MaxHoldingBars(t *testing.T) {
	const maxBars = 3
	ev, exec := buildEventDriven(t, 0.5, maxBars)
	ev.SetEventActive(true)

	// ---- entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1200,
		})
	}
	feedBars(t, ev, up)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected entry order, got %d", len(exec.Orders()))
	}
	// Feed exactly maxBars flat bars.
	var flat []candle
	for i := 0; i < maxBars; i++ {
		price := exec.Orders()[0].Price
		flat = append(flat, candle{
			high:   price + 0.2,
			low:    price - 0.2,
			close:  price,
			volume: 1100,
		})
	}
	feedBars(t, ev, flat)

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected forced close after maxHoldingBars, got %d", len(exec.Orders()))
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL to close position after maxHoldingBars, got %s", exec.Orders()[1].Side)
	}
}

/*
-----------------------------------------------------------------------
Test 7 – Deactivating the event flag closes any open position.
-----------------------------------------------------------------------
1️⃣ Open a long (event active + bullish HMA).
2️⃣ Call `SetEventActive(false)` – the strategy should instantly close

	the position.
*/
func TestEventDriven_EventDeactivationCloses(t *testing.T) {
	// Build the strategy with a low threshold so the ATSO magnitude condition
	// will be satisfied by the price series we feed.
	ev, exec := buildEventDriven(t, 0.5, 5)

	// Activate the external event.
	ev.SetEventActive(true)

	// ---- Phase 1 – long entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i) // monotonic rise → bullish HMA
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1200,
		})
	}
	feedBars(t, ev, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}

	// ---- Deactivate the external event flag ----
	ev.SetEventActive(false)

	// The deactivation routine should have issued a close order.
	if len(exec.Orders()) != 2 {
		t.Fatalf("expected a second order (close on deactivation), got %d", len(exec.Orders()))
	}
	closeOrder := exec.Orders()[1]
	if closeOrder.Side != types.Sell {
		t.Fatalf("expected SELL order to close position on deactivation, got %s", closeOrder.Side)
	}
	// The close price should be the most recent close (the last bar we fed).
	lastClose := up[len(up)-1].close
	if closeOrder.Price != lastClose {
		t.Fatalf("expected close order price %f (last close), got %f", lastClose, closeOrder.Price)
	}
}
