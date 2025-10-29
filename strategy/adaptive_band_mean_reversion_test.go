package strategy

import (
	"testing"

	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/types"
)

/*
   -----------------------------------------------------------------------
   Why the extreme thresholds?
   -----------------------------------------------------------------------
   The AdaptiveBandMR strategy checks three things before opening a trade:

   1️⃣  low ≤ lowerBand   (or high ≥ upperBand for shorts)
   2️⃣  RSI ≤ RSIOversold   &&   MFI ≤ MFIOversold   &&   !hmaBull (for longs)
   3️⃣  The symmetric checks for shorts.

   By setting RSIOversold = -1e9 and RSIOverbought = +1e9 (similarly for MFI),
   conditions 2 are *always* true regardless of what the indicator suite
   actually computes.  This lets the test focus exclusively on the adaptive‑band
   logic, which is the part we want to validate deterministically.
*/

func extremeCfg() config.StrategyConfig {
	return config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		HMAPeriod:         9,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.01, // 1 % risk per trade
		StopLossPct:       0.01, // 1 % stop‑loss (used also as band factor)
		TakeProfitPct:     0.0,  // enable per‑test
		TrailingPct:       0.0,  // enable per‑test
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}
}

/*
-----------------------------------------------------------------------

	1️⃣ Long‑entry test – the low price falls below the adaptive lower band.
	-----------------------------------------------------------------------
*/
func TestAdaptiveBandMR_LongEntry(t *testing.T) {
	cfg := extremeCfg()
	strat, exec := buildAdaptiveStrategy(t, cfg)

	/*
	   Band calculation (inside the strategy):
	     bandWidth = close * StopLossPct
	     lowerBand = close - bandWidth - atr
	   We choose a price series that makes atr = 2 and close = 100,
	   so:
	     bandWidth = 1
	     lowerBand = 100 - 1 - 2 = 97
	   Setting low = 96 guarantees the long condition.
	*/
	high, low, close, vol := 101.0, 96.0, 100.0, 1500.0
	strat.ProcessBar(high, low, close, vol)

	if got := len(exec.Orders()); got != 1 {
		t.Fatalf("expected exactly one order (long entry), got %d", got)
	}
	o := lastOrder(exec)
	if o.Side != types.Buy {
		t.Fatalf("expected BUY order, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("expected positive quantity, got %f", o.Qty)
	}
}

/*
-----------------------------------------------------------------------

	2️⃣ Short‑entry test – the high price exceeds the adaptive upper band.
	-----------------------------------------------------------------------
*/
func TestAdaptiveBandMR_ShortEntry(t *testing.T) {
	cfg := extremeCfg()
	strat, exec := buildAdaptiveStrategy(t, cfg)

	/*
	   With the same numbers as the long test (close = 100, atr = 2):
	     bandWidth = 1
	     upperBand = 100 + 1 + 2 = 103
	   Setting high = 104 forces the short condition.
	*/
	high, low, close, vol := 104.0, 99.0, 100.0, 1500.0
	strat.ProcessBar(high, low, close, vol)

	if got := len(exec.Orders()); got != 1 {
		t.Fatalf("expected exactly one order (short entry), got %d", got)
	}
	o := lastOrder(exec)
	if o.Side != types.Sell {
		t.Fatalf("expected SELL order, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("expected positive quantity, got %f", o.Qty)
	}
}

/*
-----------------------------------------------------------------------

	3️⃣ Trailing‑stop activation while a long position is open.
	-----------------------------------------------------------------------
*/
func TestAdaptiveBandMR_TrailingStop(t *testing.T) {
	cfg := extremeCfg()
	cfg.TrailingPct = 0.02 // 2 % trailing stop
	strat, exec := buildAdaptiveStrategy(t, cfg)

	// ---- Bar 1 – long entry (same numbers as test 1) ----
	high, low, close, vol := 101.0, 96.0, 100.0, 1500.0
	strat.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected entry order, got %d", len(exec.Orders()))
	}
	entryPrice := exec.Orders()[0].Price // should be 100

	// ---- Bar 2 – price climbs past trailing level (entry * (1+TrailingPct)) ----
	// trailing level = 100 * 1.02 = 102
	high, low, close, vol = 103.0, 101.0, 102.5, 1600.0
	strat.ProcessBar(high, low, close, vol)

	if got := len(exec.Orders()); got != 2 {
		t.Fatalf("expected trailing‑stop close order, got %d (orders: %+v)", got, exec.Orders())
	}
	closeOrder := exec.Orders()[1]
	if closeOrder.Side != types.Sell {
		t.Fatalf("expected SELL to close trailing stop, got %s", closeOrder.Side)
	}
	if closeOrder.Price < entryPrice*1.02 {
		t.Fatalf("trailing‑stop price %f is below expected %f", closeOrder.Price, entryPrice*1.02)
	}
}

/*
-----------------------------------------------------------------------

	4️⃣ Take‑profit activation (ATR‑multiple TP).
	-----------------------------------------------------------------------
*/
func TestAdaptiveBandMR_TakeProfit(t *testing.T) {
	cfg := extremeCfg()
	cfg.TakeProfitPct = 2.0 // TP = entry + ATR*2
	strat, exec := buildAdaptiveStrategy(t, cfg)

	// ---- Bar 1 – long entry (same numbers as test 1) ----
	high, low, close, vol := 101.0, 96.0, 100.0, 1500.0
	strat.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected entry order, got %d", len(exec.Orders()))
	}
	entryPrice := exec.Orders()[0].Price // 100

	// ---- Bar 2 – price reaches TP.
	// ATR (from ATSO) is 2, so TP = 100 + 2*2 = 104.
	high, low, close, vol = 105.0, 103.0, 104.5, 1600.0
	strat.ProcessBar(high, low, close, vol)

	if got := len(exec.Orders()); got != 2 {
		t.Fatalf("expected TP close order, got %d (orders: %+v)", got, exec.Orders())
	}
	tpOrder := exec.Orders()[1]
	if tpOrder.Side != types.Sell {
		t.Fatalf("expected SELL to close TP, got %s", tpOrder.Side)
	}
	if tpOrder.Price < entryPrice+2*2 {
		t.Fatalf("TP price %f is below expected %f", tpOrder.Price, entryPrice+2*2)
	}
}

/*
-----------------------------------------------------------------------

	5️⃣ Opposite‑side flip: a short signal arrives while a long is open.
	-----------------------------------------------------------------------
*/
func TestAdaptiveBandMR_OppositeClose(t *testing.T) {
	// Use the extreme thresholds so the oscillator checks are always true.
	cfg := extremeCfg()
	strat, exec := buildAdaptiveStrategy(t, cfg)

	/* -------------------------------------------------------------------
	   STEP 1 – LONG ENTRY (same conditions as TestAdaptiveBandMR_LongEntry)
	   ------------------------------------------------------------------- */
	high, low, close, vol := 101.0, 96.0, 100.0, 1500.0 // low ≤ lowerBand ⇒ long
	strat.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	//longEntryPrice := exec.Orders()[0].Price // should be 100

	/* -------------------------------------------------------------------
	   STEP 2 – SHORT SIGNAL ARRIVES WHILE LONG IS OPEN
	   -------------------------------------------------------------------
	   With the same ATR (=2) and close (=100) the adaptive bands are:
	     lowerBand = 100 - 1 - 2 = 97
	     upperBand = 100 + 1 + 2 = 103

	   To trigger a short we need:
	     high ≥ upperBand   (≥ 103)
	     AND the other oscillator checks (already satisfied by extreme thresholds).

	   We also set low high enough that the HMA bullish crossover flag is false.
	*/
	high, low, close, vol = 104.0, 99.0, 100.0, 1600.0 // high ≥ upperBand ⇒ short
	strat.ProcessBar(high, low, close, vol)

	/*
	   Expected order flow:
	     0 – original long entry (BUY)
	     1 – close the long position (SELL)
	     2 – open a new short position (SELL)

	   The strategy implements this by calling `closePosition` followed by
	   `openShort`, which results in two separate submissions.
	*/
	if got := len(exec.Orders()); got != 3 {
		t.Fatalf("expected three orders (entry, close‑long, open‑short), got %d: %+v", got, exec.Orders())
	}

	// Order 0 – original long entry
	if o := exec.Orders()[0]; o.Side != types.Buy {
		t.Fatalf("order 0 should be BUY (entry), got %s", o.Side)
	}
	// Order 1 – close the long position
	if o := exec.Orders()[1]; o.Side != types.Sell {
		t.Fatalf("order 1 should be SELL (close long), got %s", o.Side)
	}
	// Order 2 – open the new short position
	if o := exec.Orders()[2]; o.Side != types.Sell {
		t.Fatalf("order 2 should be SELL (new short), got %s", o.Side)
	}

	// Sanity checks on quantities – they should be identical because the
	// risk calculator sees the same equity, stop‑loss pct and ATR.
	qtyEntry := exec.Orders()[0].Qty
	qtyClose := exec.Orders()[1].Qty
	qtyShort := exec.Orders()[2].Qty

	if qtyEntry <= 0 || qtyClose <= 0 || qtyShort <= 0 {
		t.Fatalf("all quantities must be positive (got entry=%f, close=%f, short=%f)",
			qtyEntry, qtyClose, qtyShort)
	}
	if qtyEntry != qtyClose {
		t.Fatalf("quantity used to close long (%f) differs from entry quantity (%f)",
			qtyClose, qtyEntry)
	}
	if qtyEntry != qtyShort {
		t.Fatalf("quantity for new short (%f) differs from original entry (%f)",
			qtyShort, qtyEntry)
	}

	/* -------------------------------------------------------------------
	   OPTIONAL: verify that the short entry price respects the band logic.
	   ------------------------------------------------------------------- */
	if exec.Orders()[2].Price != close {
		t.Fatalf("short entry price should be the bar close (%f), got %f",
			close, exec.Orders()[2].Price)
	}
	// Ensure the close‑long price matches the bar close as well.
	if exec.Orders()[1].Price != close {
		t.Fatalf("close‑long price should be the bar close (%f), got %f",
			close, exec.Orders()[1].Price)
	}
	// Finally, confirm that the equity after the whole sequence matches expectations:
	//   start equity = 10 000
	//   long entry: equity -= longQty * entryPrice
	//   close long: equity += longQty * closePrice (same as entryPrice)
	//   short entry: equity -= shortQty * closePrice (since a sell adds cash, the mock executor adds the proceeds)
	//
	// The net effect should be zero change in equity because the long was closed at the same price
	// and the short entry does not affect equity until it is later closed.
	expectedEquity := 10_000.0
	if got := exec.Equity(); got != expectedEquity {
		t.Fatalf("expected final equity %.2f, got %.2f (net zero P&L expected)", expectedEquity, got)
	}
}
