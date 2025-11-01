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

// BreakoutMomentum implements the breakout / momentum‑burst strategy.
type BreakoutMomentum struct {
	*BaseStrategy
}

// NewBreakoutMomentum builds the suite and injects a logger.
func NewBreakoutMomentum(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger) (*BreakoutMomentum, error) {

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}
	base, err := NewBaseStrategy(symbol, cfg, exec, suiteFactory, log)
	if err != nil {
		return nil, err
	}
	return &BreakoutMomentum{BaseStrategy: base}, nil
}

// ProcessBar updates the suite, evaluates breakout signals and manages positions.
func (bm *BreakoutMomentum) ProcessBar(high, low, close, volume float64) {
	if err := bm.Suite.Add(high, low, close, volume); err != nil {
		bm.Log.Warn("suite_add_error", zap.Error(err))
		return
	}
	bm.recordPrice(close)
	if !bm.hasHistory(15) {
		return
	}

	// 1️⃣ Gather signals.
	hBull := bm.bullishFallback()
	if ok, err := bm.Suite.GetHMA().IsBullishCrossover(); err == nil {
		hBull = hBull || ok
	}
	hBear := bm.bearishFallback()
	if ok, err := bm.Suite.GetHMA().IsBearishCrossover(); err == nil {
		hBear = hBear || ok
	}
	vBull := bm.bullishFallback()
	if ok, err := bm.Suite.GetVWAO().IsBullishCrossover(); err == nil {
		vBull = vBull || ok
	}
	vBear := bm.bearishFallback()
	if ok, err := bm.Suite.GetVWAO().IsBearishCrossover(); err == nil {
		vBear = vBear || ok
	}
	atBull := bm.bullishFallback() || bm.Suite.GetATSO().IsBullishCrossover()
	atBear := bm.bearishFallback() || bm.Suite.GetATSO().IsBearishCrossover()

	longSignal := hBull && vBull && atBull
	shortSignal := hBear && vBear && atBear

	posQty, _ := bm.Exec.Position(bm.Symbol)

	switch {
	case longSignal && posQty <= 0:
		if posQty < 0 {
			bm.closePosition(close, "breakout_mom_close_short")
		}
		bm.openLong(close)

	case shortSignal && posQty >= 0:
		if posQty > 0 {
			bm.closePosition(close, "breakout_mom_close_long")
		}
		bm.openShort(close)

	case posQty != 0:
		// Trailing stop & optional TP.
		if bm.Cfg.TrailingPct > 0 {
			bm.applyTrailingStop(close)
		}
		if bm.Cfg.TakeProfitPct > 0 {
			bm.manageTakeProfit(close)
		}
	}
}

// openLong creates a long order sized by risk.
func (bm *BreakoutMomentum) openLong(price float64) {
	qty := bm.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  bm.Symbol,
		Side:    types.Buy,
		Qty:     qty,
		Price:   price,
		Comment: "BreakoutMomentum entry long",
	}
	_ = bm.submitOrder(o, "breakout_mom_long")
}

// openShort creates a short order sized by risk.
func (bm *BreakoutMomentum) openShort(price float64) {
	qty := bm.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  bm.Symbol,
		Side:    types.Sell,
		Qty:     qty,
		Price:   price,
		Comment: "BreakoutMomentum entry short",
	}
	_ = bm.submitOrder(o, "breakout_mom_short")
}

// manageTakeProfit uses ATR‑multiple TP (same logic as in AdaptiveBandMR).
func (bm *BreakoutMomentum) manageTakeProfit(currentPrice float64) {
	qty, avg := bm.Exec.Position(bm.Symbol)
	if qty == 0 {
		return
	}
	atrVals := bm.Suite.GetATSO().GetATSOValues()
	if len(atrVals) == 0 {
		return
	}
	atr := bm.sanitizeVolatility(math.Abs(atrVals[len(atrVals)-1]), avg)

	if qty > 0 {
		target := avg + atr*bm.Cfg.TakeProfitPct
		if currentPrice >= target {
			bm.closePosition(currentPrice, "breakout_mom_tp")
		}
	} else {
		target := avg - atr*bm.Cfg.TakeProfitPct
		if currentPrice <= target {
			bm.closePosition(currentPrice, "breakout_mom_tp")
		}
	}
}
