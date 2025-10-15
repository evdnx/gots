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

// VolScaledPos implements a volatility‑scaled position‑size strategy.
// The entry signal is a simple HMA crossover; the size is scaled by the
// current ATSO volatility factor and the configured risk parameters.
type VolScaledPos struct {
	*BaseStrategy
}

// NewVolScaledPos builds the indicator suite (only ATSO & HMA are needed) and
// injects a logger.
func NewVolScaledPos(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger) (*VolScaledPos, error) {

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}
	base, err := NewBaseStrategy(symbol, cfg, exec, suiteFactory, log)
	if err != nil {
		return nil, err
	}
	return &VolScaledPos{BaseStrategy: base}, nil
}

// ProcessBar updates the suite, evaluates the HMA crossover, computes the
// volatility‑scaled quantity and manages the position.
func (v *VolScaledPos) ProcessBar(high, low, close, volume float64) {
	if err := v.Suite.Add(high, low, close, volume); err != nil {
		v.Log.Warn("suite_add_error", zap.Error(err))
		return
	}
	// Warm‑up: need at least a few bars for HMA & ATSO.
	if len(v.Suite.GetHMA().GetCloses()) < 10 {
		return
	}

	// 1️⃣ Entry signals.
	hBull, _ := v.Suite.GetHMA().IsBullishCrossover()
	hBear, _ := v.Suite.GetHMA().IsBearishCrossover()

	// 2️⃣ Volatility metric (ATSO raw value).
	atsoVal, _ := v.Suite.GetATSO().Calculate()
	volFactor := math.Abs(atsoVal) + 1 // +1 avoids division by zero

	// 3️⃣ ATR for stop‑loss distance (we reuse ATSO values as a proxy).
	atrVals := v.Suite.GetATSO().GetATSOValues()
	if len(atrVals) == 0 {
		atrVals = []float64{0.0001}
	}
	atr := atrVals[len(atrVals)-1]

	// 4️⃣ Position sizing – base risk scaled by volatility.
	baseRisk := v.Exec.Equity() * v.Cfg.MaxRiskPerTrade / volFactor
	stopDist := atr * v.Cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty := baseRisk / stopDist
	qty = math.Floor(qty*100) / 100 // 2‑dp rounding

	posQty, _ := v.Exec.Position(v.Symbol)

	switch {
	case hBull && posQty <= 0:
		if posQty < 0 {
			v.closePosition(close, "volscaled_close_short")
		}
		v.openLong(close, qty)

	case hBear && posQty >= 0:
		if posQty > 0 {
			v.closePosition(close, "volscaled_close_long")
		}
		v.openShort(close, qty)

	case posQty != 0 && v.Cfg.TrailingPct > 0:
		// Optional trailing‑stop.
		v.applyTrailingStop(close)
	}
}

// openLong creates a long order with the pre‑computed quantity.
func (v *VolScaledPos) openLong(price, qty float64) {
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  v.Symbol,
		Side:    types.Buy,
		Qty:     qty,
		Price:   price,
		Comment: "VolScaled entry long",
	}
	_ = v.submitOrder(o, "volscaled_long")
}

// openShort creates a short order with the pre‑computed quantity.
func (v *VolScaledPos) openShort(price, qty float64) {
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  v.Symbol,
		Side:    types.Sell,
		Qty:     qty,
		Price:   price,
		Comment: "VolScaled entry short",
	}
	_ = v.submitOrder(o, "volscaled_short")
}

// closePosition flattens the current position at market price.
func (v *VolScaledPos) closePosition(price float64, ctx string) {
	qty, _ := v.Exec.Position(v.Symbol)
	if qty == 0 {
		return
	}
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  v.Symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "VolScaled exit",
	}
	_ = v.submitOrder(o, ctx)
}
