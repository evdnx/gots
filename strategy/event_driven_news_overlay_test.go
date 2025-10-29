package strategy

import (
	"testing"

	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/testutils"
	"github.com/evdnx/gots/types"
)

// feedBars sends a slice of candles to the supplied EventDriven instance.
func feedBarsED(t *testing.T, ev *EventDriven, bars []candle) {
	for _, b := range bars {
		ev.ProcessBar(b.high, b.low, b.close, b.volume)
	}
}

// buildEventDriven creates an EventDriven strategy wired to a mock executor
// and logger.  The suiteFactory returns a *real* goti.IndicatorSuite.
// All oscillator thresholds are set to extreme values so the RSI/MFI checks
// never block a trade – the only gating factor is the HMA crossover and
// the ATSO magnitude (the “eventThreshold” argument).
func buildEventDriven(t *testing.T,
	eventThreshold float64, maxHoldingBars int) (*EventDriven, *testutils.MockExecutor) {

	// Extremely permissive thresholds – they will never reject a trade.
	cfg := config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		HMAPeriod:         9,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.01,  // 1 % of equity per trade
		StopLossPct:       0.015, // 1.5 %
		TakeProfitPct:     0.0,   // enable per‑test when needed
		TrailingPct:       0.0,   // enable per‑test when needed
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}

	mockExec := testutils.NewMockExecutor(10_000) // $10 k start equity
	mockLog := testutils.NewMockLogger()

	// The EventDriven constructor internally creates its own IndicatorSuite,
	// so we don’t need a separate suiteFactory here.
	ev, err := NewEventDriven(
		"TEST", cfg, mockExec, mockLog,
		eventThreshold, maxHoldingBars,
	)
	if err != nil {
		t.Fatalf("NewEventDriven failed: %v", err)
	}
	return ev, mockExec
}

/*
-----------------------------------------------------------------------
Test 1 – No external event → strategy stays idle, no orders.
-----------------------------------------------------------------------
*/
func TestEventDriven_NoEvent(t *testing.T) {
	// Use a high threshold so the ATSO magnitude never exceeds it.
	ev, exec := buildEventDriven(t, 1000, 5)

	// Feed a few bars – even if HMA crosses, the event flag is false.
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
	feedBarsED(t, ev, bars)

	if len(exec.Orders()) != 0 {
		t.Fatalf("expected no orders when eventActive is false, got %d", len(exec.Orders()))
	}
}

/*
-----------------------------------------------------------------------
Test 2 – Event active + bullish HMA + ATSO magnitude above threshold → LONG.
-----------------------------------------------------------------------
*/
func TestEventDriven_LongEntry(t *testing.T) {
	// Low threshold so the ATSO magnitude condition is easily met.
	ev, exec := buildEventDriven(t, 0.5, 5)

	// Activate the external event.
	ev.SetEventActive(true)

	/*
	   Build a price series that:
	     • Produces a bullish HMA crossover (steady upward movement).
	     • Gives a sizable ATSO value (≈2) → magnitude > 0.5.
	*/
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i) // monotonic rise → bullish HMA
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1200,
		})
	}
	feedBarsED(t, ev, bars)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one BUY order after event‑driven long entry, got %d", len(exec.Orders()))
	}
	o := exec.Orders()[0]
	if o.Side != types.Buy {
		t.Fatalf("expected BUY order, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("expected positive quantity, got %f", o.Qty)
	}
}

/*
-----------------------------------------------------------------------
Test 3 – Event active + bearish HMA + ATSO magnitude above threshold → SHORT.
-----------------------------------------------------------------------
*/
func TestEventDriven_ShortEntry(t *testing.T) {
	ev, exec := buildEventDriven(t, 0.5, 5)

	ev.SetEventActive(true)

	/*
	   Downward price ramp → bearish HMA crossover.
	   ATSO magnitude will be ≈2 (negative), absolute value > 0.5.
	*/
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 115.0 - float64(i) // monotonic decline
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1200,
		})
	}
	feedBarsED(t, ev, bars)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one SELL order after event‑driven short entry, got %d", len(exec.Orders()))
	}
	o := exec.Orders()[0]
	if o.Side != types.Sell {
		t.Fatalf("expected SELL order, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("expected positive quantity, got %f", o.Qty)
	}
}

/*
-----------------------------------------------------------------------
Test 4 – Trailing‑stop while a long position is open.
-----------------------------------------------------------------------
1️⃣ Open a long (event active + bullish HMA).
2️⃣ Raise the price so that the trailing‑stop level (entry * (1+TrailingPct))

	is breached → a SELL order should close the position.
*/
func TestEventDriven_TrailingStop(t *testing.T) {
	ev, exec := buildEventDriven(t, 0.5, 5)

	ev.SetEventActive(true)
	ev.Cfg.TrailingPct = 0.02 // 2 %

	// ---- Phase 1 – long entry (upward ramp) ----
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
	feedBarsED(t, ev, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price

	// ---- Phase 2 – price climbs past trailing level ----
	trailingLevel := entryPrice * 1.02
	high := trailingLevel + 0.5
	low := trailingLevel - 0.5
	close := trailingLevel + 0.1
	feedBarsED(t, ev, []candle{{high, low, close, 1300}})

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected trailing‑stop close order, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
	}
	closeOrder := exec.Orders()[1]
	if closeOrder.Side != types.Sell {
		t.Fatalf("expected SELL to close trailing stop, got %s", closeOrder.Side)
	}
	if closeOrder.Price < trailingLevel {
		t.Fatalf("trailing‑stop price %f is below expected %f", closeOrder.Price, trailingLevel)
	}
}

/*
-----------------------------------------------------------------------
Test 5 – Take‑profit while a long position is open.
-----------------------------------------------------------------------
The strategy uses `TakeProfitPct` as an ATR‑multiple.  We set it to 2.0;
with an ATSO value ≈2 the TP level becomes `entry + 2*ATR`.
*/
func TestEventDriven_TakeProfit(t *testing.T) {
	ev, exec := buildEventDriven(t, 0.5, 5)

	ev.SetEventActive(true)
	ev.Cfg.TakeProfitPct = 2.0 // ATR‑multiple TP

	// ---- Phase 1 – long entry (upward ramp) ----
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
	feedBarsED(t, ev, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price

	// ---- Phase 2 – price reaches TP (entry + 2*ATR) ----
	// For a smooth upward ramp the ATSO value settles around 2.
	// TP = entry + 2*2 = entry + 4.
	tpLevel := entryPrice + 4.0
	high := tpLevel + 0.5
	low := tpLevel - 0.5
	close := tpLevel + 0.1
	feedBarsED(t, ev, []candle{{high, low, close, 1400}})

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected TP close order, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
	}
	tpOrder := exec.Orders()[1]
	if tpOrder.Side != types.Sell {
		t.Fatalf("expected SELL to close TP, got %s", tpOrder.Side)
	}
	if tpOrder.Price < tpLevel {
		t.Fatalf("TP price %f is below expected %f", tpOrder.Price, tpLevel)
	}
}

/*
-----------------------------------------------------------------------
Test 6 – Max‑holding‑bars enforcement.
-----------------------------------------------------------------------
After `maxHoldingBars` bars have elapsed since entry, the strategy must
close the position regardless of price.
*/
func TestEventDriven_MaxHoldingBars(t *testing.T) {
	const maxBars = 3
	ev, exec := buildEventDriven(t, 0.5, maxBars)

	ev.SetEventActive(true)

	// ---- Phase 1 – long entry (upward ramp) ----
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
	feedBarsED(t, ev, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}

	// ---- Phase 2 – feed exactly maxHoldingBars bars while staying flat.
	// The price stays around the entry level; the strategy should close
	// after the third bar.
	var flat []candle
	for i := 0; i < maxBars; i++ {
		price := exec.Orders()[0].Price // keep price roughly constant
		flat = append(flat, candle{
			high:   price + 0.2,
			low:    price - 0.2,
			close:  price,
			volume: 1100,
		})
	}
	feedBarsED(t, ev, flat)

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected a second order (forced close after maxHoldingBars), got %d", len(exec.Orders()))
	}
	closeOrder := exec.Orders()[1]
	if closeOrder.Side != types.Sell {
		t.Fatalf("expected SELL to close position after maxHoldingBars, got %s", closeOrder.Side)
	}
}

/*
-----------------------------------------------------------------------
Test 7 – Deactivating the event flag closes any open position.
-----------------------------------------------------------------------
1️⃣ Open a long (event active).
2️⃣ Call `SetEventActive(false)` – the strategy should instantly close

	the position.
*/
func TestEventDriven_EventDeactivationCloses(t *testing.T) {
	ev, exec := buildEventDriven(t, 0.5, 5)

	ev.SetEventActive(true)

	// ---- Open a long (upward ramp) ----
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
	feedBarsED(t, ev, up)

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
}
