package strategy

import (
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/risk"
	"github.com/evdnx/gots/types"
)

// SymbolState holds the per‑symbol suite and the most recent strength score.
type barSnapshot struct {
	high, low, close, volume float64
}

type SymbolState struct {
	suite     *goti.IndicatorSuite
	score     float64
	symbol    string
	lastBar   barSnapshot
	hasLast   bool
	prevClose float64
	hasPrev   bool
}

// RiskParityRotation rotates capital across a basket of symbols based on a
// composite strength score (RSI, MFI, ATSO).  It is a multi‑symbol manager, so
// it does not embed BaseStrategy directly; instead it keeps its own logger.
type RiskParityRotation struct {
	symbols            []string
	states             map[string]*SymbolState
	cfg                config.StrategyConfig
	exec               executor.Executor
	topK               int
	barCnt             int
	intervalBars       int
	log                logger.Logger
	mu                 sync.RWMutex // protect states & counters
	barsSinceRebalance int
}

// NewRiskParityRotation builds a suite for each symbol and injects a logger.
func NewRiskParityRotation(symbols []string, cfg config.StrategyConfig,
	exec executor.Executor, topK int, intervalBars int, log logger.Logger) (*RiskParityRotation, error) {

	if topK <= 0 || topK > len(symbols) {
		return nil, logOutputError(log, "invalid topK")
	}
	states := make(map[string]*SymbolState)
	for _, sym := range symbols {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = 70
		ic.RSIOversold = 30
		ic.MFIOverbought = 80
		ic.MFIOversold = 20
		suite, err := goti.NewIndicatorSuiteWithConfig(ic)
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
		log:          log,
	}, nil
}

// ProcessBar must be called for *every* symbol that receives a new candle.
func (rp *RiskParityRotation) ProcessBar(symbol string, high, low, close, volume float64) {
	rp.mu.Lock()
	state, ok := rp.states[symbol]
	if !ok {
		rp.mu.Unlock()
		// Unknown symbol – ignore silently.
		return
	}
	if err := state.suite.Add(high, low, close, volume); err != nil {
		rp.mu.Unlock()
		rp.log.Warn("rp_suite_add_error",
			logger.String("symbol", symbol),
			logger.Err(err),
		)
		return
	}
	if state.hasLast {
		state.prevClose = state.lastBar.close
		state.hasPrev = state.hasLast
	}
	state.lastBar = barSnapshot{
		high:   high,
		low:    low,
		close:  close,
		volume: volume,
	}
	state.hasLast = true
	rp.barsSinceRebalance++
	// Update strength score on every bar.
	state.score = rp.computeStrength(state)
	// Rebalance when all symbols for the interval have been processed.
	requiredBars := rp.intervalBars * len(rp.symbols)
	if requiredBars == 0 {
		requiredBars = len(rp.symbols)
	}
	if rp.barsSinceRebalance >= requiredBars {
		rp.rebalance()
		rp.barsSinceRebalance = 0
	}
	rp.mu.Unlock()
}

// computeStrength builds a normalized composite score from RSI, MFI and ATSO.
func (rp *RiskParityRotation) computeStrength(state *SymbolState) float64 {
	suite := state.suite
	defaults := goti.DefaultConfig()

	rsiUpper := rp.cfg.RSIOverbought
	rsiLower := rp.cfg.RSIOversold
	if rsiUpper <= rsiLower {
		rsiUpper = defaults.RSIOverbought
		rsiLower = defaults.RSIOversold
	}
	mfiUpper := rp.cfg.MFIOverbought
	mfiLower := rp.cfg.MFIOversold
	if mfiUpper <= mfiLower {
		mfiUpper = defaults.MFIOverbought
		mfiLower = defaults.MFIOversold
	}

	rsiVal, rsiErr := suite.GetRSI().Calculate()
	mfiVal, mfiErr := suite.GetMFI().Calculate()
	atsoVals := suite.GetATSO().GetATSOValues()

	if rsiErr == nil && mfiErr == nil && len(atsoVals) > 0 {
		rsiNorm := clamp01((rsiVal - rsiLower) / (rsiUpper - rsiLower))
		mfiNorm := clamp01((mfiVal - mfiLower) / (mfiUpper - mfiLower))
		atsoNorm := clamp01(math.Abs(atsoVals[len(atsoVals)-1]) / 3.0)

		const (
			wRSI  = 0.35
			wMFI  = 0.35
			wATSO = 0.30
		)
		return wRSI*rsiNorm + wMFI*mfiNorm + wATSO*atsoNorm
	}

	if !state.hasLast {
		return 0
	}

	closePx := state.lastBar.close
	if closePx <= 0 {
		closePx = 1
	}
	rangePerc := 0.0
	if span := state.lastBar.high - state.lastBar.low; span > 0 {
		rangePerc = clamp01(span / (closePx * 0.05))
	}
	momentum := 0.0
	if state.hasPrev && state.prevClose > 0 {
		delta := (state.lastBar.close - state.prevClose) / state.prevClose
		delta = clamp(delta/0.05, 0, 1)
		momentum = delta
	}
	volumeNorm := 0.0
	if state.lastBar.volume > 0 {
		volumeNorm = clamp01(state.lastBar.volume / 8000.0)
	}
	return 0.6*rangePerc + 0.3*momentum + 0.1*volumeNorm
}

// rebalance closes positions that fell out of the top‑K and opens equal‑risk
// positions for the newly‑selected symbols.
func (rp *RiskParityRotation) rebalance() {
	// 1️⃣ Sort symbols by descending score.
	type kv struct {
		sym   string
		score float64
	}
	var sorted []kv
	for sym, st := range rp.states {
		sorted = append(sorted, kv{sym, st.score})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].score > sorted[j].score })

	// 2️⃣ Determine the target set (top‑K) with a minimum strength threshold.
	targetSet := make(map[string]struct{})
	const strengthThreshold = 0.1
	for i := 0; i < rp.topK && i < len(sorted); i++ {
		if sorted[i].score <= strengthThreshold {
			break
		}
		targetSet[sorted[i].sym] = struct{}{}
	}

	// 3️⃣ Close any position not in the target set.
	for _, sym := range rp.symbols {
		qty, _ := rp.exec.Position(sym)
		if qty == 0 {
			continue
		}
		if _, keep := targetSet[sym]; !keep {
			state := rp.states[sym]
			price := state.lastBar.close
			if price == 0 {
				closeSeries := state.suite.GetRSI().GetCloses()
				if len(closeSeries) > 0 {
					price = closeSeries[len(closeSeries)-1]
				}
			}
			if price == 0 {
				continue
			}
			rp.closePosition(sym, price)
		}
	}

	// 4️⃣ Open equal‑risk positions for the symbols in the target set.
	totalEquity := rp.exec.Equity()
	perTradeRiskFraction := rp.cfg.MaxRiskPerTrade / float64(rp.topK)

	for sym := range targetSet {
		qty, _ := rp.exec.Position(sym)
		if qty != 0 {
			// Already have a position – skip (could adjust size here).
			continue
		}
		state := rp.states[sym]
		price := state.lastBar.close
		if price == 0 {
			closeSeries := state.suite.GetRSI().GetCloses()
			if len(closeSeries) > 0 {
				price = closeSeries[len(closeSeries)-1]
			}
		}
		if price == 0 {
			continue
		}

		qtyToTrade := risk.CalcQty(totalEquity, perTradeRiskFraction, rp.cfg.StopLossPct, price, rp.cfg)

		if qtyToTrade <= 0 {
			continue
		}
		// Side based on ATSO sign.
		atsoRaw, err := state.suite.GetATSO().Calculate()
		side := types.Buy
		if err == nil && atsoRaw < 0 {
			side = types.Sell
		} else if err != nil && state.hasPrev && state.prevClose > 0 && state.lastBar.close < state.prevClose {
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
			rp.log.Error("risk_parity_submit_error",
				logger.String("symbol", sym),
				logger.Err(err),
			)
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
		rp.log.Error("risk_parity_close_error",
			logger.String("symbol", symbol),
			logger.Err(err),
		)
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clamp(v, minVal, maxVal float64) float64 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

// Helper to create a consistent error when logger is needed.
func logOutputError(l logger.Logger, msg string) error {
	l.Error("configuration_error", logger.String("msg", msg))
	return fmt.Errorf("%s", msg)
}
