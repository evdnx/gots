package strategy

import (
	"testing"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/testutils"
	"github.com/evdnx/gots/types"
)

// feedBars sends a slice of candles to the supplied VolScaledPos instance.
func feedBarsVolScaledPos(t *testing.T, vp *VolScaledPos, bars []candle) {
	for _, b := range bars {
		vp.ProcessBar(b.high, b.low, b.close, b.volume)
	}
}

// buildVolScaled creates a VolScaledPos strategy wired to a mock executor
// and logger. All oscillator thresholds are set to extreme values so the
// RSI/MFI checks never block a trade – the only gating factor is the HMA
// crossover and the ATSO magnitude.
func buildVolScaled(t *testing.T) (*VolScaledPos, *testutils.MockExecutor) {
	// Extremely permissive thresholds – they will never reject a trade.
	cfg := config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		VWAOStrongTrend:   1e9, // not used by this strategy
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

	// Suite factory – returns a *real* goti suite.
	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = cfg.RSIOverbought
		ic.RSIOversold = cfg.RSIOversold
		ic.MFIOverbought = cfg.MFIOverbought
		ic.MFIOversold = cfg.MFIOversold
		ic.VWAOStrongTrend = cfg.VWAOStrongTrend
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}

	base, err := NewBaseStrategy("TEST", cfg, mockExec, suiteFactory, mockLog)
	if err != nil {
		t.Fatalf("NewBaseStrategy failed: %v", err)
	}
	vp := &VolScaledPos{BaseStrategy: base}
	return vp, mockExec
}

/*
-----------------------------------------------------------------------
Test 1 – Bullish HMA crossover → long entry.
-----------------------------------------------------------------------
An upward price ramp creates a bullish HMA crossover after the warm‑up
period (≈10 bars).  Because the thresholds are extreme, the RSI/MFI
checks are always satisfied, so the strategy should emit a BUY order.
*/
func TestVolScaled_LongEntry(t *testing.T) {
	vp, exec := buildVolScaled(t)

	// 15 upward bars – enough for warm‑up and to trigger the HMA crossover.
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
	feedBarsVolScaledPos(t, vp, bars)

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
Test 2 – Bearish HMA crossover → short entry.
-----------------------------------------------------------------------
A downward price ramp creates a bearish HMA crossover after warm‑up,
leading to a SELL order.
*/
func TestVolScaled_ShortEntry(t *testing.T) {
	vp, exec := buildVolScaled(t)

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
	feedBarsVolScaledPos(t, vp, bars)

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
func TestVolScaled_TrailingStop(t *testing.T) {
	vp, exec := buildVolScaled(t)

	// Enable trailing stop (2 %).
	vp.Cfg.TrailingPct = 0.02

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
	feedBarsVolScaledPos(t, vp, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price

	// ---- Phase 2 – price climbs past trailing level ----
	trailingLevel := entryPrice * 1.02
	high := trailingLevel + 0.5
	low := trailingLevel - 0.5
	close := trailingLevel + 0.1
	feedBarsVolScaledPos(t, vp, []candle{{high, low, close, 1200}})

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
func TestVolScaled_TakeProfit(t *testing.T) {
	vp, exec := buildVolScaled(t)

	// Enable TP (ATR‑multiple = 2).
	vp.Cfg.TakeProfitPct = 2.0

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
	feedBarsVolScaledPos(t, vp, up)

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
	feedBarsVolScaledPos(t, vp, []candle{{high, low, close, 1300}})

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

	the long (SELL) and then open a short (SELL).
*/
func TestVolScaled_OppositeSideFlip(t *testing.T) {
	vp, exec := buildVolScaled(t)

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
	feedBarsVolScaledPos(t, vp, up)

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
	feedBarsVolScaledPos(t, vp, down)

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
