package strategy

import (
	"testing"

	"github.com/evdnx/gots/types"
)

/*
-----------------------------------------------------------------------
Test 1 – Bullish crossovers on BOTH time‑frames → LONG entry.
-----------------------------------------------------------------------
An upward price ramp creates a bullish HMA crossover after the warm‑up
period (≈10 bars).  Because we feed the same bar to both fast and
slow suites, both will cross at roughly the same time, satisfying the
`longCond` check.
*/
func TestMultiTF_LongEntry(t *testing.T) {
	mt, exec := buildMultiTF(t, 60, 300)

	// 15 upward bars – enough for warm‑up and to trigger crossovers.
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, mt, bars)

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
Test 2 – Bearish crossovers on BOTH time‑frames → SHORT entry.
-----------------------------------------------------------------------
*/
func TestMultiTF_ShortEntry(t *testing.T) {
	mt, exec := buildMultiTF(t, 60, 300)

	// 15 downward bars.
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 115.0 - float64(i)
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, mt, bars)

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
2️⃣ Raise the price so that the trailing‑stop level (`entry *

	(1+TrailingPct)`) is breached → a SELL order should close the position.
*/
func TestMultiTF_TrailingStop(t *testing.T) {
	mt, exec := buildMultiTF(t, 60, 300)

	// Enable trailing stop (2 %).
	mt.Cfg.TrailingPct = 0.02

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
	feedBars(t, mt, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price

	// ---- Phase 2 – price climbs past trailing level ----
	trailingLevel := entryPrice * 1.02
	high := trailingLevel + 0.5
	low := trailingLevel - 0.5
	close := trailingLevel + 0.1
	feedBars(t, mt, []candle{{high, low, close, 1200}})

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
Test 4 – Take‑profit while a long position is open.
-----------------------------------------------------------------------
The strategy uses `TakeProfitPct` as an ATR‑multiple.  We set it to 2.0;
with an ATSO value ≈2 the TP level becomes `entry + 2*ATR`.
*/
func TestMultiTF_TakeProfit(t *testing.T) {
	mt, exec := buildMultiTF(t, 60, 300)

	// Enable TP (ATR‑multiple = 2).
	mt.Cfg.TakeProfitPct = 2.0

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
	feedBars(t, mt, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price

	// ---- price reaches TP (entry + 2*ATR).  ATSO≈2 for this series.
	tpLevel := entryPrice + 4.0
	high := tpLevel + 0.5
	low := tpLevel - 0.5
	close := tpLevel + 0.1
	feedBars(t, mt, []candle{{high, low, close, 1300}})

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
Test 5 – Opposite‑side flip (short after long).
-----------------------------------------------------------------------
1️⃣ Open a long (upward ramp).
2️⃣ Feed a bearish‑crossover series; the strategy should first close

	the long (SELL) and then open a new short (SELL).
*/
func TestMultiTF_OppositeSideFlip(t *testing.T) {
	mt, exec := buildMultiTF(t, 60, 300)

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
	feedBars(t, mt, up)

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
	feedBars(t, mt, down)

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
		t.Fatalf("order 1 should be SELL to close the long, got %s", exec.Orders()[1].Side)
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
