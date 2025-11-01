package strategy

import (
	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/types"
	"go.uber.org/zap"
)

// DivergenceSwing looks for bullish/bearish divergence combined with HMA trend.
type DivergenceSwing struct {
	*BaseStrategy
}

// NewDivergenceSwing builds the suite with the supplied config.
func NewDivergenceSwing(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger) (*DivergenceSwing, error) {

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
	return &DivergenceSwing{BaseStrategy: base}, nil
}

// ProcessBar updates the suite and checks for divergence signals.
func (d *DivergenceSwing) ProcessBar(high, low, close, volume float64) {
	if err := d.Suite.Add(high, low, close, volume); err != nil {
		d.Log.Warn("suite_add_error", zap.Error(err))
		return
	}
	d.recordPrice(close)
	if !d.hasHistory(12) {
		return
	}
	hBull := d.bullishFallback()
	if ok, err := d.Suite.GetHMA().IsBullishCrossover(); err == nil {
		hBull = hBull || ok
	}
	hBear := d.bearishFallback()
	if ok, err := d.Suite.GetHMA().IsBearishCrossover(); err == nil {
		hBear = hBear || ok
	}

	// Divergence checks (any oscillator may fire)
	bullDiv, bearDiv := false, false

	if ok, typ, err := d.Suite.GetRSI().IsDivergence(); err == nil && ok {
		if typ == "Bullish" {
			bullDiv = true
		} else if typ == "Bearish" {
			bearDiv = true
		}
	}
	if dir, err := d.Suite.GetMFI().IsDivergence(); err == nil {
		switch dir {
		case "Bullish":
			bullDiv = true
		case "Bearish":
			bearDiv = true
		}
	}
	if ok, typ := d.Suite.GetAMDO().IsDivergence(); ok {
		if typ == "Bullish" {
			bullDiv = true
		} else if typ == "Bearish" {
			bearDiv = true
		}
	}
	if d.bullishReversal() {
		bullDiv = true
	}
	if d.bearishReversal() {
		bearDiv = true
	}

	longCond := bullDiv && hBull
	shortCond := bearDiv && hBear

	posQty, _ := d.Exec.Position(d.Symbol)

	switch {
	case longCond && posQty <= 0:
		if posQty < 0 {
			d.closePosition(close, "divergence_close_short")
		}
		d.openLong(close)

	case shortCond && posQty >= 0:
		if posQty > 0 {
			d.closePosition(close, "divergence_close_long")
		}
		d.openShort(close)

	case posQty != 0:
		if d.Cfg.TrailingPct > 0 {
			d.applyTrailingStop(close)
		}
	}
}

// openLong / openShort reuse the base helpers.
func (d *DivergenceSwing) openLong(price float64) {
	qty := d.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  d.Symbol,
		Side:    types.Buy,
		Qty:     qty,
		Price:   price,
		Comment: "DivergenceSwing entry long",
	}
	_ = d.submitOrder(o, "divergence_long")
}

func (d *DivergenceSwing) openShort(price float64) {
	qty := d.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  d.Symbol,
		Side:    types.Sell,
		Qty:     qty,
		Price:   price,
		Comment: "DivergenceSwing entry short",
	}
	_ = d.submitOrder(o, "divergence_short")
}
