// Rotates capital across a *basket* of symbols based on a composite
// strength score derived from three oscillators (RSI, MFI, ATSO).
// The strategy maintains a **risk‑parity** allocation: each selected
// instrument receives an equal *fraction* of the total risk budget.
//
// Core ideas:
//
//   - For every incoming candle we update the suite for *that* symbol.
//   - A **strength score** = weighted sum of normalized RSI, MFI and ATSO.
//   - At the end of each evaluation interval (e.g. every N minutes)
//     we pick the top‑K symbols by score and allocate the same risk to each.
//   - Positions are entered with the generic `CalcQty` risk calculator.
//   - Existing positions that fall out of the top‑K are closed.
//
// The implementation is deliberately generic – you supply the list of
// symbols you want to monitor, the evaluation interval (in bars) and the
// number of symbols to hold (`TopK`).  All bookkeeping (last scores,
// open‑position tracking, etc.) lives inside the struct.
package strategy

import (
	"log"
	"math"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/types"
)

// SymbolState holds the per‑symbol suite and the most recent strength score.
type SymbolState struct {
	suite  *goti.IndicatorSuite
	score  float64
	symbol string
}

// RiskParityRotation is the main struct.
type RiskParityRotation struct {
	symbols      []string // universe of symbols we monitor
	states       map[string]*SymbolState
	cfg          config.StrategyConfig
	exec         executor.Executor
	topK         int // how many symbols to hold at any time
	barCnt       int // counts bars processed for the current symbol
	intervalBars int // evaluate every N bars (same for all symbols)
}

// NewRiskParityRotation builds a suite for each symbol in the universe.
func NewRiskParityRotation(symbols []string, cfg config.StrategyConfig,
	exec executor.Executor, topK int, intervalBars int) (*RiskParityRotation, error) {

	if topK <= 0 || topK > len(symbols) {
		return nil, log.Output(2, "invalid topK")
	}
	states := make(map[string]*SymbolState)
	for _, sym := range symbols {
		indCfg := goti.DefaultConfig()
		indCfg.RSIOverbought = cfg.RSIOverbought
		indCfg.RSIOversold = cfg.RSIOversold
		indCfg.MFIOverbought = cfg.MFIOverbought
		indCfg.MFIOversold = cfg.MFIOversold
		suite, err := goti.NewIndicatorSuiteWithConfig(indCfg)
		if err != nil {
			return nil, err
		}
		states[sym] = &SymbolState{
			suite:  suite,
			symbol: sym,
			score:  0,
		}
	}
	return &RiskParityRotation{
		symbols:      symbols,
		states:       states,
		cfg:          cfg,
		exec:         exec,
		topK:         topK,
		intervalBars: intervalBars,
	}, nil
}

// ProcessBar must be called for *every* symbol that receives a new candle.
// The caller is responsible for routing the correct OHLCV data to the
// appropriate symbol.
//
// Example usage in a live feed:
//
//	for each incoming tick {
//	    rp.ProcessBar(symbol, high, low, close, volume)
//	}
func (rp *RiskParityRotation) ProcessBar(symbol string, high, low, close, volume float64) {
	state, ok := rp.states[symbol]
	if !ok {
		// Unknown symbol – ignore silently (or log if you prefer).
		return
	}
	if err := state.suite.Add(high, low, close, volume); err != nil {
		log.Printf("[WARN] rp add error for %s: %v", symbol, err)
		return
	}
	rp.barCnt++

	// Update the strength score on every bar (so we have the freshest value).
	state.score = rp.computeStrength(state.suite)

	// When the evaluation interval elapses, rebalance the whole basket.
	if rp.barCnt%rp.intervalBars == 0 {
		rp.rebalance()
	}
}

// computeStrength builds a normalized composite score from RSI, MFI and ATSO.
// Normalisation: each indicator is mapped to [0,1] where 0 = worst (deeply
// oversold/overbought) and 1 = best (deeply overbought/oversold for a
// contrarian view).  The final score is a weighted sum.
func (rp *RiskParityRotation) computeStrength(suite *goti.IndicatorSuite) float64 {
	// ----- RSI component -----
	rsiVal, _ := suite.GetRSI().Calculate()
	rsiNorm := (rsiVal - rp.cfg.RSIOversold) / (rp.cfg.RSIOverbought - rp.cfg.RSIOversold)
	if rsiNorm < 0 {
		rsiNorm = 0
	}
	if rsiNorm > 1 {
		rsiNorm = 1
	}
	// ----- MFI component -----
	mfiVal, _ := suite.GetMFI().Calculate()
	mfiNorm := (mfiVal - rp.cfg.MFIOversold) / (rp.cfg.MFIOverbought - rp.cfg.MFIOversold)
	if mfiNorm < 0 {
		mfiNorm = 0
	}
	if mfiNorm > 1 {
		mfiNorm = 1
	}
	// ----- ATSO component (raw value, higher magnitude = stronger trend) -----
	atsoRaw, _ := suite.GetATSO().Calculate()
	// ATSO can be negative (downtrend) or positive (uptrend).  We take the
	// absolute value and cap it to a reasonable range (e.g. 0‑3) before
	// normalising.
	atsoAbs := math.Abs(atsoRaw)
	if atsoAbs > 3 {
		atsoAbs = 3
	}
	atsoNorm := atsoAbs / 3.0

	// Weighted sum – you can tweak the weights in the config if you wish.
	const (
		wRSI  = 0.35
		wMFI  = 0.35
		wATSO = 0.30
	)
	return wRSI*rsiNorm + wMFI*mfiNorm + wATSO*atsoNorm
}

// rebalance closes positions that fell out of the top‑K and opens equal‑risk
// positions for the newly‑selected symbols.
func (rp *RiskParityRotation) rebalance() {
	// 1️⃣  Sort symbols by score (descending).
	type kv struct {
		sym   string
		score float64
	}
	var sorted []kv
	for sym, st := range rp.states {
		sorted = append(sorted, kv{sym, st.score})
	}
	// Simple bubble sort – the list is tiny (dozens at most).
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].score > sorted[i].score {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	// 2️⃣  Determine the set of symbols we want to hold.
	targetSet := make(map[string]struct{})
	for i := 0; i < rp.topK && i < len(sorted); i++ {
		targetSet[sorted[i].sym] = struct{}{}
	}
	// 3️⃣  Close any position not in the target set.
	for _, sym := range rp.symbols {
		qty, _ := rp.exec.Position(sym)
		if qty == 0 {
			continue
		}
		if _, keep := targetSet[sym]; !keep {
			// Flatten the position at market price (price unknown here –
			// we use the last close from the suite as a proxy).
			lastClose := rp.states[sym].suite.GetRSI().GetCloses()
			if len(lastClose) == 0 {
				continue
			}
			price := lastClose[len(lastClose)-1]
			rp.closePosition(sym, price)
		}
	}
	// 4️⃣  Open equal‑risk positions for the symbols in the target set.
	//    Risk per trade = MaxRiskPerTrade * total equity / topK
	totalEquity := rp.exec.Equity()
	perTradeRisk := totalEquity * rp.cfg.MaxRiskPerTrade / float64(rp.topK)

	for sym := range targetSet {
		qty, _ := rp.exec.Position(sym)
		if qty != 0 {
			// Already have a position – skip (could also adjust size here).
			continue
		}
		// Use the latest close as entry price.
		closeSeries := rp.states[sym].suite.GetRSI().GetCloses()
		if len(closeSeries) == 0 {
			continue
		}
		price := closeSeries[len(closeSeries)-1]

		// Approximate stop‑distance using ATR from the ATSO suite (ATSO
		// already reflects volatility, so we reuse it).
		atrVals := rp.states[sym].suite.GetATSO().GetATSOValues()
		if len(atrVals) == 0 {
			continue
		}
		atr := atrVals[len(atrVals)-1]
		stopDist := atr * rp.cfg.StopLossPct
		if stopDist <= 0 {
			stopDist = 0.0001
		}
		qtyToTrade := perTradeRisk / stopDist
		qtyToTrade = math.Floor(qtyToTrade*100) / 100 // 2‑dp

		if qtyToTrade <= 0 {
			continue
		}
		// Decide side based on ATSO sign (positive = uptrend → long,
		// negative = downtrend → short).
		atsoRaw, _ := rp.states[sym].suite.GetATSO().Calculate()
		side := types.Buy
		if atsoRaw < 0 {
			side = types.Sell
		}
		o := types.Order{
			Symbol:  sym,
			Side:    side,
			Qty:     qtyToTrade,
			Price:   price,
			Comment: "RiskParity entry",
		}
		if err := rp.exec.Submit(o); err != nil {
			log.Printf("[ERR] risk‑parity submit %s: %v", sym, err)
		}
	}
}

// closePosition flattens the position for a given symbol.
func (rp *RiskParityRotation) closePosition(symbol string, price float64) {
	qty, _ := rp.exec.Position(symbol)
	if qty == 0 {
		return
	}
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: "RiskParity exit",
	}
	if err := rp.exec.Submit(o); err != nil {
		log.Printf("[ERR] risk‑parity close %s: %v", symbol, err)
	}
}
