package strategy

import (
	"testing"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/testutils"
)

// ---------------------------------------------------------------------
// Helper types
// ---------------------------------------------------------------------

// candle represents a single OHLCV bar that the tests feed to the strategy.
type candle struct {
	high, low, close, volume float64
}

// feedBars iterates over a slice of candles and calls ProcessBar on the
// supplied MeanReversion instance.
func feedBarsMr(t *testing.T, mr *MeanReversion, bars []candle) {
	for _, b := range bars {
		mr.ProcessBar(b.high, b.low, b.close, b.volume)
	}
}

// ---------------------------------------------------------------------
// buildMeanReversion
// ---------------------------------------------------------------------
//
// Constructs a MeanReversion strategy wired to a mock executor and logger.
// The suiteFactory creates a **real** goti.IndicatorSuite (the same type the
// production code uses).  All oscillator thresholds are set to extreme values
// so the RSI/MFI/VWAO value checks are always satisfied – the tests only
// need to control the crossover direction via the price series.
//
// Returns:
//
//	*MeanReversion – the ready‑to‑use strategy instance
//	*executor.MockExecutor – the mock executor that records every order
func buildMeanReversion(t *testing.T) (*MeanReversion, *testutils.MockExecutor) {
	// Extremely permissive thresholds – they will never block a trade.
	cfg := config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		VWAOStrongTrend:   1e9, // not used directly by MeanReversion
		HMAPeriod:         9,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.01,  // 1 % of equity per trade
		StopLossPct:       0.015, // 1.5 %
		TakeProfitPct:     0.0,   // enabled per‑test when needed
		TrailingPct:       0.0,   // enabled per‑test when needed
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}

	// Mock executor records orders and equity changes.
	mockExec := testutils.NewMockExecutor(10_000) // start with $10 k equity
	mockLog := testutils.NewMockLogger()

	// Suite factory – returns a *real* goti.IndicatorSuite.
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

	// Build the BaseStrategy (validation happens inside NewBaseStrategy).
	base, err := NewBaseStrategy("TEST", cfg, mockExec, suiteFactory, mockLog)
	if err != nil {
		t.Fatalf("NewBaseStrategy failed: %v", err)
	}

	// Assemble the concrete MeanReversion strategy.
	mr := &MeanReversion{BaseStrategy: base}
	return mr, mockExec
}
