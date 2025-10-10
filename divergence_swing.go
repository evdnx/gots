// Looks for classic bullish/bearish divergence between price and an
// oscillator (RSI, MFI, or ADMO).  When any oscillator signals divergence
// **and** the HMA indicates the overall trend direction, a trade is taken.
package gots

import (
	"log"
	"math"

	"github.com/evdnx/goti"
)

type DivergenceSwing struct {
	suite  *goti.IndicatorSuite
	cfg    StrategyConfig
	exec   Executor
	symbol string
}

// NewDivergenceSwing builds a suite with the default config (thresholds
// are taken from cfg for flexibility).
func NewDivergenceSwing(symbol string, cfg StrategyConfig, exec Executor) (*DivergenceSwing, error) {
	indCfg := goti.DefaultConfig()
	indCfg.RSIOverbought = cfg.RSIOverbought
	indCfg.RSIOversold = cfg.RSIOversold
	indCfg.MFIOverbought = cfg.MFIOverbought
	indCfg.MFIOversold = cfg.MFIOversold
	suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &DivergenceSwing{
		suite:  suite,
		cfg:    cfg,
		exec:   exec,
		symbol: symbol,
	}, nil
}

// ProcessBar updates the suite and checks for divergence.
func (d *DivergenceSwing) ProcessBar(high, low, close, volume float64) {
	if err := d.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] divergence add error: %v", err)
		return
	}

	// ---- Trend filter (HMA) ----
	hBull, _ := d.suite.GetHMA().IsBullishCrossover()
	hBear, _ := d.suite.GetHMA().IsBearishCrossover()

	// ---- Divergence checks (any one oscillator may fire) ----
	bullDiv := false
	bearDiv := false

	if ok, typ, _ := d.suite.GetRSI().IsDivergence(); ok && typ == "Bullish" {
		bullDiv = true
	}
	if ok, typ, _ := d.suite.GetRSI().IsDivergence(); ok && typ == "Bearish" {
		bearDiv = true
	}
	if ok, _ := d.suite.GetMFI().IsDivergence(); ok == "Bullish" {
		bullDiv = true
	}
	if ok, _ := d.suite.GetMFI().IsDivergence(); ok == "Bearish" {
		bearDiv = true
	}
	if ok, typ := d.suite.GetAMDO().IsDivergence(); ok && typ == "Bullish" {
		bullDiv = true
	}
	if ok, typ := d.suite.GetAMDO().IsDivergence(); ok && typ == "Bearish" {
		bearDiv = true
	}

	longCond := bullDiv && hBull
	shortCond := bearDiv && hBear

	posQty, _ := d.exec.Position(d.symbol)

	switch {
	case longCond && posQty <= 0:
		if posQty < 0 {
			d.closePosition(close)
		}
		d.openPosition(Buy, close)

	case shortCond && posQty >= 0:
		if posQty > 0 {
			d.closePosition(close)
		}
		d.openPosition(Sell, close)

	case posQty != 0:
		d.applyTrailingStop(close)
	}
}

// openPosition / closePosition / applyTrailingStop are identical to the
// implementations used in the other strategy files (re‑used for brevity).
func (d *DivergenceSwing) openPosition(side Side, price float64) {
	qty := CalcQty(d.exec.Equity(), d.cfg.MaxRiskPerTrade, d.cfg.StopLossPct, price)
	if qty <= 0 {
		return
	}
	o := Order{
		Symbol:  d.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "DivergenceSwing entry",
	}
	if err := d.exec.Submit(o); err != nil {
		log.Printf("[ERR] divergence submit entry: %v", err)
	}
}
func (d *DivergenceSwing) closePosition(price float64) {
	qty, _ := d.exec.Position(d.symbol)
	if qty == 0 {
		return
	}
	side := Sell
	if qty < 0 {
		side = Buy
	}
	o := Order{
		Symbol:  d.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "DivergenceSwing exit",
	}
	if err := d.exec.Submit(o); err != nil {
		log.Printf("[ERR] divergence submit exit: %v", err)
	}
}
func (d *DivergenceSwing) applyTrailingStop(currentPrice float64) {
	if d.cfg.TrailingPct <= 0 {
		return
	}
	qty, avg := d.exec.Position(d.symbol)
	if qty == 0 {
		return
	}
	var trailLevel float64
	if qty > 0 {
		trailLevel = avg * (1 + d.cfg.TrailingPct)
		if currentPrice >= trailLevel {
			d.closePosition(currentPrice)
		}
	} else {
		trailLevel = avg * (1 - d.cfg.TrailingPct)
		if currentPrice <= trailLevel {
			d.closePosition(currentPrice)
		}
	}
}
