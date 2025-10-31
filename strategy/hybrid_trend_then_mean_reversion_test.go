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
Test 1 – Idle → Trend (bullish HMA) → Revert (flat‑bar timeout)
-----------------------------------------------------------------------
*/
func TestHybridTrendMeanReversion_FSM_IdleTrendRevert(t *testing.T) {
	ht, exec := buildHybrid(t)

	/* -------- Phase A – bullish HMA crossover (enter Trend) -------- */
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
	feedBars(t, ht, up)

	if ht.state != stateTrend {
		t.Fatalf("expected stateTrend after bullish HMA, got %v", ht.state)
	}
	if ht.trendSide != types.Buy {
		t.Fatalf("expected trendSide BUY, got %v", ht.trendSide)
	}
	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected one BUY entry order, got %+v", exec.Orders())
	}

	/* -------- Phase B – flat bars → transition to Revert -------- */
	var flat []candle
	for i := 0; i < 4; i++ { // > flatBarThreshold (3)
		price := 115.0
		flat = append(flat, candle{
			high:   price + 0.2,
			low:    price - 0.2,
			close:  price,
			volume: 900,
		})
	}
	feedBars(t, ht, flat)

	if ht.state != stateRevert {
		t.Fatalf("expected stateRevert after flat‑bar timeout, got %v", ht.state)
	}
	// The exit‑trend order is emitted when the trend is closed.
	if len(exec.Orders()) != 2 {
		t.Fatalf("expected a second order (trend exit), got %d", len(exec.Orders()))
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL order to close the long trend, got %s", exec.Orders()[1].Side)
	}
}

/*
-----------------------------------------------------------------------
Test 2 – Revert → opposite short entry after overbought signal.
-----------------------------------------------------------------------
*/
func TestHybridTrendMeanReversion_RevertOppositeEntry(t *testing.T) {
	// Use normal thresholds so the overbought check can fire.
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
	ht := &HybridTrendMeanReversion{
		BaseStrategy:   base,
		state:          stateIdle,
		trendSide:      "",
		flatBarCounter: 0,
	}

	/* -------- Phase A – bullish trend entry -------- */
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
	feedBars(t, ht, up)

	if ht.state != stateTrend || ht.trendSide != types.Buy {
		t.Fatalf("failed to enter bullish trend: state=%v side=%v", ht.state, ht.trendSide)
	}
	if len(mockExec.Orders()) != 1 || mockExec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", mockExec.Orders())
	}

	/* -------- Phase B – flat bars → Revert -------- */
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
	feedBars(t, ht, flat)

	if ht.state != stateRevert {
		t.Fatalf("expected stateRevert after flat‑bars, got %v", ht.state)
	}
	if len(mockExec.Orders()) != 2 {
		t.Fatalf("expected second order (trend exit), got %d", len(mockExec.Orders()))
	}
	if mockExec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL order to close the long trend, got %s", mockExec.Orders()[1].Side)
	}

	/* -------- Phase C – overbought signal → opposite short -------- */
	over := []candle{
		{
			high:   200,
			low:    190,
			close:  195, // pushes RSI/MFI over their overbought levels
			volume: 1500,
		},
	}
	feedBars(t, ht, over)

	if ht.state != stateIdle {
		t.Fatalf("expected FSM to return to Idle after opposite entry, got %v", ht.state)
	}
	if len(mockExec.Orders()) != 3 {
		t.Fatalf("expected third order (short entry) after overbought signal, got %d", len(mockExec.Orders()))
	}
	if mockExec.Orders()[2].Side != types.Sell {
		t.Fatalf("expected SELL order for opposite short entry, got %s", mockExec.Orders()[2].Side)
	}
}
