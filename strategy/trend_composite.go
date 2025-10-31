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

// TrendComposite combines HMA, ADMO, and ATSO crossovers with raw‑value filters.
type TrendComposite struct {
	*BaseStrategy
	lastDir int // -1 = short, 0 = flat, +1 = long
}

// NewTrendComposite builds the suite and injects a logger.
func NewTrendComposite(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger) (*TrendComposite, error) {

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = 70
		ic.RSIOversold = 30
		ic.MFIOverbought = 80
		ic.MFIOversold = 20
		ic.VWAOStrongTrend = 70
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}
	base, err := NewBaseStrategy(symbol, cfg, exec, suiteFactory, log)
	if err != nil {
		return nil, err
	}
	return &TrendComposite{
		BaseStrategy: base,
		lastDir:      0,
	}, nil
}

// ProcessBar evaluates the composite signal and manages the position.
func (t *TrendComposite) ProcessBar(high, low, close, volume float64) {
	if err := t.Suite.Add(high, low, close, volume); err != nil {
		t.Log.Warn("suite_add_error", zap.Error(err))
		return
	}
	// Warm‑up: ensure we have enough data for the indicators.
	if len(t.Suite.GetHMA().GetCloses()) < 10 {
		return
	}

	// Pull the three core signals.
	hBull, _ := t.Suite.GetHMA().IsBullishCrossover()
	hBear, _ := t.Suite.GetHMA().IsBearishCrossover()
	aBull, _ := t.Suite.GetAMDO().IsBullishCrossover()
	aBear, _ := t.Suite.GetAMDO().IsBearishCrossover()
	atBull := t.Suite.GetATSO().IsBullishCrossover()
	atBear := t.Suite.GetATSO().IsBearishCrossover()

	// Raw indicator values for momentum direction.
	admoVal, _ := t.Suite.GetAMDO().Calculate()
	atsoVal, _ := t.Suite.GetATSO().Calculate()

	longCond := hBull && aBull && atBull && admoVal > 0 && atsoVal > 0
	shortCond := hBear && aBear && atBear && admoVal < 0 && atsoVal < 0

	posQty, _ := t.Exec.Position(t.Symbol)

	switch {
	case longCond && posQty <= 0:
		if posQty < 0 {
			t.closePosition(close, "trendcomp_close_short")
		}
		t.openLong(close)

	case shortCond && posQty >= 0:
		if posQty > 0 {
			t.closePosition(close, "trendcomp_close_long")
		}
		t.openShort(close)

	case posQty != 0 && t.Cfg.TrailingPct > 0:
		// Optional trailing‑stop logic.
		t.applyTrailingStop(close)
	}
}

// openLong creates a long order sized by the generic risk calculator.
func (t *TrendComposite) openLong(price float64) {
	qty := t.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  t.Symbol,
		Side:    types.Buy,
		Qty:     qty,
		Price:   price,
		Comment: "TrendComposite entry long",
	}
	_ = t.submitOrder(o, "trendcomp_long")
	t.lastDir = 1
}

// openShort creates a short order sized by the generic risk calculator.
func (t *TrendComposite) openShort(price float64) {
	qty := t.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  t.Symbol,
		Side:    types.Sell,
		Qty:     qty,
		Price:   price,
		Comment: "TrendComposite entry short",
	}
	_ = t.submitOrder(o, "trendcomp_short")
	t.lastDir = -1
}

// closePosition flattens the current position at market price.
func (t *TrendComposite) closePosition(price float64, ctx string) {
	qty, _ := t.Exec.Position(t.Symbol)
	if qty == 0 {
		return
	}
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  t.Symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "TrendComposite exit",
	}
	_ = t.submitOrder(o, ctx)
	t.lastDir = 0
}
