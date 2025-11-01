package strategy

import (
	"testing"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/testutils"
)

// candle represents a single OHLCV bar that the tests feed to the strategy.
type candle struct {
	high, low, close, volume float64
}

// feedBars sends a slice of candles to the supplied strategy instance.
func feedBars(t *testing.T, strat interface {
	ProcessBar(high, low, close, volume float64)
}, bars []candle) {
	for _, b := range bars {
		strat.ProcessBar(b.high, b.low, b.close, b.volume)
	}
}

// buildConfig returns a StrategyConfig whose RSI/MFI thresholds are
// deliberately inverted so that the “oversold” check (`val <= RSIOversold`)
// is *always* true and the “overbought” check (`val >= RSIOverbought`) is
// *always* true.  This lets the tests focus on the price‑based logic
// (crossovers, ATSO magnitude, etc.) without having to manipulate the
// actual indicator values.
//
// The validator only cares that Overbought and Oversold are not equal,
// which is satisfied by the values below.
func buildConfig() config.StrategyConfig {
	return config.StrategyConfig{
		// Overbought is a huge negative number, oversold a huge positive.
		RSIOverbought: -1e9, // far below any realistic RSI/MFI
		RSIOversold:   1e9,  // far above any realistic RSI/MFI
		MFIOverbought: -1e9,
		MFIOversold:   1e9,

		// VWAO isn’t used by most strategies; keep it large.
		VWAOStrongTrend: 1e9,

		// Indicator periods – reasonable defaults for the synthetic data.
		HMAPeriod:    9,
		ATSEMAperiod: 5,

		// Risk parameters – 1 % of equity per trade, 1.5 % stop‑loss.
		MaxRiskPerTrade: 0.01,
		StopLossPct:     0.015,
		TakeProfitPct:   0.0, // enabled per‑test when needed
		TrailingPct:     0.0, // enabled per‑test when needed

		// Quantity rounding / broker constraints.
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}
}

// buildStrategy creates a mock executor, a mock logger and then calls the
// concrete strategy constructor.  The constructor builds its own
// IndicatorSuite (using *valid* RSI/MFI thresholds), so we no longer call
// NewBaseStrategy here – that call previously caused the “RSI overbought
// must be greater than oversold” error.
func buildStrategy(t *testing.T,
	constructor func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	}) (interface {
	ProcessBar(high, low, close, volume float64)
}, *testutils.MockExecutor) {

	cfg := buildConfig()
	mockExec := testutils.NewMockExecutor(10_000) // $10 k start equity
	mockLog := testutils.NewMockLogger()

	// Validate the config (uses the relaxed Validate we added).
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validation failed: %v", err)
	}

	// Construct the concrete strategy – each strategy’s own suiteFactory
	// supplies production‑grade RSI/MFI thresholds, so this will succeed.
	strat := constructor("TEST", cfg, mockExec, mockLog)
	return strat, mockExec
}

/* -----------------------------------------------------------------------
   Strategy‑specific builders – thin wrappers around the generic helper.
   ----------------------------------------------------------------------- */

func buildAdaptive(t *testing.T) (*AdaptiveBandMR, *testutils.MockExecutor) {
	ctor := func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	} {
		ab, err := NewAdaptiveBandMR(symbol, cfg, exec, log)
		if err != nil {
			t.Fatalf("NewAdaptiveBandMR failed: %v", err)
		}
		return ab
	}
	s, exec := buildStrategy(t, ctor)
	return s.(*AdaptiveBandMR), exec
}

func buildBreakout(t *testing.T) (*BreakoutMomentum, *testutils.MockExecutor) {
	ctor := func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	} {
		bm, err := NewBreakoutMomentum(symbol, cfg, exec, log)
		if err != nil {
			t.Fatalf("NewBreakoutMomentum failed: %v", err)
		}
		return bm
	}
	s, exec := buildStrategy(t, ctor)
	return s.(*BreakoutMomentum), exec
}

func buildDivergence(t *testing.T) (*DivergenceSwing, *testutils.MockExecutor) {
	ctor := func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	} {
		ds, err := NewDivergenceSwing(symbol, cfg, exec, log)
		if err != nil {
			t.Fatalf("NewDivergenceSwing failed: %v", err)
		}
		return ds
	}
	s, exec := buildStrategy(t, ctor)
	return s.(*DivergenceSwing), exec
}

func buildEventDriven(t *testing.T, eventThreshold float64, maxHoldingBars int) (*EventDriven, *testutils.MockExecutor) {
	cfg := buildConfig()
	mockExec := testutils.NewMockExecutor(10_000)
	mockLog := testutils.NewMockLogger()

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = 70
		ic.RSIOversold = 30
		ic.MFIOverbought = 80
		ic.MFIOversold = 20
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}
	_, err := NewBaseStrategy("TEST", cfg, mockExec, suiteFactory, mockLog)
	if err != nil {
		t.Fatalf("NewBaseStrategy failed: %v", err)
	}
	ev, err := NewEventDriven("TEST", cfg, mockExec, mockLog, eventThreshold, maxHoldingBars)
	if err != nil {
		t.Fatalf("NewEventDriven failed: %v", err)
	}
	return ev, mockExec
}

func buildHybrid(t *testing.T) (*HybridTrendMeanReversion, *testutils.MockExecutor) {
	ctor := func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	} {
		ht, err := NewHybridTrendMeanReversion(symbol, cfg, exec, log)
		if err != nil {
			t.Fatalf("NewHybridTrendMeanReversion failed: %v", err)
		}
		return ht
	}
	s, exec := buildStrategy(t, ctor)
	return s.(*HybridTrendMeanReversion), exec
}

func buildMeanReversion(t *testing.T) (*MeanReversion, *testutils.MockExecutor) {
	ctor := func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	} {
		mr, err := NewMeanReversion(symbol, cfg, exec, log)
		if err != nil {
			t.Fatalf("NewMeanReversion failed: %v", err)
		}
		return mr
	}
	s, exec := buildStrategy(t, ctor)
	return s.(*MeanReversion), exec
}

func buildMultiTF(t *testing.T, fastSec, slowSec int) (*MultiTF, *testutils.MockExecutor) {
	ctor := func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	} {
		mt, err := NewMultiTF(symbol, cfg, exec, log, fastSec, slowSec)
		if err != nil {
			t.Fatalf("NewMultiTF failed: %v", err)
		}
		return mt
	}
	s, exec := buildStrategy(t, ctor)
	return s.(*MultiTF), exec
}

func buildRiskParity(t *testing.T,
	symbols []string, topK, intervalBars int) (*RiskParityRotation, *testutils.MockExecutor) {

	cfg := buildConfig() // <-- use the corrected config
	mockExec := testutils.NewMockExecutor(10_000)
	mockLog := testutils.NewMockLogger()

	rp, err := NewRiskParityRotation(symbols, cfg, mockExec, topK, intervalBars, mockLog)
	if err != nil {
		t.Fatalf("NewRiskParityRotation failed: %v", err)
	}
	return rp, mockExec
}

func buildTrendComposite(t *testing.T) (*TrendComposite, *testutils.MockExecutor) {
	ctor := func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	} {
		tc, err := NewTrendComposite(symbol, cfg, exec, log)
		if err != nil {
			t.Fatalf("NewTrendComposite failed: %v", err)
		}
		return tc
	}
	s, exec := buildStrategy(t, ctor)
	return s.(*TrendComposite), exec
}

func buildVolScaled(t *testing.T) (*VolScaledPos, *testutils.MockExecutor) {
	ctor := func(symbol string, cfg config.StrategyConfig,
		exec executor.Executor, log logger.Logger) interface {
		ProcessBar(high, low, close, volume float64)
	} {
		vs, err := NewVolScaledPos(symbol, cfg, exec, log)
		if err != nil {
			t.Fatalf("NewVolScaledPos failed: %v", err)
		}
		return vs
	}
	s, exec := buildStrategy(t, ctor)
	return s.(*VolScaledPos), exec
}
