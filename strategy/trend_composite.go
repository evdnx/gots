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
	t.recordPrice(close)
	if !t.hasHistory(15) {
		return
	}

	// Pull the three core signals.
	hBull := t.bullishFallback()
	if ok, err := t.Suite.GetHMA().IsBullishCrossover(); err == nil {
		hBull = hBull || ok
	}
	hBear := t.bearishFallback()
	if ok, err := t.Suite.GetHMA().IsBearishCrossover(); err == nil {
		hBear = hBear || ok
	}
	aBull := t.bullishFallback()
	if ok, err := t.Suite.GetAMDO().IsBullishCrossover(); err == nil {
		aBull = aBull || ok
	}
	aBear := t.bearishFallback()
	if ok, err := t.Suite.GetAMDO().IsBearishCrossover(); err == nil {
		aBear = aBear || ok
	}
	atBull := t.bullishFallback() || t.Suite.GetATSO().IsBullishCrossover()
	atBear := t.bearishFallback() || t.Suite.GetATSO().IsBearishCrossover()

	// Raw indicator values for momentum direction.
	admoVal, err := t.Suite.GetAMDO().Calculate()
	if err != nil {
		admoVal = t.prices.Slope()
	}
	atsoVal, err := t.Suite.GetATSO().Calculate()
	if err != nil {
		atsoVal = t.prices.Slope()
	} else {
		atsoVal = t.sanitizeVolatility(math.Abs(atsoVal), close) * math.Copysign(1, atsoVal)
	}

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
		if t.Cfg.TakeProfitPct > 0 {
			t.manageTakeProfit(close)
		}
	case posQty != 0:
		if t.Cfg.TakeProfitPct > 0 {
			t.manageTakeProfit(close)
		}
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

func (t *TrendComposite) manageTakeProfit(currentPrice float64) {
	qty, avg := t.Exec.Position(t.Symbol)
	if qty == 0 {
		return
	}
	atrVals := t.Suite.GetATSO().GetATSOValues()
	atr := 0.0
	if len(atrVals) > 0 {
		atr = math.Abs(atrVals[len(atrVals)-1])
	}
	atr = t.sanitizeVolatility(atr, avg)
	if qty > 0 {
		target := avg + atr*t.Cfg.TakeProfitPct
		if currentPrice >= target {
			t.closePosition(currentPrice, "trendcomp_tp")
		}
	} else {
		target := avg - atr*t.Cfg.TakeProfitPct
		if currentPrice <= target {
			t.closePosition(currentPrice, "trendcomp_tp")
		}
	}
}
