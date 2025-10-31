package strategy

import (
	"testing"

	"github.com/evdnx/gots/types"
)

/*
-----------------------------------------------------------------------
Test 1 – Bullish crossovers → long entry.
-----------------------------------------------------------------------
An upward price ramp creates bullish crossovers for RSI, MFI and VWAO
after the warm‑up period (≥ 14 bars).  Because the oscillator thresholds
are inverted in the test helpers, the RSI/MFI value checks are always
satisfied, so the strategy should emit a BUY order.
*/
func TestBreakoutMomentum_LongEntry(t *testing.T) {
	bm, exec := buildBreakout(t)

	// 15 upward bars – enough for warm‑up and to trigger crossovers.
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i) // 101, 102, … 115
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, bm, bars)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected exactly one BUY order, got %d", len(exec.Orders()))
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
Test 2 – Bearish crossovers → short entry.
-----------------------------------------------------------------------
A downward price ramp produces bearish crossovers for all three
oscillators, leading to a SELL order.
*/
func TestBreakoutMomentum_ShortEntry(t *testing.T) {
	bm, exec := buildBreakout(t)

	// 15 downward bars.
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 115.0 - float64(i) // 114, 113, … 100
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, bm, bars)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected exactly one SELL order, got %d", len(exec.Orders()))
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
Test 3 – Trailing‑stop while a long position is open.
-----------------------------------------------------------------------
1️⃣ Open a long (upward ramp).
2️⃣ Raise the price so that the trailing‑stop level

	(`entry × (1+TrailingPct)`) is breached → a SELL order should close
	the position.
*/
func TestBreakoutMomentum_TrailingStop(t *testing.T) {
	bm, exec := buildBreakout(t)
	bm.Cfg.TrailingPct = 0.02 // 2 %

	// ---- Phase 1 – long entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, bm, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entry := exec.Orders()[0].Price

	// ---- Phase 2 – price crosses trailing level ----
	trail := entry * 1.02
	bm.ProcessBar(trail+0.5, trail-0.5, trail+0.1, 1200)

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

/*
-----------------------------------------------------------------------
Test 4 – Take‑profit while a long position is open.
-----------------------------------------------------------------------
The strategy uses `TakeProfitPct` as an ATR‑multiple.  We set it to 2.0;
with an ATSO value ≈2 the TP level becomes `entry + 2*ATR`.
*/
func TestBreakoutMomentum_TakeProfit(t *testing.T) {
	bm, exec := buildBreakout(t)
	bm.Cfg.TakeProfitPct = 2.0 // ATR‑multiple TP

	// ---- Phase 1 – long entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i) // 101…115
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, bm, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entry := exec.Orders()[0].Price

	// TP = entry + 2*ATR (ATSO≈2 → TP ≈ entry+4)
	tp := entry + 4.0
	bm.ProcessBar(tp+0.5, tp-0.5, tp+0.1, 1300)

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

/*
-----------------------------------------------------------------------
Test 5 – Opposite‑side flip (short after long).
-----------------------------------------------------------------------
1️⃣ Open a long (upward ramp).
2️⃣ Feed a bearish‑crossover series; the strategy should first close

	the long (SELL) and then open a short (SELL).
*/
func TestBreakoutMomentum_OppositeSideFlip(t *testing.T) {
	bm, exec := buildBreakout(t)

	// ---- Phase 1 – long entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, bm, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	longQty := exec.Orders()[0].Qty

	// ---- Phase 2 – bearish crossover (downward ramp) ----
	var down []candle
	for i := 1; i <= 15; i++ {
		price := 115.0 - float64(i) // 114 … 100
		down = append(down, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, bm, down)

	/*
	   Expected order flow:
	     0 – long entry (BUY)
	     1 – close long (SELL)
	     2 – open new short (SELL)
	*/
	if len(exec.Orders()) != 3 {
		t.Fatalf("expected three orders (long, close‑long, short), got %d: %+v",
			len(exec.Orders()), exec.Orders())
	}
	if exec.Orders()[0].Side != types.Buy {
		t.Fatalf("order 0 should be BUY (long entry), got %s", exec.Orders()[0].Side)
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("order 1 should be SELL to close long, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[2].Side != types.Sell {
		t.Fatalf("order 2 should be SELL (short entry), got %s", exec.Orders()[2].Side)
	}
	if exec.Orders()[1].Qty != longQty {
		t.Fatalf("close‑long quantity (%f) should equal original long quantity (%f)",
			exec.Orders()[1].Qty, longQty)
	}
	if exec.Orders()[2].Qty <= 0 {
		t.Fatalf("short entry quantity must be positive, got %f", exec.Orders()[2].Qty)
	}
}
