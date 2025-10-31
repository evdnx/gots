package strategy

import (
	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/types"
	"go.uber.org/zap"
)

// AdaptiveBandMR implements the ATR‑adaptive band mean‑reversion strategy.
type AdaptiveBandMR struct {
	*BaseStrategy
}

// NewAdaptiveBandMR constructs the strategy, validates config and injects a logger.
func NewAdaptiveBandMR(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger) (*AdaptiveBandMR, error) {

	// Build suite with user‑provided thresholds.
	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = 70
		ic.RSIOversold = 30
		ic.MFIOverbought = 80
		ic.MFIOversold = 20
		return goti.NewIndicatorSuiteWithConfig(ic)
	}

	base, err := NewBaseStrategy(symbol, cfg, exec, suiteFactory, log)
	if err != nil {
		return nil, err
	}
	return &AdaptiveBandMR{BaseStrategy: base}, nil
}

// ProcessBar updates the suite and decides whether to open/close a trade.
func (a *AdaptiveBandMR) ProcessBar(high, low, close, volume float64) {
	// Warm‑up: ensure we have enough data for the indicators.
	if err := a.Suite.Add(high, low, close, volume); err != nil {
		a.Log.Warn("suite_add_error", zap.Error(err))
		return
	}
	if len(a.Suite.GetRSI().GetCloses()) < 14 { // example warm‑up length
		return
	}

	// 1️⃣ Pull latest indicator values.
	atrVals := a.Suite.GetATSO().GetATSOValues()
	if len(atrVals) == 0 {
		return
	}
	atr := atrVals[len(atrVals)-1]

	rsiVal, _ := a.Suite.GetRSI().Calculate()
	mfiVal, _ := a.Suite.GetMFI().Calculate()
	hmaBull, _ := a.Suite.GetHMA().IsBullishCrossover()
	hmaBear, _ := a.Suite.GetHMA().IsBearishCrossover()

	// 2️⃣ Build adaptive band.
	bandWidth := close * a.Cfg.StopLossPct // reuse StopLossPct as band factor
	upperBand := close + bandWidth + atr
	lowerBand := close - bandWidth - atr

	// 3️⃣ Entry conditions.
	longCond := low <= lowerBand && rsiVal <= a.Cfg.RSIOversold && mfiVal <= a.Cfg.MFIOversold && !hmaBull
	shortCond := high >= upperBand && rsiVal >= a.Cfg.RSIOverbought && mfiVal >= a.Cfg.MFIOverbought && !hmaBear

	posQty, _ := a.Exec.Position(a.Symbol)

	switch {
	case longCond && posQty <= 0:
		if posQty < 0 {
			a.closePosition(close, "adaptiveband_rev_close_short")
		}
		a.openLong(close, atr)

	case shortCond && posQty >= 0:
		if posQty > 0 {
			a.closePosition(close, "adaptiveband_rev_close_long")
		}
		a.openShort(close, atr)

	case posQty != 0:
		// Manage existing position – trailing stop & optional TP.
		if a.Cfg.TrailingPct > 0 {
			a.applyTrailingStop(close)
		}
		if a.Cfg.TakeProfitPct > 0 {
			a.manageTakeProfit(close, atr)
		}
	}
}

// openLong creates a long order sized by risk.
func (a *AdaptiveBandMR) openLong(price, atr float64) {
	stopDist := atr * a.Cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty := a.calcQty(price) // uses risk.CalcQty internally
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  a.Symbol,
		Side:    types.Buy,
		Qty:     qty,
		Price:   price,
		Comment: "AdaptiveBandMR entry long",
	}
	_ = a.submitOrder(o, "adaptiveband_rev_long")
}

// openShort creates a short order sized by risk.
func (a *AdaptiveBandMR) openShort(price, atr float64) {
	stopDist := atr * a.Cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty := a.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  a.Symbol,
		Side:    types.Sell,
		Qty:     qty,
		Price:   price,
		Comment: "AdaptiveBandMR entry short",
	}
	_ = a.submitOrder(o, "adaptiveband_rev_short")
}

// manageTakeProfit implements the optional ATR‑multiple TP.
func (a *AdaptiveBandMR) manageTakeProfit(currentPrice, atr float64) {
	qty, avg := a.Exec.Position(a.Symbol)
	if qty == 0 {
		return
	}
	if qty > 0 { // long
		target := avg + atr*a.Cfg.TakeProfitPct
		if currentPrice >= target {
			a.closePosition(currentPrice, "adaptiveband_rev_tp")
		}
	} else { // short
		target := avg - atr*a.Cfg.TakeProfitPct
		if currentPrice <= target {
			a.closePosition(currentPrice, "adaptiveband_rev_tp")
		}
	}
}
