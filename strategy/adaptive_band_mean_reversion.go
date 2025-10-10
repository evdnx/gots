// Mean‑reversion strategy that uses an **ATR‑based adaptive band** together
// with RSI/MFI to decide entry points.  The band width expands when
// volatility (ATR) rises, shrinking when the market calms, which makes the
// stop‑loss distance naturally adapt to current market conditions.
//
// Entry logic (per candle):
//   - Price touches the **lower band** AND RSI is oversold → go long.
//   - Price touches the **upper band** AND RSI is overbought → go short.
//   - MFI is used as a secondary confirmation (must be on the same side of
//     its own overbought/oversold thresholds).
//   - A simple HMA trend filter is applied so we only take mean‑reversion
//     trades when the overall trend is flat or mildly opposite.
//
// Exit logic:
//   - Fixed take‑profit (configurable) or stop‑loss (ATR‑based).
//   - Optional trailing‑stop (percentage of entry price).
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

type AdaptiveBandMR struct {
	suite  *goti.IndicatorSuite
	cfg    config.StrategyConfig
	exec   executor.Executor
	symbol string
}

// NewAdaptiveBandMR constructs a suite with the default config, but we
// propagate the user‑provided thresholds (RSI/MFI overbought‑oversold).
func NewAdaptiveBandMR(symbol string, cfg config.StrategyConfig, exec executor.Executor) (*AdaptiveBandMR, error) {
	indCfg := goti.DefaultConfig()
	indCfg.RSIOverbought = cfg.RSIOverbought
	indCfg.RSIOversold = cfg.RSIOversold
	indCfg.MFIOverbought = cfg.MFIOverbought
	indCfg.MFIOversold = cfg.MFIOversold
	suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &AdaptiveBandMR{
		suite:  suite,
		cfg:    cfg,
		exec:   exec,
		symbol: symbol,
	}, nil
}

// ProcessBar updates the suite and decides whether to open/close a trade.
func (a *AdaptiveBandMR) ProcessBar(high, low, close, volume float64) {
	if err := a.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] adaptive‑band MR add error: %v", err)
		return
	}

	// -----------------------------------------------------------------
	// 1️⃣  Pull the latest indicator values we need.
	// -----------------------------------------------------------------
	atrVals := a.suite.GetATSO().GetATSOValues() // we use ATSO as a proxy for volatility
	if len(atrVals) == 0 {
		// Not enough data yet.
		return
	}
	atr := atrVals[len(atrVals)-1]

	rsiVal, _ := a.suite.GetRSI().Calculate()
	mfiVal, _ := a.suite.GetMFI().Calculate()
	hmaBull, _ := a.suite.GetHMA().IsBullishCrossover()
	hmaBear, _ := a.suite.GetHMA().IsBearishCrossover()

	// -----------------------------------------------------------------
	// 2️⃣  Build the adaptive band.
	// -----------------------------------------------------------------
	// Band multiplier is configurable via the strategy config – we reuse
	// StopLossPct as a convenient “band width” factor (e.g. 1.5 %).
	bandWidth := close * a.cfg.StopLossPct
	upperBand := close + bandWidth + atr
	lowerBand := close - bandWidth - atr

	// -----------------------------------------------------------------
	// 3️⃣  Determine entry conditions.
	// -----------------------------------------------------------------
	longCond := low <= lowerBand && rsiVal <= a.cfg.RSIOversold && mfiVal <= a.cfg.MFIOversold && !hmaBull
	shortCond := high >= upperBand && rsiVal >= a.cfg.RSIOverbought && mfiVal >= a.cfg.MFIOverbought && !hmaBear

	posQty, _ := a.exec.Position(a.symbol)

	switch {
	case longCond && posQty <= 0:
		if posQty < 0 {
			a.closePosition(close)
		}
		a.openPosition(types.Buy, close, atr)

	case shortCond && posQty >= 0:
		if posQty > 0 {
			a.closePosition(close)
		}
		a.openPosition(types.Sell, close, atr)

	case posQty != 0:
		// Manage existing position – trailing stop & optional profit target.
		a.manageOpenPosition(close, atr)
	}
}

// openPosition creates a market order sized by risk.  The stop‑loss distance
// is derived from the current ATR (so it expands/contracts with volatility).
func (a *AdaptiveBandMR) openPosition(side types.Side, price, atr float64) {
	// Risk per trade is a % of equity; the stop distance is ATR * StopLossPct.
	stopDist := atr * a.cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty := risk.CalcQty(a.exec.Equity(), a.cfg.MaxRiskPerTrade, stopDist/price, price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  a.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "AdaptiveBandMR entry",
	}
	if err := a.exec.Submit(o); err != nil {
		log.Printf("[ERR] adaptive‑band submit entry: %v", err)
	}
}

// closePosition flattens the current position at market price.
func (a *AdaptiveBandMR) closePosition(price float64) {
	qty, _ := a.exec.Position(a.symbol)
	if qty == 0 {
		return
	}
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  a.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "AdaptiveBandMR exit",
	}
	if err := a.exec.Submit(o); err != nil {
		log.Printf("[ERR] adaptive‑band submit exit: %v", err)
	}
}

// manageOpenPosition applies trailing‑stop and optional fixed take‑profit.
func (a *AdaptiveBandMR) manageOpenPosition(currentPrice, atr float64) {
	if a.cfg.TrailingPct > 0 {
		a.applyTrailingStop(currentPrice)
	}
	// Optional static take‑profit based on a multiple of ATR.
	if a.cfg.TakeProfitPct > 0 {
		qty, avg := a.exec.Position(a.symbol)
		if qty == 0 {
			return
		}
		target := avg
		if qty > 0 { // long
			target = avg + atr*a.cfg.TakeProfitPct
			if currentPrice >= target {
				a.closePosition(currentPrice)
			}
		} else { // short
			target = avg - atr*a.cfg.TakeProfitPct
			if currentPrice <= target {
				a.closePosition(currentPrice)
			}
		}
	}
}

// applyTrailingStop moves the stop‑loss toward market price.
func (a *AdaptiveBandMR) applyTrailingStop(currentPrice float64) {
	if a.cfg.TrailingPct <= 0 {
		return
	}
	qty, avg := a.exec.Position(a.symbol)
	if qty == 0 {
		return
	}
	var trailLevel float64
	if qty > 0 { // long
		trailLevel = avg * (1 + a.cfg.TrailingPct)
		if currentPrice >= trailLevel {
			a.closePosition(currentPrice)
		}
	} else { // short
		trailLevel = avg * (1 - a.cfg.TrailingPct)
		if currentPrice <= trailLevel {
			a.closePosition(currentPrice)
		}
	}
}
