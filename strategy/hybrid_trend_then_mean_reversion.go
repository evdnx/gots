// Hybrid “Trend‑then‑Mean‑Reversion” strategy.
//
// State machine:
//
//	STATE_IDLE   – No position, waiting for a clear HMA trend signal.
//	STATE_TREND  – Riding the detected trend (long or short) as long as the
//	               HMA continues to give crossovers in the same direction.
//	               If the HMA stops crossing for a configurable number of
//	               consecutive bars we consider the trend exhausted.
//	STATE_REVERS – After the trend expires we look for a contrarian signal
//	               using RSI/MFI (oversold → long, overbought → short).  When
//	               such a reversal signal appears we open a position opposite
//	               to the previous trend and then return to STATE_IDLE.
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
// State enumeration
// ---------------------------------------------------------------------
type hybridState int

const (
	stateIdle hybridState = iota
	stateTrend
	stateRevert
)

// ---------------------------------------------------------------------
// HybridTrendMeanReversion – public struct
// ---------------------------------------------------------------------
type HybridTrendMeanReversion struct {
	suite          *goti.IndicatorSuite  // all indicators for the symbol
	cfg            config.StrategyConfig // risk & threshold parameters
	exec           executor.Executor     // order router / paper‑trader
	symbol         string                // ticker (e.g. "BTCUSDT")
	state          hybridState           // current FSM state
	trendSide      types.Side            // direction of the active trend (Buy/Sell)
	flatBarCounter int                   // counts consecutive bars without a confirming HMA crossover
}

// ---------------------------------------------------------------------
// NewHybridTrendMeanReversion – constructor
// ---------------------------------------------------------------------
func NewHybridTrendMeanReversion(symbol string, cfg config.StrategyConfig,
	exec executor.Executor) (*HybridTrendMeanReversion, error) {

	indCfg := goti.DefaultConfig()
	indCfg.RSIOverbought = cfg.RSIOverbought
	indCfg.RSIOversold = cfg.RSIOversold
	indCfg.MFIOverbought = cfg.MFIOverbought
	indCfg.MFIOversold = cfg.MFIOversold

	suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
	if err != nil {
		return nil, err
	}
	return &HybridTrendMeanReversion{
		suite:  suite,
		cfg:    cfg,
		exec:   exec,
		symbol: symbol,
		state:  stateIdle,
	}, nil
}

// ---------------------------------------------------------------------
// ProcessBar – entry point for every new OHLCV candle.
// ---------------------------------------------------------------------
func (h *HybridTrendMeanReversion) ProcessBar(high, low, close, volume float64) {
	// 1️⃣  Feed the suite.
	if err := h.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] hybrid add error: %v", err)
		return
	}

	// 2️⃣  Pull the signals we need.
	hBull, _ := h.suite.GetHMA().IsBullishCrossover()
	hBear, _ := h.suite.GetHMA().IsBearishCrossover()
	rsiVal, _ := h.suite.GetRSI().Calculate()
	mfiVal, _ := h.suite.GetMFI().Calculate()
	posQty, _ := h.exec.Position(h.symbol)

	switch h.state {
	case stateIdle:
		// Look for a fresh trend signal.
		if hBull {
			h.enterTrend(types.Buy, close)
		} else if hBear {
			h.enterTrend(types.Sell, close)
		}
	case stateTrend:
		// Keep riding the trend as long as HMA continues to confirm it.
		if h.trendSide == types.Buy && hBull {
			h.flatBarCounter = 0 // trend reinforced
		} else if h.trendSide == types.Sell && hBear {
			h.flatBarCounter = 0 // trend reinforced
		} else {
			h.flatBarCounter++
			// If we miss a confirming crossover for N bars we deem the
			// trend exhausted and switch to reversal mode.
			const flatBarThreshold = 3 // can be moved to cfg if you wish
			if h.flatBarCounter >= flatBarThreshold {
				h.exitTrend(close)
				h.state = stateRevert
				h.flatBarCounter = 0
			}
		}
	case stateRevert:
		// In reversal mode we look for an opposite‑direction oversold/
		// overbought signal (RSI/MFI).  The direction is opposite to the
		// previous trend.
		if h.trendSide == types.Buy {
			// Previous trend was up → look for a *short* reversal.
			if rsiVal >= h.cfg.RSIOverbought && mfiVal >= h.cfg.MFIOverbought {
				h.openPosition(types.Sell, close)
				h.state = stateIdle
			}
		} else {
			// Previous trend was down → look for a *long* reversal.
			if rsiVal <= h.cfg.RSIOversold && mfiVal <= h.cfg.MFIOversold {
				h.openPosition(types.Buy, close)
				h.state = stateIdle
			}
		}
		// Safety net – if we already have a position, manage SL/TP/TS.
		if posQty != 0 {
			h.manageOpenPosition(close)
		}
	}
}

// ---------------------------------------------------------------------
// enterTrend – opens a position in the direction indicated by the HMA
// crossover and switches the FSM to STATE_TREND.
// ---------------------------------------------------------------------
func (h *HybridTrendMeanReversion) enterTrend(side types.Side, price float64) {
	qty := risk.CalcQty(h.exec.Equity(), h.cfg.MaxRiskPerTrade,
		h.cfg.StopLossPct, price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  h.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "HybridTrend entry",
	}
	if err := h.exec.Submit(o); err != nil {
		log.Printf("[ERR] hybrid trend entry: %v", err)
		return
	}
	h.state = stateTrend
	h.trendSide = side
	h.flatBarCounter = 0
}

// ---------------------------------------------------------------------
// exitTrend – closes the current trend position (if any) and resets the
// FSM to STATE_REVERT.
// ---------------------------------------------------------------------
func (h *HybridTrendMeanReversion) exitTrend(price float64) {
	qty, _ := h.exec.Position(h.symbol)
	if qty == 0 {
		return
	}
	// Reverse the side to flatten the position.
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  h.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "HybridTrend exit",
	}
	if err := h.exec.Submit(o); err != nil {
		log.Printf("[ERR] hybrid trend exit: %v", err)
	}
	// After exiting we stay in STATE_REVERT (handled by ProcessBar).
}

// ---------------------------------------------------------------------
// openPosition – used in the reversal phase to open a contrarian trade.
// ---------------------------------------------------------------------
func (h *HybridTrendMeanReversion) openPosition(side types.Side, price float64) {
	qty := risk.CalcQty(h.exec.Equity(), h.cfg.MaxRiskPerTrade,
		h.cfg.StopLossPct, price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  h.symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "HybridRevert entry",
	}
	if err := h.exec.Submit(o); err != nil {
		log.Printf("[ERR] hybrid revert entry: %v", err)
	}
}

// ---------------------------------------------------------------------
// manageOpenPosition – applies stop‑loss, take‑profit and optional
// trailing‑stop while a position is alive.
// ---------------------------------------------------------------------
func (h *HybridTrendMeanReversion) manageOpenPosition(currentPrice float64) {
	// Fixed stop‑loss based on ATR (via ATSO) – we reuse ATSO values as a
	// volatility proxy.
	atrVals := h.suite.GetATSO().GetATSOValues()
	if len(atrVals) == 0 {
		return
	}
	atr := atrVals[len(atrVals)-1]
	stopDist := atr * h.cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty, avg := h.exec.Position(h.symbol)
	if qty == 0 {
		return
	}
	// ---- Stop‑loss ----
	if qty > 0 { // long
		if currentPrice <= avg-stopDist {
			h.closePosition(currentPrice)
			return
		}
	} else { // short
		if currentPrice >= avg+stopDist {
			h.closePosition(currentPrice)
			return
		}
	}
	// ---- Take‑profit (optional, ATR‑multiple) ----
	if h.cfg.TakeProfitPct > 0 {
		target := avg
		if qty > 0 {
			target = avg + atr*h.cfg.TakeProfitPct
			if currentPrice >= target {
				h.closePosition(currentPrice)
				return
			}
		} else {
			target = avg - atr*h.cfg.TakeProfitPct
			if currentPrice <= target {
				h.closePosition(currentPrice)
				return
			}
		}
	}
	// ---- Trailing‑stop (percentage) ----
	if h.cfg.TrailingPct > 0 {
		var trailLevel float64
		if qty > 0 {
			trailLevel = avg * (1 + h.cfg.TrailingPct)
			if currentPrice >= trailLevel {
				h.closePosition(currentPrice)
			}
		} else {
			trailLevel = avg * (1 - h.cfg.TrailingPct)
			if currentPrice <= trailLevel {
				h.closePosition(currentPrice)
			}
		}
	}
}

// ---------------------------------------------------------------------
// closePosition – flattens the current position at market price.
// ---------------------------------------------------------------------
func (h *HybridTrendMeanReversion) closePosition(price float64) {
	qty, _ := h.exec.Position(h.symbol)
	if qty == 0 {
		return
	}
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  h.symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "Hybrid close",
	}
	if err := h.exec.Submit(o); err != nil {
		log.Printf("[ERR] hybrid close: %v", err)
	}
}
