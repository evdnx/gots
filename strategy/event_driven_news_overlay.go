// Event‑driven / news‑overlay strategy.
//
// The idea is simple:
//
//	1️⃣  An external signal (e.g. earnings release, macro data, protocol
//	    upgrade) toggles `eventActive` to true.
//	2️⃣  While the event flag is true we watch for a **volatility burst**
//	    detected by the Adaptive Trend Strength Oscillator (ATSO).  A burst
//	    is defined as |ATSO| > `eventThreshold`.
//	3️⃣  If a burst coincides with a **Hull Moving Average (HMA) breakout**
//	    (bullish or bearish crossover) we enter a trade in the direction of
//	    the ATSO sign.
//	4️⃣  The trade is held for at most `maxHoldingBars` bars or until a
//	    stop‑loss / take‑profit / trailing‑stop fires.
//	5️⃣  When the external event flag goes false we close any open position
//	    and stay idle until the next event.
//
// The strategy is completely stateless apart from the per‑symbol
// `IndicatorSuite` and a few counters, making it safe to run in a
// multi‑symbol environment.
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

// ---------------------------------------------------------------------
// EventDriven – public type
// ---------------------------------------------------------------------
type EventDriven struct {
	suite          *goti.IndicatorSuite  // holds all indicators for the symbol
	cfg            config.StrategyConfig // risk & threshold config
	exec           executor.Executor     // order router / paper‑trader
	symbol         string                // ticker (e.g. "BTCUSDT")
	eventActive    bool                  // set by external news feed
	eventThreshold float64               // minimum |ATSO| to consider the event “real”
	maxHoldingBars int                   // max bars we stay in the trade
	barSinceEntry  int                   // counts bars after entry
}

// ---------------------------------------------------------------------
// NewEventDriven – constructor
// ---------------------------------------------------------------------
//
// `eventThreshold` is expressed in ATSO‑raw units (typical values are
// 0.5‑2.0).  `maxHoldingBars` limits exposure after the event; a value
// of 10–20 works well on minute‑resolution data.
func NewEventDriven(symbol string, cfg config.StrategyConfig, exec executor.Executor,
	eventThreshold float64, maxHoldingBars int) (*EventDriven, error) {

	indCfg := goti.DefaultConfig()
	indCfg.RSIOverbought = cfg.RSIOverbought
	indCfg.RSIOversold = cfg.RSIOversold
	indCfg.MFIOverbought = cfg.MFIOverbought
	indCfg.MFIOversold = cfg.MFIOversold
	indCfg.ATSEMAperiod = cfg.ATSEMAperiod

	suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &EventDriven{
		suite:          suite,
		cfg:            cfg,
		exec:           exec,
		symbol:         symbol,
		eventActive:    false,
		eventThreshold: eventThreshold,
		maxHoldingBars: maxHoldingBars,
		barSinceEntry:  0,
	}, nil
}

// ---------------------------------------------------------------------
// SetEventActive – called by the surrounding application whenever a
// external event becomes active (true) or inactive (false).
// ---------------------------------------------------------------------
func (e *EventDriven) SetEventActive(active bool) {
	e.eventActive = active
	if !active {
		// If the event ends while we still have a position, close it.
		if qty, _ := e.exec.Position(e.symbol); qty != 0 {
			e.closePosition(e.lastClose())
		}
	}
}

// ---------------------------------------------------------------------
// ProcessBar – per‑candle entry point.
// ---------------------------------------------------------------------
func (e *EventDriven) ProcessBar(high, low, close, volume float64) {
	// -----------------------------------------------------------------
	// 0️⃣  Feed the suite first.
	// -----------------------------------------------------------------
	if err := e.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] event‑driven add error: %v", err)
		return
	}

	// -----------------------------------------------------------------
	// 1️⃣  If we already have a position, manage exits.
	// -----------------------------------------------------------------
	if qty, _ := e.exec.Position(e.symbol); qty != 0 {
		e.barSinceEntry++
		e.manageOpenPosition(close)

		// Enforce the hard time‑limit.
		if e.barSinceEntry >= e.maxHoldingBars {
			e.closePosition(close)
		}
		return
	}

	// -----------------------------------------------------------------
	// 2️⃣  No open position – we only act when the external event flag is true.
	// -----------------------------------------------------------------
	if !e.eventActive {
		return
	}

	// -----------------------------------------------------------------
	// 3️⃣  Pull the three signals we care about.
	// -----------------------------------------------------------------
	hBull, _ := e.suite.GetHMA().IsBullishCrossover()
	hBear, _ := e.suite.GetHMA().IsBearishCrossover()
	atsoRaw, _ := e.suite.GetATSO().Calculate()

	// -----------------------------------------------------------------
	// 4️⃣  Volatility‑burst filter: |ATSO| must exceed the threshold.
	// -----------------------------------------------------------------
	if math.Abs(atsoRaw) < e.eventThreshold {
		// Not enough volatility – wait for the next bar.
		return
	}

	// -----------------------------------------------------------------
	// 5️⃣  Determine direction and open the trade.
	// -----------------------------------------------------------------
	var side types.Side
	var longCond, shortCond bool

	if atsoRaw > 0 {
		// Upside burst – look for a bullish HMA breakout.
		longCond = hBull
		side = types.Buy
	} else {
		// Downside burst – look for a bearish HMA breakout.
		shortCond = hBear
		side = types.Sell
	}

	if (longCond && side == types.Buy) || (shortCond && side == types.Sell) {
		// Reset the holding‑bar counter and open the position.
		e.barSinceEntry = 0
		e.openPosition(side, close)
	}
}

// ---------------------------------------------------------------------
// openPosition – creates a market order sized by risk parameters.
// The stop‑loss distance is derived from the latest ATR (via ATSO) so it
// adapts to the current volatility regime.
// ---------------------------------------------------------------------
func (e *EventDriven) openPosition(side types.Side, price float64) {
	// Use ATSO as a proxy for volatility; the raw value is already a
	// volatility‑adjusted z‑score, but we also fetch the latest ATR for a
	// concrete dollar distance.
	atrVals := e.suite.GetATSO().GetATSOValues()
	if len(atrVals) == 0 {
		// Fallback – a tiny stop distance to avoid division by zero.
		atrVals = []float64{0.0001}
	}
	atr := atrVals[len(atrVals)-1]

	// Stop‑loss distance = ATR * StopLossPct (configurable).
	stopDist := atr * e.cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty := risk.CalcQty(e.exec.Equity(), e.cfg.MaxRiskPerTrade, stopDist/price, price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  e.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "EventDriven entry",
	}
	if err := e.exec.Submit(o); err != nil {
		log.Printf("[ERR] event‑driven submit entry: %v", err)
	}
}

// ---------------------------------------------------------------------
// closePosition – flattens the current position at market price.
// ---------------------------------------------------------------------
func (e *EventDriven) closePosition(price float64) {
	qty, _ := e.exec.Position(e.symbol)
	if qty == 0 {
		return
	}
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  e.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "EventDriven exit",
	}
	if err := e.exec.Submit(o); err != nil {
		log.Printf("[ERR] event‑driven submit exit: %v", err)
	}
}

// ---------------------------------------------------------------------
// manageOpenPosition – applies stop‑loss, take‑profit and optional
// trailing‑stop while the trade is alive.
// ---------------------------------------------------------------------
func (e *EventDriven) manageOpenPosition(currentPrice float64) {
	// ---- Fixed stop‑loss based on ATR ----
	atrVals := e.suite.GetATSO().GetATSOValues()
	if len(atrVals) == 0 {
		return
	}
	atr := atrVals[len(atrVals)-1]
	stopDist := atr * e.cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty, avg := e.exec.Position(e.symbol)
	if qty == 0 {
		return
	}
	// Compute stop‑loss levels.
	if qty > 0 { // long
		if currentPrice <= avg-stopDist {
			e.closePosition(currentPrice)
			return
		}
	} else { // short
		if currentPrice >= avg+stopDist {
			e.closePosition(currentPrice)
			return
		}
	}

	// ---- Take‑profit (optional, based on a multiple of ATR) ----
	if e.cfg.TakeProfitPct > 0 {
		target := avg
		if qty > 0 {
			target = avg + atr*e.cfg.TakeProfitPct
			if currentPrice >= target {
				e.closePosition(currentPrice)
				return
			}
		} else {
			target = avg - atr*e.cfg.TakeProfitPct
			if currentPrice <= target {
				e.closePosition(currentPrice)
				return
			}
		}
	}

	// ---- Trailing‑stop (percentage of entry price) ----
	if e.cfg.TrailingPct > 0 {
		var trailLevel float64
		if qty > 0 {
			trailLevel = avg * (1 + e.cfg.TrailingPct)
			if currentPrice >= trailLevel {
				e.closePosition(currentPrice)
			}
		} else {
			trailLevel = avg * (1 - e.cfg.TrailingPct)
			if currentPrice <= trailLevel {
				e.closePosition(currentPrice)
			}
		}
	}
}

// ---------------------------------------------------------------------
// lastClose – helper that returns the most recent close price from the
// suite (used when we need a price for a forced exit after the event ends).
// ---------------------------------------------------------------------
func (e *EventDriven) lastClose() float64 {
	closes := e.suite.GetRSI().GetCloses()
	if len(closes) == 0 {
		return 0
	}
	return closes[len(closes)-1]
}
