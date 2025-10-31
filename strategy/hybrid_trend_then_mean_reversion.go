package strategy

import (
	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/types"
	"go.uber.org/zap"
)

// hybridState enumerates the FSM states.
type hybridState int

const (
	stateIdle hybridState = iota
	stateTrend
	stateRevert
)

// HybridTrendMeanReversion implements the “trend‑then‑mean‑reversion” FSM.
type HybridTrendMeanReversion struct {
	*BaseStrategy
	state          hybridState
	trendSide      types.Side
	flatBarCounter int
}

// NewHybridTrendMeanReversion builds the suite and injects a logger.
func NewHybridTrendMeanReversion(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger) (*HybridTrendMeanReversion, error) {

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = 70
		ic.RSIOversold = 30
		ic.MFIOverbought = 80
		ic.MFIOversold = 20
		return goti.NewIndicatorSuiteWithConfig(ic)
	}
	base, err := NewBaseStrategy(symbol, cfg, exec, suiteFactory, log)
	if err != nil {
		return nil, err
	}
	return &HybridTrendMeanReversion{
		BaseStrategy:   base,
		state:          stateIdle,
		trendSide:      "",
		flatBarCounter: 0,
	}, nil
}

// ProcessBar drives the finite‑state machine.
func (h *HybridTrendMeanReversion) ProcessBar(high, low, close, volume float64) {
	if err := h.Suite.Add(high, low, close, volume); err != nil {
		h.Log.Warn("suite_add_error", zap.Error(err))
		return
	}

	if len(h.Suite.GetHMA().GetCloses()) < 10 {
		return // warm‑up
	}

	// Pull signals.
	hBull, _ := h.Suite.GetHMA().IsBullishCrossover()
	hBear, _ := h.Suite.GetHMA().IsBearishCrossover()
	rsiVal, _ := h.Suite.GetRSI().Calculate()
	mfiVal, _ := h.Suite.GetMFI().Calculate()
	posQty, _ := h.Exec.Position(h.Symbol)

	switch h.state {
	case stateIdle:
		if hBull {
			h.enterTrend(types.Buy, close)
		} else if hBear {
			h.enterTrend(types.Sell, close)
		}
	case stateTrend:
		// Reinforce trend or count flat bars.
		if h.trendSide == types.Buy && hBull {
			h.flatBarCounter = 0
		} else if h.trendSide == types.Sell && hBear {
			h.flatBarCounter = 0
		} else {
			h.flatBarCounter++
			const flatBarThreshold = 3
			if h.flatBarCounter >= flatBarThreshold {
				h.exitTrend(close)
				h.state = stateRevert
				h.flatBarCounter = 0
			}
		}
	case stateRevert:
		// Look for opposite‑direction oversold/overbought signal.
		if h.trendSide == types.Buy {
			if rsiVal >= h.Cfg.RSIOverbought && mfiVal >= h.Cfg.MFIOverbought {
				h.openOpposite(types.Sell, close)
				h.state = stateIdle
			}
		} else {
			if rsiVal <= h.Cfg.RSIOversold && mfiVal <= h.Cfg.MFIOversold {
				h.openOpposite(types.Buy, close)
				h.state = stateIdle
			}
		}
		// Manage any open position.
		if posQty != 0 && h.Cfg.TrailingPct > 0 {
			h.applyTrailingStop(close)
		}
	}
}

// enterTrend opens a position in the direction indicated by the HMA crossover.
func (h *HybridTrendMeanReversion) enterTrend(side types.Side, price float64) {
	qty := h.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  h.Symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "HybridTrend entry",
	}
	_ = h.submitOrder(o, "hybrid_trend_entry")
	h.state = stateTrend
	h.trendSide = side
	h.flatBarCounter = 0
}

// exitTrend closes the current trend position (if any) and stays in REVERT.
func (h *HybridTrendMeanReversion) exitTrend(price float64) {
	qty, _ := h.Exec.Position(h.Symbol)
	if qty == 0 {
		return
	}
	h.closePosition(price, "hybrid_trend_exit")
}

// openOpposite opens a contrarian trade during the REVERT phase.
func (h *HybridTrendMeanReversion) openOpposite(side types.Side, price float64) {
	qty := h.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  h.Symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "HybridRevert entry",
	}
	_ = h.submitOrder(o, "hybrid_revert_entry")
}
