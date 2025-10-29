package strategy

import (
	"testing"

	"github.com/evdnx/gots/types"
)

/*
-----------------------------------------------------------------------
Test 1 – Bullish crossover → long entry.
-----------------------------------------------------------------------
A steady upward price ramp causes the three oscillators (RSI, MFI,
VWAO) to generate bullish crossovers after the warm‑up period.
Because we set the thresholds to extreme values, the oscillator
*value* checks are always satisfied.
*/
func TestMeanReversion_LongEntry(t *testing.T) {
	mr, exec := buildMeanReversion(t)

	// 15 upward bars – enough for warm‑up (14) and to trigger crossovers.
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := float64(100 + i) // 101, 102, … 115
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBarsMr(t, mr, bars)

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
Test 2 – Bearish crossover → short entry.
-----------------------------------------------------------------------
A steady downward price ramp produces bearish crossovers for all three
oscillators.
*/
func TestMeanReversion_ShortEntry(t *testing.T) {
	mr, exec := buildMeanReversion(t)

	// 15 downward bars.
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := float64(115 - i) // 114, 113, … 100
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBarsMr(t, mr, bars)

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
1️⃣ Generate a long entry (upward ramp).
2️⃣ Raise the price so that the trailing‑stop level (entry * (1+TrailingPct))

	is crossed.  The strategy should emit a SELL order that closes the
	position.
*/
func TestMeanReversion_TrailingStop(t *testing.T) {
	mr, exec := buildMeanReversion(t)

	// Enable trailing stop (2 %).
	mr.Cfg.TrailingPct = 0.02

	// ---- Phase 1 – long entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := float64(100 + i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBarsMr(t, mr, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price // should be ~115

	// ---- Phase 2 – price climbs past trailing level ----
	// trailing level = entry * (1 + 0.02)
	trailingLevel := entryPrice * 1.02
	high := trailingLevel + 0.5
	low := trailingLevel - 0.5
	close := trailingLevel + 0.1 // ensure close ≥ trailing level
	feedBarsMr(t, mr, []candle{{high, low, close, 1200}})

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected trailing‑stop close order, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
	}
	closeOrder := exec.Orders()[1]
	if closeOrder.Side != types.Sell {
		t.Fatalf("expected SELL to close trailing stop, got %s", closeOrder.Side)
	}
	if closeOrder.Price < trailingLevel {
		t.Fatalf("trailing‑stop price %f is below expected level %f", closeOrder.Price, trailingLevel)
	}
}

/*
-----------------------------------------------------------------------
Test 4 – Take‑profit while a long position is open.
-----------------------------------------------------------------------
The strategy uses `TakeProfitPct` as an ATR‑multiple.  We set
`TakeProfitPct = 2.0`; with an ATSO value of ≈2 (produced by the price
series) the TP level becomes `entry + 2*ATR`.
*/
func TestMeanReversion_TakeProfit(t *testing.T) {
	mr, exec := buildMeanReversion(t)

	// Enable TP (ATR‑multiple = 2).
	mr.Cfg.TakeProfitPct = 2.0

	// ---- Phase 1 – long entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := float64(100 + i) // 101…115
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBarsMr(t, mr, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price // ≈115

	// ---- Phase 2 – price reaches TP ----
	// The ATSO value for a smooth upward ramp settles around 2.
	// TP = entry + ATR*TakeProfitPct = entry + 2*2 = entry + 4.
	tpLevel := entryPrice + 4.0
	high := tpLevel + 0.5
	low := tpLevel - 0.5
	close := tpLevel + 0.1
	feedBarsMr(t, mr, []candle{{high, low, close, 1300}})

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
Test 5 – Opposite‑side flip (short signal while long is open).
-----------------------------------------------------------------------
1️⃣ Open a long position (upward ramp).
2️⃣ Feed a bar that produces a bearish crossover for all three

	oscillators (downward ramp).  The strategy should first close the
	long (SELL) and then open a new short (SELL).
*/
func TestMeanReversion_OppositeSideFlip(t *testing.T) {
	mr, exec := buildMeanReversion(t)

	// ---- Phase 1 – long entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := float64(100 + i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBarsMr(t, mr, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	longQty := exec.Orders()[0].Qty

	// ---- Phase 2 – bearish crossover (downward ramp) ----
	var down []candle
	for i := 1; i <= 15; i++ {
		price := float64(115 - i) // 114 … 100
		down = append(down, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBarsMr(t, mr, down)

	/*
	   Expected order flow:
	     0 – initial long (BUY)
	     1 – close long (SELL)
	     2 – open new short (SELL)
	*/
	if len(exec.Orders()) != 3 {
		t.Fatalf("expected three orders (long, close, short), got %d: %+v", len(exec.Orders()), exec.Orders())
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("order 1 should be SELL to close the long, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[2].Side != types.Sell {
		t.Fatalf("order 2 should be SELL to open the short, got %s", exec.Orders()[2].Side)
	}
	if exec.Orders()[1].Qty != longQty {
		t.Fatalf("close‑long quantity (%f) should equal original long quantity (%f)", exec.Orders()[1].Qty, longQty)
	}
	if exec.Orders()[2].Qty <= 0 {
		t.Fatalf("short entry quantity must be positive, got %f", exec.Orders()[2].Qty)
	}
}
