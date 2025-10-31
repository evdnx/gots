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
	"github.com/evdnx/gots/types"
	"go.uber.org/zap"
)

// SymbolState holds the per‑symbol suite and the most recent strength score.
type SymbolState struct {
	suite  *goti.IndicatorSuite
	score  float64
	symbol string
}

// RiskParityRotation rotates capital across a basket of symbols based on a
// composite strength score (RSI, MFI, ATSO).  It is a multi‑symbol manager, so
// it does not embed BaseStrategy directly; instead it keeps its own logger.
type RiskParityRotation struct {
	symbols      []string
	states       map[string]*SymbolState
	cfg          config.StrategyConfig
	exec         executor.Executor
	topK         int
	barCnt       int
	intervalBars int
	log          logger.Logger
	mu           sync.RWMutex // protect states & counters
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
	rp.mu.RLock()
	state, ok := rp.states[symbol]
	rp.mu.RUnlock()
	if !ok {
		// Unknown symbol – ignore silently.
		return
	}
	if err := state.suite.Add(high, low, close, volume); err != nil {
		rp.log.Warn("rp_suite_add_error", zap.String("symbol", symbol), zap.Error(err))
		return
	}
	rp.mu.Lock()
	rp.barCnt++
	// Update strength score on every bar.
	state.score = rp.computeStrength(state.suite)
	// Rebalance when the interval elapses.
	if rp.barCnt%rp.intervalBars == 0 {
		rp.rebalance()
	}
	rp.mu.Unlock()
}

// computeStrength builds a normalized composite score from RSI, MFI and ATSO.
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
	// ----- ATSO component (absolute value, capped) -----
	atsoRaw, _ := suite.GetATSO().Calculate()
	atsoAbs := math.Abs(atsoRaw)
	if atsoAbs > 3 {
		atsoAbs = 3
	}
	atsoNorm := atsoAbs / 3.0

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

	// 2️⃣ Determine the target set (top‑K).
	targetSet := make(map[string]struct{})
	for i := 0; i < rp.topK && i < len(sorted); i++ {
		targetSet[sorted[i].sym] = struct{}{}
	}

	// 3️⃣ Close any position not in the target set.
	for _, sym := range rp.symbols {
		qty, _ := rp.exec.Position(sym)
		if qty == 0 {
			continue
		}
		if _, keep := targetSet[sym]; !keep {
			// Use the latest close price from the suite as a proxy.
			closeSeries := rp.states[sym].suite.GetRSI().GetCloses()
			if len(closeSeries) == 0 {
				continue
			}
			price := closeSeries[len(closeSeries)-1]
			rp.closePosition(sym, price)
		}
	}

	// 4️⃣ Open equal‑risk positions for the symbols in the target set.
	totalEquity := rp.exec.Equity()
	perTradeRisk := totalEquity * rp.cfg.MaxRiskPerTrade / float64(rp.topK)

	for sym := range targetSet {
		qty, _ := rp.exec.Position(sym)
		if qty != 0 {
			// Already have a position – skip (could adjust size here).
			continue
		}
		closeSeries := rp.states[sym].suite.GetRSI().GetCloses()
		if len(closeSeries) == 0 {
			continue
		}
		price := closeSeries[len(closeSeries)-1]

		// Approximate stop‑distance using ATSO (as a volatility proxy).
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
		// Side based on ATSO sign.
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
			rp.log.Error("risk_parity_submit_error",
				zap.String("symbol", sym), zap.Error(err))
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
			zap.String("symbol", symbol), zap.Error(err))
	}
}

// Helper to create a consistent error when logger is needed.
func logOutputError(l logger.Logger, msg string) error {
	l.Error("configuration_error", zap.String("msg", msg))
	return fmt.Errorf("%s", msg)
}
