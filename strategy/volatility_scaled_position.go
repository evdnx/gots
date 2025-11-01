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
	v.recordPrice(close)
	if !v.hasHistory(15) {
		return
	}

	// 1️⃣ Entry signals.
	hBull := v.bullishFallback()
	if ok, err := v.Suite.GetHMA().IsBullishCrossover(); err == nil {
		hBull = hBull || ok
	}
	hBear := v.bearishFallback()
	if ok, err := v.Suite.GetHMA().IsBearishCrossover(); err == nil {
		hBear = hBear || ok
	}

	// 2️⃣ Volatility metric (ATSO raw value).
	atsoValRaw, err := v.Suite.GetATSO().Calculate()
	if err != nil {
		atsoValRaw = v.prices.Slope()
	}
	volFactor := v.sanitizeVolatility(math.Abs(atsoValRaw), close) + 1 // +1 avoids division by zero

	// 3️⃣ ATR for stop‑loss distance (we reuse ATSO values as a proxy).
	atrVals := v.Suite.GetATSO().GetATSOValues()
	atr := 0.0
	if len(atrVals) > 0 {
		atr = math.Abs(atrVals[len(atrVals)-1])
	}
	atr = v.sanitizeVolatility(atr, close)

	// 4️⃣ Position sizing – base risk scaled by volatility.
	baseRisk := v.Exec.Equity() * v.Cfg.MaxRiskPerTrade / volFactor
	stopDist := atr * v.Cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty := baseRisk / stopDist
	maxQty := v.Exec.Equity() / close
	if maxQty > 0 && qty > maxQty {
		qty = maxQty
	}
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
		if v.Cfg.TakeProfitPct > 0 {
			v.manageTakeProfit(close)
		}
	case posQty != 0:
		if v.Cfg.TakeProfitPct > 0 {
			v.manageTakeProfit(close)
		}
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

func (v *VolScaledPos) manageTakeProfit(currentPrice float64) {
	qty, avg := v.Exec.Position(v.Symbol)
	if qty == 0 {
		return
	}
	atrVals := v.Suite.GetATSO().GetATSOValues()
	atr := 0.0
	if len(atrVals) > 0 {
		atr = math.Abs(atrVals[len(atrVals)-1])
	}
	atr = v.sanitizeVolatility(atr, avg)
	if qty > 0 {
		target := avg + atr*v.Cfg.TakeProfitPct
		if currentPrice >= target {
			v.closePosition(currentPrice, "volscaled_tp")
		}
	} else {
		target := avg - atr*v.Cfg.TakeProfitPct
		if currentPrice <= target {
			v.closePosition(currentPrice, "volscaled_tp")
		}
	}
}
