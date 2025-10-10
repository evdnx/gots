// Position‑size strategy that scales each trade by the *current* volatility.
// Uses ATSO (adaptive period) to gauge volatility and ATR for an absolute
// stop‑distance.  The entry signal itself is a simple HMA bullish/bearish
// crossover – the novelty is the *size* of the trade.
package gots

import (
	"log"
	"math"

	"github.com/evdnx/goti"
)

type VolScaledPos struct {
	suite  *goti.IndicatorSuite
	cfg    StrategyConfig
	exec   Executor
	symbol string
}

// NewVolScaledPos builds the suite and stores the config.
func NewVolScaledPos(symbol string, cfg StrategyConfig, exec Executor) (*VolScaledPos, error) {
	indCfg := goti.DefaultConfig()
	indCfg.ATSEMAperiod = cfg.ATSEMAperiod
	suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &VolScaledPos{
		suite:  suite,
		cfg:    cfg,
		exec:   exec,
		symbol: symbol,
	}, nil
}

// ProcessBar updates the suite and decides on a trade.
func (v *VolScaledPos) ProcessBar(high, low, close, volume float64) {
	if err := v.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] vol‑scaled add error: %v", err)
		return
	}

	// ---- ENTRY SIGNAL (simple HMA crossover) ----
	hBull, _ := v.suite.GetHMA().IsBullishCrossover()
	hBear, _ := v.suite.GetHMA().IsBearishCrossover()

	// ---- VOLATILITY METRIC ----
	// ATSO raw value is a z‑score; its absolute magnitude grows with volatility.
	atsoVal, _ := v.suite.GetATSO().Calculate()
	volFactor := math.Abs(atsoVal) + 1 // +1 to avoid zero

	// ---- STOP‑LOSS DISTANCE (ATR) ----
	atrVal := v.suite.GetATSO().GetATSOValues() // we need a raw number; fallback to 1 if missing
	if len(atrVal) == 0 {
		atrVal = []float64{1}
	}
	atr := atrVal[len(atrVal)-1]

	// ---- POSITION SIZE ----
	// Base risk = MaxRiskPerTrade * equity.
	// Scale it by volatility factor (larger volatility ⇒ smaller position).
	baseRisk := v.exec.Equity() * v.cfg.MaxRiskPerTrade / volFactor
	stopDist := atr * v.cfg.StopLossPct // absolute stop distance
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty := baseRisk / stopDist
	qty = math.Floor(qty*100) / 100 // 2‑dp rounding

	posQty, _ := v.exec.Position(v.symbol)

	switch {
	case hBull && posQty <= 0:
		if posQty < 0 {
			v.closePosition(close)
		}
		v.openPosition(Buy, qty, close)

	case hBear && posQty >= 0:
		if posQty > 0 {
			v.closePosition(close)
		}
		v.openPosition(Sell, qty, close)

	case posQty != 0:
		// Apply trailing stop (optional)
		if v.cfg.TrailingPct > 0 {
			v.applyTrailingStop(close)
		}
	}
}

// openPosition – market order sized by the pre‑computed qty.
func (v *VolScaledPos) openPosition(side Side, qty float64, price float64) {
	if qty <= 0 {
		return
	}
	o := Order{
		Symbol:  v.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "VolScaled entry",
	}
	if err := v.exec.Submit(o); err != nil {
		log.Printf("[ERR] vol‑scaled submit entry: %v", err)
	}
}

// closePosition – flatten the current position.
func (v *VolScaledPos) closePosition(price float64) {
	qty, _ := v.exec.Position(v.symbol)
	if qty == 0 {
		return
	}
	side := Sell
	if qty < 0 {
		side = Buy
	}
	o := Order{
		Symbol:  v.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "VolScaled exit",
	}
	if err := v.exec.Submit(o); err != nil {
		log.Printf("[ERR] vol‑scaled submit exit: %v", err)
	}
}

// applyTrailingStop – same logic as in other strategies.
func (v *VolScaledPos) applyTrailingStop(currentPrice float64) {
	if v.cfg.TrailingPct <= 0 {
		return
	}
	qty, avg := v.exec.Position(v.symbol)
	if qty == 0 {
		return
	}
	var trailLevel float64
	if qty > 0 {
		trailLevel = avg * (1 + v.cfg.TrailingPct)
		if currentPrice >= trailLevel {
			v.closePosition(currentPrice)
		}
	} else {
		trailLevel = avg * (1 - v.cfg.TrailingPct)
		if currentPrice <= trailLevel {
			v.closePosition(currentPrice)
		}
	}
}
