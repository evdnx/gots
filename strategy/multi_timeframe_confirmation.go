package strategy

import (
	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/types"
	"go.uber.org/zap"
)

// MultiTF confirms a signal on two time‑frames (fast & slow).
type MultiTF struct {
	*BaseStrategy
	fastSuite *goti.IndicatorSuite
	slowSuite *goti.IndicatorSuite
	fastTFSec int
	slowTFSec int
}

// NewMultiTF builds two independent suites – one for each resolution.
func NewMultiTF(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger,
	fastSec, slowSec int) (*MultiTF, error) {

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}
	fast, err := suiteFactory()
	if err != nil {
		return nil, err
	}
	slow, err := suiteFactory()
	if err != nil {
		return nil, err
	}
	base, err := NewBaseStrategy(symbol, cfg, exec, suiteFactory, log)
	if err != nil {
		return nil, err
	}
	return &MultiTF{
		BaseStrategy: base,
		fastSuite:    fast,
		slowSuite:    slow,
		fastTFSec:    fastSec,
		slowTFSec:    slowSec,
	}, nil
}

// ProcessBar receives fast bars; the slow suite receives the same data
// (it internally trims to its longer window).
func (m *MultiTF) ProcessBar(high, low, close, volume float64) {
	// Fast suite always receives the bar.
	if err := m.fastSuite.Add(high, low, close, volume); err != nil {
		m.Log.Warn("fast_suite_add_error", zap.Error(err))
	}
	// Slow suite receives the same bar (it will ignore excess data internally).
	if err := m.slowSuite.Add(high, low, close, volume); err != nil {
		m.Log.Warn("slow_suite_add_error", zap.Error(err))
	}

	// Ensure both suites have enough history.
	if len(m.fastSuite.GetHMA().GetCloses()) < 10 || len(m.slowSuite.GetHMA().GetCloses()) < 10 {
		return // warm‑up
	}

	// Check for aligned HMA crossovers.
	fBull, _ := m.fastSuite.GetHMA().IsBullishCrossover()
	fBear, _ := m.fastSuite.GetHMA().IsBearishCrossover()
	sBull, _ := m.slowSuite.GetHMA().IsBullishCrossover()
	sBear, _ := m.slowSuite.GetHMA().IsBearishCrossover()

	longCond := fBull && sBull
	shortCond := fBear && sBear

	posQty, _ := m.Exec.Position(m.Symbol)

	switch {
	case longCond && posQty <= 0:
		if posQty < 0 {
			m.closePosition(close, "mtf_close_short")
		}
		m.openLong(close)

	case shortCond && posQty >= 0:
		if posQty > 0 {
			m.closePosition(close, "mtf_close_long")
		}
		m.openShort(close)

	case posQty != 0 && m.Cfg.TrailingPct > 0:
		m.applyTrailingStop(close)
	}
}

// openLong / openShort reuse the base helpers.
func (m *MultiTF) openLong(price float64) {
	qty := m.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  m.Symbol,
		Side:    types.Buy,
		Qty:     qty,
		Price:   price,
		Comment: "MultiTF entry long",
	}
	_ = m.submitOrder(o, "mtf_long")
}

func (m *MultiTF) openShort(price float64) {
	qty := m.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  m.Symbol,
		Side:    types.Sell,
		Qty:     qty,
		Price:   price,
		Comment: "MultiTF entry short",
	}
	_ = m.submitOrder(o, "mtf_short")
}
