// Breakout / Momentum‑Burst strategy built on the GoTI indicator suite.
// It combines:
//
//   - Hull Moving Average (HMA) – fast trend detector.
//   - Volume‑Weighted Aroon Oscillator (VWAO) – tells us whether the breakout
//     happened on heavy volume (more reliable).
//   - Adaptive Trend Strength Oscillator (ATSO) – adapts its look‑back to recent
//     volatility, so we capture sudden volatility spikes that usually accompany
//     breakouts.
//
// The rule set is:
//
//   - **Long entry**  – price makes a new *high* (HMA bullish crossover) AND
//     VWAO bullish crossover (price crossing the strong‑trend
//     line on high volume) AND
//     ATSO turns positive (raw sign change) on the same bar.
//   - **Short entry** – symmetric opposite conditions.
//   - **Exit**        – opposite breakout signal, stop‑loss, take‑profit or
//     optional trailing‑stop.
//   - Position sizing is driven by the generic risk calculator (max % of equity
//     per trade, stop‑loss % etc.).
package gots

import (
	"log"
	"math"

	"github.com/evdnx/goti"
)

// ---------------------------------------------------------------------
// BreakoutMomentum – public type
// ---------------------------------------------------------------------
//
// Fields:
//
//	suite   – IndicatorSuite containing HMA, VWAO, ATSO (plus the other
//	         indicators that the suite always creates – they are simply unused).
//	cfg     – risk & threshold configuration.
//	exec    – order router / paper‑trader.
//	symbol  – ticker we trade (e.g. "ETHUSDT").
type BreakoutMomentum struct {
	suite  *goti.IndicatorSuite
	cfg    StrategyConfig
	exec   Executor
	symbol string
}

// ---------------------------------------------------------------------
// NewBreakoutMomentum – constructor
// ---------------------------------------------------------------------
//
// Builds a fresh IndicatorSuite with the thresholds supplied in cfg.
// Only the three indicators needed for the breakout logic are consulted,
// but we still instantiate the full suite because the constructor lives in
// the GoTI package and returns a ready‑to‑use object.
func NewBreakoutMomentum(symbol string, cfg StrategyConfig, exec Executor) (*BreakoutMomentum, error) {
	indCfg := goti.DefaultConfig()
	indCfg.ATSEMAperiod = cfg.ATSEMAperiod // propagate user‑chosen EMA period

	suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &BreakoutMomentum{
		suite:  suite,
		cfg:    cfg,
		exec:   exec,
		symbol: symbol,
	}, nil
}

// ---------------------------------------------------------------------
// ProcessBar – called for every new OHLCV candle
// ---------------------------------------------------------------------
//
// 1️⃣ Feed the suite with the fresh data.
// 2️⃣ Pull the three breakout‑related signals.
// 3️⃣ If **all three** agree on a bullish (or bearish) breakout, open a
//
//	position sized by the risk calculator.
//
// 4️⃣ If a position already exists, close it when the opposite breakout
//
//	appears or when stop‑loss / trailing‑stop fires.
func (bm *BreakoutMomentum) ProcessBar(high, low, close, volume float64) {
	// -----------------------------------------------------------------
	// 1️⃣  Update the shared IndicatorSuite
	// -----------------------------------------------------------------
	if err := bm.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] breakout suite.Add error: %v", err)
		return
	}

	// -----------------------------------------------------------------
	// 2️⃣  Gather the three breakout signals
	// -----------------------------------------------------------------
	hBull, _ := bm.suite.GetHMA().IsBullishCrossover()
	hBear, _ := bm.suite.GetHMA().IsBearishCrossover()

	vBull, _ := bm.suite.GetVWAO().IsBullishCrossover()
	vBear, _ := bm.suite.GetVWAO().IsBearishCrossover()

	// ATSO does not expose a crossover method that returns an error – it
	// simply scans the raw series for a sign change.
	atBull := bm.suite.GetATSO().IsBullishCrossover()
	atBear := bm.suite.GetATSO().IsBearishCrossover()

	// -----------------------------------------------------------------
	// 3️⃣  Composite breakout condition
	// -----------------------------------------------------------------
	longSignal := hBull && vBull && atBull
	shortSignal := hBear && vBear && atBear

	// -----------------------------------------------------------------
	// 4️⃣  Position management
	// -----------------------------------------------------------------
	posQty, _ := bm.exec.Position(bm.symbol)

	switch {
	case longSignal && posQty <= 0:
		// Close any short side first, then go long.
		if posQty < 0 {
			bm.closePosition(close)
		}
		bm.openPosition(Buy, close)

	case shortSignal && posQty >= 0:
		// Close any long side first, then go short.
		if posQty > 0 {
			bm.closePosition(close)
		}
		bm.openPosition(Sell, close)

	// No new breakout signal – keep the existing position alive but enforce
	// trailing‑stop (if enabled) and monitor for an opposite breakout.
	case posQty != 0:
		bm.applyTrailingStop(close)

		// Optional: early exit if the opposite breakout appears while we
		// already have a position (helps avoid fighting the market).
		if posQty > 0 && shortSignal {
			bm.closePosition(close)
		}
		if posQty < 0 && longSignal {
			bm.closePosition(close)
		}
	}
}

// ---------------------------------------------------------------------
// openPosition – creates a market order sized by risk parameters.
// ---------------------------------------------------------------------
func (bm *BreakoutMomentum) openPosition(side Side, price float64) {
	qty := CalcQty(
		bm.exec.Equity(),
		bm.cfg.MaxRiskPerTrade,
		bm.cfg.StopLossPct,
		price,
	)
	if qty <= 0 {
		return
	}
	o := Order{
		Symbol:  bm.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price, // market price – set to 0 for a true market order
		Comment: "BreakoutMomentum entry",
	}
	if err := bm.exec.Submit(o); err != nil {
		log.Printf("[ERR] breakout submit entry: %v", err)
	}
}

// ---------------------------------------------------------------------
// closePosition – exits the current position at market price.
// ---------------------------------------------------------------------
func (bm *BreakoutMomentum) closePosition(price float64) {
	qty, _ := bm.exec.Position(bm.symbol)
	if qty == 0 {
		return
	}
	// Reverse side to flatten the position.
	side := Sell
	if qty < 0 {
		side = Buy
	}
	o := Order{
		Symbol:  bm.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "BreakoutMomentum exit",
	}
	if err := bm.exec.Submit(o); err != nil {
		log.Printf("[ERR] breakout submit exit: %v", err)
	}
}

// ---------------------------------------------------------------------
// applyTrailingStop – moves the stop‑loss toward market price.
// ---------------------------------------------------------------------
func (bm *BreakoutMomentum) applyTrailingStop(currentPrice float64) {
	if bm.cfg.TrailingPct <= 0 {
		return
	}
	qty, avg := bm.exec.Position(bm.symbol)
	if qty == 0 {
		return
	}
	var trailLevel float64
	if qty > 0 { // long
		trailLevel = avg * (1 + bm.cfg.TrailingPct)
		if currentPrice >= trailLevel {
			bm.closePosition(currentPrice)
		}
	} else { // short
		trailLevel = avg * (1 - bm.cfg.TrailingPct)
		if currentPrice <= trailLevel {
			bm.closePosition(currentPrice)
		}
	}
}
