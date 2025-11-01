package strategy

import (
	"math"

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
	fastSuite  *goti.IndicatorSuite
	slowSuite  *goti.IndicatorSuite
	fastTFSec  int
	slowTFSec  int
	lastSignal int
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
		lastSignal:   0,
	}, nil
}

// ProcessBar receives fast bars; the slow suite receives the same data
// (it internally trims to its longer window).
func (m *MultiTF) ProcessBar(high, low, close, volume float64) {
	if err := m.Suite.Add(high, low, close, volume); err != nil {
		m.Log.Warn("base_suite_add_error", zap.Error(err))
	}
	// Fast suite always receives the bar.
	if err := m.fastSuite.Add(high, low, close, volume); err != nil {
		m.Log.Warn("fast_suite_add_error", zap.Error(err))
	}
	// Slow suite receives the same bar (it will ignore excess data internally).
	if err := m.slowSuite.Add(high, low, close, volume); err != nil {
		m.Log.Warn("slow_suite_add_error", zap.Error(err))
	}
	m.recordPrice(close)
	if !m.hasHistory(15) {
		return
	}

	// Check for aligned HMA crossovers.
	fBull := m.bullishFallback()
	if ok, err := m.fastSuite.GetHMA().IsBullishCrossover(); err == nil {
		fBull = fBull || ok
	}
	fBear := m.bearishFallback()
	if ok, err := m.fastSuite.GetHMA().IsBearishCrossover(); err == nil {
		fBear = fBear || ok
	}
	sBull := m.bullishFallback()
	if ok, err := m.slowSuite.GetHMA().IsBullishCrossover(); err == nil {
		sBull = sBull || ok
	}
	sBear := m.bearishFallback()
	if ok, err := m.slowSuite.GetHMA().IsBearishCrossover(); err == nil {
		sBear = sBear || ok
	}

	trendDir := m.prices.Trend()
	longCond := trendDir > 0 && fBull && sBull
	shortCond := trendDir < 0 && fBear && sBear
	if longCond && m.lastSignal == 1 {
		longCond = false
	}
	if shortCond && m.lastSignal == -1 {
		shortCond = false
	}

	posQty, _ := m.Exec.Position(m.Symbol)

	switch {
	case longCond && posQty <= 0:
		if posQty < 0 {
			m.closePosition(close, "mtf_close_short")
		}
		m.openLong(close)
		m.lastSignal = 1

	case shortCond && posQty >= 0:
		if posQty > 0 {
			m.closePosition(close, "mtf_close_long")
		}
		m.openShort(close)
		m.lastSignal = -1

	case posQty != 0 && m.Cfg.TrailingPct > 0:
		m.applyTrailingStop(close)
		if m.Cfg.TakeProfitPct > 0 {
			m.manageTakeProfit(close)
		}
	case posQty != 0:
		if m.Cfg.TakeProfitPct > 0 {
			m.manageTakeProfit(close)
		}
	default:
		if !longCond && !shortCond {
			m.lastSignal = 0
		}
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

func (m *MultiTF) manageTakeProfit(currentPrice float64) {
	qty, avg := m.Exec.Position(m.Symbol)
	if qty == 0 {
		return
	}
	atrVals := m.Suite.GetATSO().GetATSOValues()
	atr := 0.0
	if len(atrVals) > 0 {
		atr = math.Abs(atrVals[len(atrVals)-1])
	}
	atr = m.sanitizeVolatility(atr, currentPrice)
	if qty > 0 {
		target := avg + atr*m.Cfg.TakeProfitPct
		if currentPrice >= target {
			m.closePosition(currentPrice, "mtf_tp")
		}
	} else {
		target := avg - atr*m.Cfg.TakeProfitPct
		if currentPrice <= target {
			m.closePosition(currentPrice, "mtf_tp")
		}
	}
}
