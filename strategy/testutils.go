package strategy

import (
	"testing"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/testutils"
	"github.com/evdnx/gots/types"
)

// buildAdaptiveStrategy builds an AdaptiveBandMR strategy wired to a mock
// executor / logger and returns both the strategy and the executor so the
// test can inspect submitted orders.
func buildAdaptiveStrategy(t *testing.T, cfg config.StrategyConfig) (*AdaptiveBandMR, *testutils.MockExecutor) {
	// Mock executor – records every order.
	mockExec := testutils.NewMockExecutor(10_000) // $10 k start equity
	mockLog := testutils.NewMockLogger()

	// Suite factory – we really create a *goti.IndicatorSuite*.
	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		// The suite will use the same thresholds we put in cfg, but we have
		// already pushed those thresholds to extreme values (see the tests),
		// so the oscillator checks become a no‑op.
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
	strat := &AdaptiveBandMR{BaseStrategy: base}
	return strat, mockExec
}

// helper to fetch the last order submitted (panics if none).
func lastOrder(exec *testutils.MockExecutor) types.Order {
	orders := exec.Orders()
	if len(orders) == 0 {
		panic("no orders recorded")
	}
	return orders[len(orders)-1]
}
