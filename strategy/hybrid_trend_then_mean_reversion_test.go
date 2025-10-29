// strategy/hybrid_trend_then_mean_reversion_test.go
package strategy

import (
	"testing"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/testutils"
	"github.com/evdnx/gots/types"
)

/*
-----------------------------------------------------------------------
Helper: builds a HybridTrendMeanReversion strategy wired to a mock
executor / logger.  The suiteFactory creates a *real* goti.IndicatorSuite.
All oscillator thresholds are set to
very wide values so the RSI/MFI checks are effectively a no‑op – the
test can concentrate on the HMA‑driven FSM.
-----------------------------------------------------------------------
*/
func buildHybrid(t *testing.T) (*HybridTrendMeanReversion, *testutils.MockExecutor) {
	// Very permissive thresholds – they will never block a trade.
	cfg := config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		HMAPeriod:         9,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.01,
		StopLossPct:       0.015,
		TakeProfitPct:     0.0,
		TrailingPct:       0.0,
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
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}

	base, err := NewBaseStrategy("TEST", cfg, mockExec, suiteFactory, mockLog)
	if err != nil {
		t.Fatalf("NewBaseStrategy failed: %v", err)
	}
	hyb := &HybridTrendMeanReversion{
		BaseStrategy:   base,
		state:          stateIdle,
		trendSide:      "",
		flatBarCounter: 0,
	}
	return hyb, mockExec
}

func feedBarsHybridTrendMeanReversion(t *testing.T, h *HybridTrendMeanReversion, bars []candle) {
	for i, b := range bars {
		h.ProcessBar(b.high, b.low, b.close, b.volume)
		// The strategy does a warm‑up check (`len(Suite.GetHMA().GetCloses()) < 10`);
		// we don’t need to assert anything per‑bar – the test will look at the
		// final state after the whole slice has been processed.
		_ = i // silence unused‑var warning if you add debug prints later
	}
}

/*
-----------------------------------------------------------------------
Test 1 – Idle → Trend (bullish HMA) → Revert (flat‑bar timeout)
-----------------------------------------------------------------------
1️⃣ Warm‑up + monotonic rise → HMA bullish crossover → long entry.
2️⃣ Several flat bars (no HMA cross) → flatBarCounter reaches threshold → stateRevert.
*/
func TestHybridTrendMeanReversion_FSM_IdleTrendRevert(t *testing.T) {
	h, exec := buildHybrid(t)

	/*
	   Phase A – generate a bullish HMA crossover.
	   We feed a steadily rising close price; after the built‑in warm‑up
	   (≈10 bars for HMA) the HMA line will cross above the price,
	   causing `IsBullishCrossover()` to return true.
	*/
	var rising []candle
	for i := 1; i <= 15; i++ {
		price := float64(100 + i) // 101, 102, … 115
		rising = append(rising, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBarsHybridTrendMeanReversion(t, h, rising)

	// After the bullish crossover we expect:
	if h.state != stateTrend {
		t.Fatalf("expected stateTrend after bullish HMA, got %v", h.state)
	}
	if h.trendSide != types.Buy {
		t.Fatalf("expected trendSide BUY, got %v", h.trendSide)
	}
	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one entry order after trend entry, got %d", len(exec.Orders()))
	}
	if exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected BUY order for trend entry, got %s", exec.Orders()[0].Side)
	}

	/*
	   Phase B – flat bars (no HMA crossover).  The strategy counts them
	   in `flatBarCounter`.  After `flatBarThreshold` (hard‑coded to 3)
	   it should exit the trend and move to `stateRevert`.
	*/
	var flat []candle
	for i := 0; i < 4; i++ { // 4 > threshold to guarantee transition
		price := 115.0 // same close each bar → no new HMA cross
		flat = append(flat, candle{
			high:   price + 0.2,
			low:    price - 0.2,
			close:  price,
			volume: 900,
		})
	}
	feedBarsHybridTrendMeanReversion(t, h, flat)

	if h.state != stateRevert {
		t.Fatalf("expected stateRevert after flat‑bar timeout, got %v", h.state)
	}
	// The exit‑trend order is emitted when the trend is closed.
	if len(exec.Orders()) != 2 {
		t.Fatalf("expected a second order (trend exit) after entering Revert, got %d", len(exec.Orders()))
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL order to close the long trend, got %s", exec.Orders()[1].Side)
	}
}

/*
-----------------------------------------------------------------------
Test 2 – Revert → Opposite entry (short) after overbought signal.
-----------------------------------------------------------------------
After reaching `stateRevert` we feed a bar that pushes RSI/MFI
above the (normal) overbought levels, causing the strategy to open a
short position opposite to the original trend.
Because the earlier test used extreme thresholds, we now construct a
fresh strategy with *real* thresholds so the overbought check can fire.
*/
func TestHybridTrendMeanReversion_RevertOppositeEntry(t *testing.T) {
	// Normal thresholds – we want the overbought check to matter.
	cfg := config.StrategyConfig{
		RSIOverbought:     70,
		RSIOversold:       30,
		MFIOverbought:     80,
		MFIOversold:       20,
		HMAPeriod:         9,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.01,
		StopLossPct:       0.015,
		TakeProfitPct:     0.0,
		TrailingPct:       0.0,
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}

	mockExec := testutils.NewMockExecutor(10_000)
	mockLog := testutils.NewMockLogger()

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = cfg.RSIOverbought
		ic.RSIOversold = cfg.RSIOversold
		ic.MFIOverbought = cfg.MFIOverbought
		ic.MFIOversold = cfg.MFIOversold
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}

	base, err := NewBaseStrategy("TEST", cfg, mockExec, suiteFactory, mockLog)
	if err != nil {
		t.Fatalf("NewBaseStrategy failed: %v", err)
	}
	hyb := &HybridTrendMeanReversion{
		BaseStrategy:   base,
		state:          stateIdle,
		trendSide:      "",
		flatBarCounter: 0,
	}

	/*
	   Phase A – force a bullish HMA crossover to enter the trend.
	   A simple upward ramp works for the HMA as in the previous test.
	*/
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
	feedBarsHybridTrendMeanReversion(t, hyb, up)

	if hyb.state != stateTrend || hyb.trendSide != types.Buy {
		t.Fatalf("failed to enter bullish trend: state=%v side=%v", hyb.state, hyb.trendSide)
	}
	if len(mockExec.Orders()) != 1 || mockExec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", mockExec.Orders())
	}

	/*
	   Phase B – flat bars to push us into the Revert state.
	*/
	var flat []candle
	for i := 0; i < 4; i++ {
		price := 115.0
		flat = append(flat, candle{
			high:   price + 0.2,
			low:    price - 0.2,
			close:  price,
			volume: 900,
		})
	}
	feedBarsHybridTrendMeanReversion(t, hyb, flat)

	if hyb.state != stateRevert {
		t.Fatalf("expected stateRevert after flat‑bars, got %v", hyb.state)
	}
	// One extra order for exiting the trend.
	if len(mockExec.Orders()) != 2 {
		t.Fatalf("expected two orders after entering Revert, got %d", len(mockExec.Orders()))
	}
	if mockExec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL order to close the long trend, got %s", mockExec.Orders()[1].Side)
	}

	/*
	   Phase C – overbought signal.
	   We craft a bar with a *very high* close price; the built‑in RSI/MFI
	   calculators will quickly swing above the overbought thresholds
	   (70 / 80).  The HMA is flat (no new crossover), satisfying the
	   “!hmaBull” part of the long‑condition and the “!hmaBear” part of
	   the short‑condition.
	*/
	overbought := []candle{
		{
			high:   200,
			low:    190,
			close:  195, // large jump → RSI/MFI go high
			volume: 1500,
		},
	}
	feedBarsHybridTrendMeanReversion(t, hyb, overbought)

	/*
	   At this point the strategy should have opened a short position
	   (opposite of the original long trend).
	*/
	if hyb.state != stateIdle {
		t.Fatalf("expected FSM to return to Idle after opposite entry, got %v", hyb.state)
	}
	if len(mockExec.Orders()) != 3 {
		t.Fatalf("expected third order (short entry) after overbought signal, got %d", len(mockExec.Orders()))
	}
	if mockExec.Orders()[2].Side != types.Sell {
		t.Fatalf("expected SELL order for opposite short entry, got %s", mockExec.Orders()[2].Side)
	}
}
