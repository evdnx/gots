package strategy

import (
	"log"
	"math"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/risk"
	"github.com/evdnx/gots/types"
)

type TrendComposite struct {
	suite   *goti.IndicatorSuite
	cfg     config.StrategyConfig
	exec    executor.Executor
	symbol  string
	lastDir int // -1 = short, 0 = flat, +1 = long
}

func NewTrendComposite(symbol string, cfg config.StrategyConfig, exec executor.Executor) (*TrendComposite, error) {
	// Build a suite with the same config we will use for thresholds
	indCfg := goti.DefaultConfig()
	indCfg.RSIOverbought = cfg.RSIOverbought
	indCfg.RSIOversold = cfg.RSIOversold
	indCfg.MFIOverbought = cfg.MFIOverbought
	indCfg.MFIOversold = cfg.MFIOversold
	indCfg.VWAOStrongTrend = cfg.VWAOStrongTrend
	indCfg.ATSEMAperiod = cfg.ATSEMAperiod

	suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &TrendComposite{
		suite:   suite,
		cfg:     cfg,
		exec:    exec,
		symbol:  symbol,
		lastDir: 0,
	}, nil
}

// ProcessBar is called for every new OHLCV candle.
func (t *TrendComposite) ProcessBar(high, low, close, volume float64) {
	// 1️⃣ Feed the suite
	if err := t.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] suite add error: %v", err)
		return
	}

	// 2️⃣ Pull the three signals
	hBull, _ := t.suite.GetHMA().IsBullishCrossover()
	hBear, _ := t.suite.GetHMA().IsBearishCrossover()

	aBull, _ := t.suite.GetAMDO().IsBullishCrossover()
	aBear, _ := t.suite.GetAMDO().IsBearishCrossover()

	atBull := t.suite.GetATSO().IsBullishCrossover()
	atBear := t.suite.GetATSO().IsBearishCrossover()

	// 3️⃣ Filter by raw values (ensure momentum is positive)
	admoVal, _ := t.suite.GetAMDO().Calculate()
	atsoVal, _ := t.suite.GetATSO().Calculate()

	longCond := hBull && aBull && atBull && admoVal > 0 && atsoVal > 0
	shortCond := hBear && aBear && atBear && admoVal < 0 && atsoVal < 0

	// 4️⃣ Position management
	posQty, _ := t.exec.Position(t.symbol)

	switch {
	case longCond && posQty <= 0:
		// Close short (if any) then go long
		if posQty < 0 {
			t.closePosition(close)
		}
		t.openPosition(types.Buy, close)

	case shortCond && posQty >= 0:
		// Close long (if any) then go short
		if posQty > 0 {
			t.closePosition(close)
		}
		t.openPosition(types.Sell, close)

	// Optional trailing‑stop logic (run every bar)
	case posQty != 0:
		t.applyTrailingStop(close)
	}
}

// openPosition creates a market order sized by risk.
func (t *TrendComposite) openPosition(side types.Side, price float64) {
	qty := risk.CalcQty(t.exec.Equity(), t.cfg.MaxRiskPerTrade, t.cfg.StopLossPct, price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  t.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price, // market price – could be 0 for true market order
		Comment: "TrendComposite entry",
	}
	if err := t.exec.Submit(o); err != nil {
		log.Printf("[ERR] submit entry: %v", err)
	}
	t.lastDir = map[types.Side]int{types.Buy: 1, types.Sell: -1}[side]
}

// closePosition exits the current position at market price.
func (t *TrendComposite) closePosition(price float64) {
	qty, _ := t.exec.Position(t.symbol)
	if qty == 0 {
		return
	}
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  t.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "TrendComposite exit",
	}
	if err := t.exec.Submit(o); err != nil {
		log.Printf("[ERR] submit exit: %v", err)
	}
	t.lastDir = 0
}

// applyTrailingStop moves the stop‑loss toward market price.
func (t *TrendComposite) applyTrailingStop(currentPrice float64) {
	if t.cfg.TrailingPct <= 0 {
		return
	}
	qty, avg := t.exec.Position(t.symbol)
	if qty == 0 {
		return
	}
	var trailLevel float64
	if qty > 0 { // long
		trailLevel = avg * (1 + t.cfg.TrailingPct)
		if currentPrice >= trailLevel {
			// lock in profit – close the position
			t.closePosition(currentPrice)
		}
	} else { // short
		trailLevel = avg * (1 - t.cfg.TrailingPct)
		if currentPrice <= trailLevel {
			t.closePosition(currentPrice)
		}
	}
}
