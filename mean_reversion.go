// Mean‑reversion / oversold‑overbought strategy built on the GoTI
// indicator suite.
package gots

import (
	"log"
	"math"

	"github.com/evdnx/goti"
)

// ---------------------------------------------------------------------
// MeanReversion – public type
// ---------------------------------------------------------------------
//
// Fields:
//
//	suite   – a fully‑configured IndicatorSuite (RSI, MFI, VWAO, …)
//	cfg     – risk & indicator thresholds (see StrategyConfig)
//	exec    – order routing / paper‑trading implementation
//	symbol  – ticker we trade (e.g. "BTCUSDT")
type MeanReversion struct {
	suite  *goti.IndicatorSuite
	cfg    StrategyConfig
	exec   Executor
	symbol string
}

// ---------------------------------------------------------------------
// NewMeanReversion – constructor
// ---------------------------------------------------------------------
//
// Builds a fresh IndicatorSuite using the thresholds supplied in cfg.
// Returns an error if the underlying suite cannot be created.
func NewMeanReversion(symbol string, cfg StrategyConfig, exec Executor) (*MeanReversion, error) {
	indCfg := goti.DefaultConfig()
	indCfg.RSIOverbought = cfg.RSIOverbought
	indCfg.RSIOversold = cfg.RSIOversold
	indCfg.MFIOverbought = cfg.MFIOverbought
	indCfg.MFIOversold = cfg.MFIOversold
	indCfg.VWAOStrongTrend = cfg.VWAOStrongTrend

	suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &MeanReversion{
		suite:  suite,
		cfg:    cfg,
		exec:   exec,
		symbol: symbol,
	}, nil
}

// ---------------------------------------------------------------------
// ProcessBar – entry point for each new OHLCV candle
// ---------------------------------------------------------------------
//
// 1️⃣ Feed the suite with the fresh data.
// 2️⃣ Pull the three oscillator signals (RSI, MFI, VWAO).
// 3️⃣ If **all three** generate a bullish (or bearish) crossover on the
//
//	same bar, open a position sized by the risk calculator.
//
// 4️⃣ If a position already exists, apply a trailing‑stop (if enabled)
//
//	and/or close it when the opposite signal appears.
func (mr *MeanReversion) ProcessBar(high, low, close, volume float64) {
	// -----------------------------------------------------------------
	// 1️⃣  Update the shared IndicatorSuite
	// -----------------------------------------------------------------
	if err := mr.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] mean‑reversion suite.Add error: %v", err)
		return
	}

	// -----------------------------------------------------------------
	// 2️⃣  Gather the three crossover flags
	// -----------------------------------------------------------------
	rsiBull, _ := mr.suite.GetRSI().IsBullishCrossover()
	rsiBear, _ := mr.suite.GetRSI().IsBearishCrossover()

	mfiBull, _ := mr.suite.GetMFI().IsBullishCrossover()
	mfiBear, _ := mr.suite.GetMFI().IsBearishCrossover()

	vwaoBull, _ := mr.suite.GetVWAO().IsBullishCrossover()
	vwaoBear, _ := mr.suite.GetVWAO().IsBearishCrossover()

	// -----------------------------------------------------------------
	// 3️⃣  Determine the composite signal
	// -----------------------------------------------------------------
	longSignal := rsiBull && mfiBull && vwaoBull
	shortSignal := rsiBear && mfiBear && vwaoBear

	// -----------------------------------------------------------------
	// 4️⃣  Position management
	// -----------------------------------------------------------------
	posQty, _ := mr.exec.Position(mr.symbol)

	switch {
	case longSignal && posQty <= 0:
		// Close any short side first, then go long
		if posQty < 0 {
			mr.closePosition(close)
		}
		mr.openPosition(Buy, close)

	case shortSignal && posQty >= 0:
		// Close any long side first, then go short
		if posQty > 0 {
			mr.closePosition(close)
		}
		mr.openPosition(Sell, close)

	// If we already have a position but no new signal, just run the
	// trailing‑stop logic (if the user enabled it).
	case posQty != 0:
		mr.applyTrailingStop(close)
	}
}

// ---------------------------------------------------------------------
// openPosition – creates a market order sized by risk parameters.
// ---------------------------------------------------------------------
func (mr *MeanReversion) openPosition(side Side, price float64) {
	qty := CalcQty(mr.exec.Equity(),
		mr.cfg.MaxRiskPerTrade,
		mr.cfg.StopLossPct,
		price)

	if qty <= 0 {
		return
	}
	o := Order{
		Symbol:  mr.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price, // market price – set to 0 if you want a true market order
		Comment: "MeanReversion entry",
	}
	if err := mr.exec.Submit(o); err != nil {
		log.Printf("[ERR] mean‑reversion submit entry: %v", err)
	}
}

// ---------------------------------------------------------------------
// closePosition – exits the current position at market price.
// ---------------------------------------------------------------------
func (mr *MeanReversion) closePosition(price float64) {
	qty, _ := mr.exec.Position(mr.symbol)
	if qty == 0 {
		return
	}
	// Reverse the side to flatten the position
	side := Sell
	if qty < 0 {
		side = Buy
	}
	o := Order{
		Symbol:  mr.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "MeanReversion exit",
	}
	if err := mr.exec.Submit(o); err != nil {
		log.Printf("[ERR] mean‑reversion submit exit: %v", err)
	}
}

// ---------------------------------------------------------------------
// applyTrailingStop – moves the stop‑loss toward market price.
// ---------------------------------------------------------------------
func (mr *MeanReversion) applyTrailingStop(currentPrice float64) {
	if mr.cfg.TrailingPct <= 0 {
		return
	}
	qty, avg := mr.exec.Position(mr.symbol)
	if qty == 0 {
		return
	}
	var trailLevel float64
	if qty > 0 { // long
		// For longs we tighten the stop upward as price rises.
		trailLevel = avg * (1 + mr.cfg.TrailingPct)
		if currentPrice >= trailLevel {
			mr.closePosition(currentPrice)
		}
	} else { // short
		// For shorts we tighten the stop downward as price falls.
		trailLevel = avg * (1 - mr.cfg.TrailingPct)
		if currentPrice <= trailLevel {
			mr.closePosition(currentPrice)
		}
	}
}
