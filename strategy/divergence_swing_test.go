package strategy

import (
	"testing"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/testutils"
	"github.com/evdnx/gots/types"
)

// buildDivergenceSwing creates a DivergenceSwing strategy wired to a mock
// executor and logger.  The suiteFactory returns a *real* goti.IndicatorSuite.
// All oscillator thresholds are set to extreme values so the RSI/MFI/VWAO
// value checks are always satisfied – the tests only need to trigger (or
// avoid) divergence detection via the price series.
func buildDivergenceSwing(t *testing.T) (*DivergenceSwing, *testutils.MockExecutor) {
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
		ic.RSIOversold = 30
		ic.MFIOverbought = 80
		ic.MFIOversold = 20
		ic.VWAOStrongTrend = 70
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}

	base, err := NewBaseStrategy("TEST", cfg, mockExec, suiteFactory, mockLog)
	if err != nil {
		t.Fatalf("NewBaseStrategy failed: %v", err)
	}
	ds := &DivergenceSwing{BaseStrategy: base}
	return ds, mockExec
}

// feedBars sends a slice of candles to the supplied DivergenceSwing instance.
func feedBarsDS(t *testing.T, ds *DivergenceSwing, bars []candle) {
	for _, b := range bars {
		ds.ProcessBar(b.high, b.low, b.close, b.volume)
	}
}

/*
-----------------------------------------------------------------------
Test 1 – No divergence (monotonic price) → no orders.
-----------------------------------------------------------------------
A simple upward price ramp yields no divergence (price and RSI move
together).  The strategy should therefore stay idle.
*/
func TestDivergenceSwing_NoDivergence(t *testing.T) {
	ds, exec := buildDivergenceSwing(t)

	// 20 upward bars – enough for warm‑up (RSI needs 14 closes) and
	// to ensure the indicators have settled.
	var bars []candle
	for i := 1; i <= 20; i++ {
		price := float64(100 + i) // 101, 102, …
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBarsDS(t, ds, bars)

	if len(exec.Orders()) != 0 {
		t.Fatalf("expected no orders when there is no divergence, got %d", len(exec.Orders()))
	}
}

/*
-----------------------------------------------------------------------
Test 2 – Bullish divergence → long entry.
-----------------------------------------------------------------------
Bullish divergence (price makes lower lows while RSI makes higher lows)
is a classic pattern that the `goti` library recognises.  We construct a
simple synthetic series that exhibits this behaviour:

  - Prices: 100, 99, 98, 99, 100   (lower lows, then a bounce)
  - RSI (computed internally) will rise because the price drops are
    followed by a recovery, creating higher lows on the oscillator.

After the warm‑up period the strategy should detect the bullish
divergence, see a bullish HMA crossover (the upward bounce), and open
a long position.
*/
func TestDivergenceSwing_BullishDivergenceLong(t *testing.T) {
	ds, exec := buildDivergenceSwing(t)

	/*
	   Build a price series that creates a bullish divergence:
	     1. Sharp drop → price low, RSI also drops.
	     2. Slight rebound → price higher low, RSI higher low.
	     3. Continue upward → HMA bullish crossover.
	*/
	var bars []candle

	// 1️⃣ Warm‑up (flat) – 10 bars so the indicators have enough history.
	for i := 0; i < 10; i++ {
		price := 100.0
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}

	// 2️⃣ Create the divergence pattern.
	//    Bar 11: price drops sharply.
	bars = append(bars, candle{high: 100, low: 95, close: 96, volume: 1200})
	//    Bar 12: price recovers but not to the original level (higher low).
	bars = append(bars, candle{high: 101, low: 97, close: 100, volume: 1300})
	//    Bar 13–15: continue upward to give HMA a bullish crossover.
	bars = append(bars, candle{high: 102, low: 99, close: 101, volume: 1400})
	bars = append(bars, candle{high: 103, low: 100, close: 102, volume: 1500})
	bars = append(bars, candle{high: 104, low: 101, close: 103, volume: 1600})

	feedBarsDS(t, ds, bars)

	/*
	   After processing the series we expect exactly one BUY order:
	     * The bullish divergence flag becomes true.
	     * The HMA bullish crossover is detected (price trending up).
	     * The strategy opens a long position.
	*/
	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one BUY order after bullish divergence, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
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
Test 3 – Bearish divergence → short entry.
-----------------------------------------------------------------------
Mirror of the bullish test: price makes higher highs while the RSI
makes lower highs, which the `goti` library reports as bearish
divergence.  After the warm‑up the strategy should open a short.
*/
func TestDivergenceSwing_BearishDivergenceShort(t *testing.T) {
	ds, exec := buildDivergenceSwing(t)

	/*
	   Build a price series that creates a bearish divergence:
	     1. Sharp rise → price high, RSI also rises.
	     2. Small pull‑back → price lower high, RSI lower high.
	     3. Continue downward → HMA bearish crossover.
	*/
	var bars []candle

	// Warm‑up (flat) – 10 bars.
	for i := 0; i < 10; i++ {
		price := 100.0
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}

	// 1️⃣ Sharp rise.
	bars = append(bars, candle{high: 105, low: 100, close: 104, volume: 1200})
	// 2️⃣ Pull‑back, creating a lower high on price but a lower high on RSI.
	bars = append(bars, candle{high: 103, low: 99, close: 100, volume: 1300})
	// 3️⃣ Continue downward to give HMA a bearish crossover.
	bars = append(bars, candle{high: 102, low: 98, close: 99, volume: 1400})
	bars = append(bars, candle{high: 101, low: 97, close: 98, volume: 1500})
	bars = append(bars, candle{high: 100, low: 96, close: 97, volume: 1600})

	feedBarsDS(t, ds, bars)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one SELL order after bearish divergence, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
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
Test 4 – Trailing‑stop on an open long position.
-----------------------------------------------------------------------
After a bullish divergence opens a long, we raise the price enough to
exceed the trailing‑stop level (`entry * (1 + TrailingPct)`).  The
strategy should emit a SELL order that closes the position.
*/
func TestDivergenceSwing_TrailingStop(t *testing.T) {
	ds, exec := buildDivergenceSwing(t)

	// Enable trailing stop (2 %).
	ds.Cfg.TrailingPct = 0.02

	/*
	   Use the same bullish‑divergence series from TestDivergenceSwing_BullishDivergenceLong
	   to open a long position.
	*/
	var bars []candle
	for i := 0; i < 10; i++ {
		price := 100.0
		bars = append(bars, candle{high: price + 0.5, low: price - 0.5, close: price, volume: 1000})
	}
	bars = append(bars,
		candle{high: 100, low: 95, close: 96, volume: 1200},
		candle{high: 101, low: 97, close: 100, volume: 1300},
		candle{high: 102, low: 99, close: 101, volume: 1400},
		candle{high: 103, low: 100, close: 102, volume: 1500},
		candle{high: 104, low: 101, close: 103, volume: 1600},
	)
	feedBarsDS(t, ds, bars)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entryPrice := exec.Orders()[0].Price

	// ---- Price climbs past trailing level ----
	trailingLevel := entryPrice * 1.02
	high := trailingLevel + 0.5
	low := trailingLevel - 0.5
	close := trailingLevel + 0.1
	feedBarsDS(t, ds, []candle{{high, low, close, 1700}})

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
Test 5 – Opposite‑side flip (short after long).
-----------------------------------------------------------------------
After a bullish divergence opens a long, feed a bearish‑divergence
series.  The strategy should close the long (SELL) and then open a new
short (SELL).
*/
func TestDivergenceSwing_OppositeSideFlip(t *testing.T) {
	ds, exec := buildDivergenceSwing(t)

	/*
	   Phase 1 – bullish divergence → long entry.
	*/
	var bullish []candle
	// Warm‑up (flat) – 10 bars so the indicators have enough history.
	for i := 0; i < 10; i++ {
		price := 100.0
		bullish = append(bullish, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	// Construct a classic bullish‑divergence pattern:
	//   • Sharp drop → price low, RSI also drops.
	//   • Recovery to a higher low → RSI makes a higher low.
	//   • Continued upward movement gives a bullish HMA crossover.
	bullish = append(bullish,
		candle{high: 100, low: 95, close: 96, volume: 1200},  // sharp drop
		candle{high: 101, low: 97, close: 100, volume: 1300}, // higher low
		candle{high: 102, low: 99, close: 101, volume: 1400},
		candle{high: 103, low: 100, close: 102, volume: 1500},
		candle{high: 104, low: 101, close: 103, volume: 1600},
	)

	feedBarsDS(t, ds, bullish)

	// Verify that the bullish divergence opened a long position.
	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one BUY order after bullish divergence, got %d", len(exec.Orders()))
	}
	if exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected BUY order, got %s", exec.Orders()[0].Side)
	}
	longQty := exec.Orders()[0].Qty
	if longQty <= 0 {
		t.Fatalf("expected positive quantity for long entry, got %f", longQty)
	}

	/*
	   Phase 2 – bearish divergence (price makes higher highs while RSI makes lower highs)
	   This should cause the strategy to:
	     1️⃣ Close the existing long (SELL)
	     2️⃣ Open a new short (SELL)
	*/

	// Warm‑up for the bearish side (another 10 flat bars to give the
	// indicators enough data after the long entry).
	var bearish []candle
	for i := 0; i < 10; i++ {
		price := 103.0 // keep price near the level we exited at
		bearish = append(bearish, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	// Classic bearish‑divergence shape:
	//   1️⃣ Sharp rise → price high, RSI high.
	//   2️⃣ Small pull‑back → price lower high, RSI lower high.
	//   3️⃣ Continue downward → HMA bearish crossover.
	bearish = append(bearish,
		candle{high: 108, low: 103, close: 107, volume: 1200}, // sharp rise
		candle{high: 106, low: 101, close: 102, volume: 1300}, // lower high
		candle{high: 105, low: 100, close: 101, volume: 1400},
		candle{high: 104, low: 99, close: 100, volume: 1500},
		candle{high: 103, low: 98, close: 99, volume: 1600},
	)

	feedBarsDS(t, ds, bearish)

	/*
	   Expected order flow:
	     0 – initial long (BUY) from bullish divergence
	     1 – close long (SELL) when bearish divergence is detected
	     2 – open new short (SELL) as the opposite entry
	*/
	if len(exec.Orders()) != 3 {
		t.Fatalf("expected three orders (long, close‑long, short), got %d: %+v",
			len(exec.Orders()), exec.Orders())
	}

	// Order 0 – long entry
	if o := exec.Orders()[0]; o.Side != types.Buy {
		t.Fatalf("order 0 should be BUY (long entry), got %s", o.Side)
	}
	// Order 1 – close the long position
	if o := exec.Orders()[1]; o.Side != types.Sell {
		t.Fatalf("order 1 should be SELL to close the long, got %s", o.Side)
	}
	// Order 2 – open the new short position
	if o := exec.Orders()[2]; o.Side != types.Sell {
		t.Fatalf("order 2 should be SELL (short entry), got %s", o.Side)
	}
	// The close‑long quantity must match the original long quantity.
	if exec.Orders()[1].Qty != longQty {
		t.Fatalf("close‑long quantity (%f) should equal original long quantity (%f)",
			exec.Orders()[1].Qty, longQty)
	}
	// The short entry quantity should be positive (risk calculator may give a
	// slightly different size, but it must be > 0).
	if exec.Orders()[2].Qty <= 0 {
		t.Fatalf("short entry quantity must be positive, got %f", exec.Orders()[2].Qty)
	}
}
