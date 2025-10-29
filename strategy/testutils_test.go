package strategy

import (
	"testing"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/testutils"
)

// ---------------------------------------------------------------------
// Minimal interface that all strategy structs satisfy (they all expose
// ProcessBar).  Using a named interface keeps the signature readable.
// ---------------------------------------------------------------------
type barProcessor interface {
	ProcessBar(high, low, close, volume float64)
}

/*
buildStrategy is a generic factory used by the individual “buildXXX”
helpers (AdaptiveBand, BreakoutMomentum, etc.).

Parameters
----------
t           – *testing.T* (so we can fail the test on construction errors)
constructor – a closure that knows how to build the concrete strategy.

	Its signature must match the usual strategy constructor:
	    NewXxx(symbol string, cfg config.StrategyConfig,
	          exec executor.Executor, log logger.Logger) (*Xxx, error)

Returns
-------
(barProcessor, *executor.MockExecutor)

	– the constructed strategy (as a barProcessor) and the mock executor
	  that records every order for assertions.
*/
func buildStrategy(
	t *testing.T,
	constructor func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) (barProcessor, error),
) (barProcessor, *testutils.MockExecutor) {

	// -----------------------------------------------------------------
	// 1️⃣  Build a StrategyConfig with *inverted* RSI/MFI thresholds.
	//     This makes the RSI/MFI checks always succeed, allowing the
	//     tests to focus on price‑based logic (HMA crossovers,
	//     ATSO magnitude, etc.).
	// -----------------------------------------------------------------
	cfg := config.StrategyConfig{
		RSIOverbought:     -1e9, // overbought check always true
		RSIOversold:       1e9,  // oversold check always true
		MFIOverbought:     -1e9,
		MFIOversold:       1e9,
		VWAOStrongTrend:   1e9, // not used by most strategies
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

	// -----------------------------------------------------------------
	// 2️⃣  Create the mock executor and logger.
	// -----------------------------------------------------------------
	mockExec := testutils.NewMockExecutor(10_000) // $10 k start equity
	mockLog := testutils.NewMockLogger()

	// -----------------------------------------------------------------
	// 3️⃣  Suite factory – returns a *real* goti.IndicatorSuite.
	// -----------------------------------------------------------------
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

	// -----------------------------------------------------------------
	// 4️⃣  Initialise the BaseStrategy (this validates the config and
	//     creates the real indicator suite).  The concrete strategy
	//     constructor will reuse this BaseStrategy internally.
	// -----------------------------------------------------------------
	_, err := NewBaseStrategy("TEST", cfg, mockExec, suiteFactory, mockLog)
	if err != nil {
		t.Fatalf("NewBaseStrategy failed: %v", err)
	}

	// -----------------------------------------------------------------
	// 5️⃣  Build the concrete strategy via the supplied constructor.
	// -----------------------------------------------------------------
	strat, err := constructor("TEST", cfg, mockExec, mockLog)
	if err != nil {
		t.Fatalf("strategy constructor failed: %v", err)
	}

	return strat, mockExec
}
