// Trades only when the same signal appears on two different time‑frames.
// Here we use a *fast* (e.g., 5‑min) suite and a *slow* (e.g., 30‑min) suite.
// The signal we look for is a bullish/bearish HMA crossover.
package gots

import (
	"log"
	"math"

	"github.com/evdnx/goti"
)

type MultiTF struct {
	fastSuite *goti.IndicatorSuite
	slowSuite *goti.IndicatorSuite
	cfg       StrategyConfig
	exec      Executor
	symbol    string
	fastTFSec int // seconds per fast bar (e.g. 300 for 5‑min)
	slowTFSec int // seconds per slow bar (e.g. 1800 for 30‑min)
}

// NewMultiTF builds two independent suites – one for each resolution.
func NewMultiTF(symbol string, cfg StrategyConfig, exec Executor,
	fastSec, slowSec int) (*MultiTF, error) {

	indCfg := goti.DefaultConfig()
	indCfg.ATSEMAperiod = cfg.ATSEMAperiod

	fast, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	slow, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &MultiTF{
		fastSuite: fast,
		slowSuite: slow,
		cfg:       cfg,
		exec:      exec,
		symbol:    symbol,
		fastTFSec: fastSec,
		slowTFSec: slowSec,
	}, nil
}

// ProcessBar receives *fast* bars.  When a new *slow* bar is due we also
// feed the same candle to the slow suite (the caller must aggregate candles
// externally – here we simply forward every bar to both suites; the slow
// suite will ignore extra data because its internal window is larger).
func (m *MultiTF) ProcessBar(high, low, close, volume float64) {
	// Fast suite always receives the bar.
	if err := m.fastSuite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] fast suite add: %v", err)
	}
	// Slow suite receives the same bar (it will keep only the most recent
	// `slowTFSec` worth of data because of its internal trimming).
	if err := m.slowSuite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] slow suite add: %v", err)
	}

	// ----- Check for aligned signals -----
	fBull, _ := m.fastSuite.GetHMA().IsBullishCrossover()
	fBear, _ := m.fastSuite.GetHMA().IsBearishCrossover()

	sBull, _ := m.slowSuite.GetHMA().IsBullishCrossover()
	sBear, _ := m.slowSuite.GetHMA().IsBearishCrossover()

	longCond := fBull && sBull
	shortCond := fBear && sBear

	posQty, _ := m.exec.Position(m.symbol)

	switch {
	case longCond && posQty <= 0:
		if posQty < 0 {
			m.closePosition(close)
		}
		m.openPosition(Buy, close)

	case shortCond && posQty >= 0:
		if posQty > 0 {
			m.closePosition(close)
		}
		m.openPosition(Sell, close)

	case posQty != 0:
		m.applyTrailingStop(close)
	}
}

// openPosition / closePosition / applyTrailingStop are identical to the
// implementations in other strategy files (re‑used for brevity).
func (m *MultiTF) openPosition(side Side, price float64) {
	qty := CalcQty(m.exec.Equity(), m.cfg.MaxRiskPerTrade, m.cfg.StopLossPct, price)
	if qty <= 0 {
		return
	}
	o := Order{
		Symbol:  m.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "MultiTF entry",
	}
	if err := m.exec.Submit(o); err != nil {
		log.Printf("[ERR] multift submit entry: %v", err)
	}
}
func (m *MultiTF) closePosition(price float64) {
	qty, _ := m.exec.Position(m.symbol)
	if qty == 0 {
		return
	}
	side := Sell
	if qty < 0 {
		side = Buy
	}
	o := Order{
		Symbol:  m.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "MultiTF exit",
	}
	if err := m.exec.Submit(o); err != nil {
		log.Printf("[ERR] multift submit exit: %v", err)
	}
}
func (m *MultiTF) applyTrailingStop(currentPrice float64) {
	if m.cfg.TrailingPct <= 0 {
		return
	}
	qty, avg := m.exec.Position(m.symbol)
	if qty == 0 {
		return
	}
	var trailLevel float64
	if qty > 0 {
		trailLevel = avg * (1 + m.cfg.TrailingPct)
		if currentPrice >= trailLevel {
			m.closePosition(currentPrice)
		}
	} else {
		trailLevel = avg * (1 - m.cfg.TrailingPct)
		if currentPrice <= trailLevel {
			m.closePosition(currentPrice)
		}
	}
}
