package strategy

import (
	"testing"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/testutils"
	"github.com/evdnx/gots/types"
)

// buildBreakoutMomentum creates a BreakoutMomentum strategy wired to a mock
// executor and logger.  The suiteFactory returns a *real* goti.IndicatorSuite.
// All oscillator thresholds are set to extreme values so the RSI/MFI/VWAO
// value checks are always satisfied – the tests only need to control the
// crossover direction via the price series.
func buildBreakoutMomentum(t *testing.T) (*BreakoutMomentum, *testutils.MockExecutor) {
	// Extremely permissive thresholds – they will never block a trade.
	cfg := config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		VWAOStrongTrend:   1e9, // not used directly by this strategy
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
	bm := &BreakoutMomentum{BaseStrategy: base}
	return bm, mockExec
}

// feedBars sends a slice of candles to the supplied BreakoutMomentum instance.
func feedBarsBM(t *testing.T, bm *BreakoutMomentum, bars []candle) {
	for _, b := range bars {
		bm.ProcessBar(b.high, b.low, b.close, b.volume)
	}
}

/*
-----------------------------------------------------------------------
Test 1 – Bullish crossovers → long entry.
-----------------------------------------------------------------------
An upward price ramp generates bullish crossovers for RSI, MFI and VWAO
after the warm‑up period (≥ 10 bars for HMA, ≥ 14 for the oscillators).
Because the thresholds are extreme, the oscillator *values* are irrelevant.
*/
func TestBreakoutMomentum_LongEntry(t *testing.T) {
	bm, exec := buildBreakoutMomentum(t)

	// 15 upward bars – enough for warm‑up and to trigger crossovers.
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
	feedBarsBM(t, bm, bars)

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
A downward price ramp produces bearish crossovers for all three oscillators.
*/
func TestBreakoutMomentum_ShortEntry(t *testing.T) {
	bm, exec := buildBreakoutMomentum(t)

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
	feedBarsBM(t, bm, bars)

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
func TestBreakoutMomentum_TrailingStop(t *testing.T) {
	bm, exec := buildBreakoutMomentum(t)

	// Enable trailing stop (2 %).
	bm.Cfg.TrailingPct = 0.02

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
	feedBarsBM(t, bm, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price // ≈115

	// ---- Phase 2 – price climbs past trailing level ----
	trailingLevel := entryPrice * 1.02
	high := trailingLevel + 0.5
	low := trailingLevel - 0.5
	close := trailingLevel + 0.1 // ensure close ≥ trailing level
	feedBarsBM(t, bm, []candle{{high, low, close, 1200}})

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
func TestBreakoutMomentum_TakeProfit(t *testing.T) {
	bm, exec := buildBreakoutMomentum(t)

	// Enable TP (ATR‑multiple = 2).
	bm.Cfg.TakeProfitPct = 2.0

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
	feedBarsBM(t, bm, up)

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
	feedBarsBM(t, bm, []candle{{high, low, close, 1300}})

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
func TestBreakoutMomentum_OppositeSideFlip(t *testing.T) {
	bm, exec := buildBreakoutMomentum(t)

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
	feedBarsBM(t, bm, up)

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
	feedBarsBM(t, bm, down)

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
