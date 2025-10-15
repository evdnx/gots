package strategy

import (
	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/types"
	"go.uber.org/zap"
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
		ic.RSIOverbought = cfg.RSIOverbought
		ic.RSIOversold = cfg.RSIOversold
		ic.MFIOverbought = cfg.MFIOverbought
		ic.MFIOversold = cfg.MFIOversold
		ic.VWAOStrongTrend = cfg.VWAOStrongTrend
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
		mr.Log.Warn("suite_add_error", zap.Error(err))
		return
	}
	if len(mr.Suite.GetRSI().GetCloses()) < 14 {
		return // warm‑up
	}

	// Pull crossovers.
	rsiBull, _ := mr.Suite.GetRSI().IsBullishCrossover()
	rsiBear, _ := mr.Suite.GetRSI().IsBearishCrossover()
	mfiBull, _ := mr.Suite.GetMFI().IsBullishCrossover()
	mfiBear, _ := mr.Suite.GetMFI().IsBearishCrossover()
	vwaoBull, _ := mr.Suite.GetVWAO().IsBullishCrossover()
	vwaoBear, _ := mr.Suite.GetVWAO().IsBearishCrossover()

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
