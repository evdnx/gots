package strategy

import (
	"math"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/types"
)

// MeanReversion implements the classic oversold/overbought mean‑reversion strategy.
type MeanReversion struct {
	*BaseStrategy
}

// NewMeanReversion builds the suite and injects a logger.
func NewMeanReversion(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger) (*MeanReversion, error) {

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = 70
		ic.RSIOversold = 30
		ic.MFIOverbought = 80
		ic.MFIOversold = 20
		ic.VWAOStrongTrend = 70
		return goti.NewIndicatorSuiteWithConfig(ic)
	}
	base, err := NewBaseStrategy(symbol, cfg, exec, suiteFactory, log)
	if err != nil {
		return nil, err
	}
	return &MeanReversion{BaseStrategy: base}, nil
}

// ProcessBar updates the suite and evaluates the three oscillator crossovers.
func (mr *MeanReversion) ProcessBar(high, low, close, volume float64) {
	if err := mr.Suite.Add(high, low, close, volume); err != nil {
		mr.Log.Warn("suite_add_error", logger.Err(err))
		return
	}
	mr.recordPrice(close)
	if !mr.hasHistory(15) {
		return
	}

	rsiBull := mr.bullishFallback()
	if ok, err := mr.Suite.GetRSI().IsBullishCrossover(); err == nil {
		rsiBull = rsiBull || ok
	}
	rsiBear := mr.bearishFallback()
	if ok, err := mr.Suite.GetRSI().IsBearishCrossover(); err == nil {
		rsiBear = rsiBear || ok
	}
	mfiBull := mr.bullishFallback()
	if ok, err := mr.Suite.GetMFI().IsBullishCrossover(); err == nil {
		mfiBull = mfiBull || ok
	}
	mfiBear := mr.bearishFallback()
	if ok, err := mr.Suite.GetMFI().IsBearishCrossover(); err == nil {
		mfiBear = mfiBear || ok
	}
	vwaoBull := mr.bullishFallback()
	if ok, err := mr.Suite.GetVWAO().IsBullishCrossover(); err == nil {
		vwaoBull = vwaoBull || ok
	}
	vwaoBear := mr.bearishFallback()
	if ok, err := mr.Suite.GetVWAO().IsBearishCrossover(); err == nil {
		vwaoBear = vwaoBear || ok
	}

	longSignal := rsiBull && mfiBull && vwaoBull
	shortSignal := rsiBear && mfiBear && vwaoBear

	posQty, _ := mr.Exec.Position(mr.Symbol)

	switch {
	case longSignal && posQty <= 0:
		if posQty < 0 {
			mr.closePosition(close, "mr_close_short")
		}
		mr.openLong(close)

	case shortSignal && posQty >= 0:
		if posQty > 0 {
			mr.closePosition(close, "mr_close_long")
		}
		mr.openShort(close)

	case posQty != 0 && mr.Cfg.TrailingPct > 0:
		mr.applyTrailingStop(close)
	case posQty != 0:
		if mr.Cfg.TakeProfitPct > 0 {
			mr.manageTakeProfit(close)
		}
	}
}

// openLong creates a long order sized by risk.
func (mr *MeanReversion) openLong(price float64) {
	qty := mr.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  mr.Symbol,
		Side:    types.Buy,
		Qty:     qty,
		Price:   price,
		Comment: "MeanReversion entry long",
	}
	_ = mr.submitOrder(o, "mr_long")
}

// openShort creates a short order sized by risk.
func (mr *MeanReversion) openShort(price float64) {
	qty := mr.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  mr.Symbol,
		Side:    types.Sell,
		Qty:     qty,
		Price:   price,
		Comment: "MeanReversion entry short",
	}
	_ = mr.submitOrder(o, "mr_short")
}

func (mr *MeanReversion) manageTakeProfit(currentPrice float64) {
	qty, avg := mr.Exec.Position(mr.Symbol)
	if qty == 0 {
		return
	}
	atrVals := mr.Suite.GetATSO().GetATSOValues()
	atr := 0.0
	if len(atrVals) > 0 {
		atr = math.Abs(atrVals[len(atrVals)-1])
	}
	atr = mr.sanitizeVolatility(atr, avg)
	if qty > 0 {
		target := avg + atr*mr.Cfg.TakeProfitPct
		if currentPrice >= target {
			mr.closePosition(currentPrice, "mr_tp")
		}
	} else {
		target := avg - atr*mr.Cfg.TakeProfitPct
		if currentPrice <= target {
			mr.closePosition(currentPrice, "mr_tp")
		}
	}
}
