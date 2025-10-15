package strategy

import (
	"math"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/types"
	"go.uber.org/zap"
)

// EventDriven implements the news‑overlay strategy.
type EventDriven struct {
	*BaseStrategy
	eventActive    bool
	eventThreshold float64 // absolute ATSO magnitude required to trigger
	maxHoldingBars int
	barSinceEntry  int
}

// NewEventDriven builds the suite and injects a logger.
func NewEventDriven(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger,
	eventThreshold float64, maxHoldingBars int) (*EventDriven, error) {

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = cfg.RSIOverbought
		ic.RSIOversold = cfg.RSIOversold
		ic.MFIOverbought = cfg.MFIOverbought
		ic.MFIOversold = cfg.MFIOversold
		ic.ATSEMAperiod = cfg.ATSEMAperiod
		return goti.NewIndicatorSuiteWithConfig(ic)
	}
	base, err := NewBaseStrategy(symbol, cfg, exec, suiteFactory, log)
	if err != nil {
		return nil, err
	}
	return &EventDriven{
		BaseStrategy:   base,
		eventActive:    false,
		eventThreshold: eventThreshold,
		maxHoldingBars: maxHoldingBars,
		barSinceEntry:  0,
	}, nil
}

// SetEventActive toggles the external news flag.
func (e *EventDriven) SetEventActive(active bool) {
	e.eventActive = active
	if !active {
		if qty, _ := e.Exec.Position(e.Symbol); qty != 0 {
			e.closePosition(e.lastClose(), "event_inactive_close")
		}
	}
}

// ProcessBar handles each incoming candle.
func (e *EventDriven) ProcessBar(high, low, close, volume float64) {
	if err := e.Suite.Add(high, low, close, volume); err != nil {
		e.Log.Warn("suite_add_error", zap.Error(err))
		return
	}
	if len(e.Suite.GetHMA().GetCloses()) < 10 {
		return // warm‑up
	}

	// If we already have a position, manage it first.
	if qty, _ := e.Exec.Position(e.Symbol); qty != 0 {
		e.barSinceEntry++
		e.manageOpenPosition(close)
		if e.barSinceEntry >= e.maxHoldingBars {
			e.closePosition(close, "event_max_holding")
		}
		return
	}

	// No open position – act only when the external event flag is true.
	if !e.eventActive {
		return
	}

	// Pull signals.
	hBull, _ := e.Suite.GetHMA().IsBullishCrossover()
	hBear, _ := e.Suite.GetHMA().IsBearishCrossover()
	atsoRaw, _ := e.Suite.GetATSO().Calculate()

	// Volatility‑burst filter.
	if math.Abs(atsoRaw) < e.eventThreshold {
		return
	}

	var side types.Side
	var cond bool
	if atsoRaw > 0 {
		cond = hBull
		side = types.Buy
	} else {
		cond = hBear
		side = types.Sell
	}

	if cond {
		e.barSinceEntry = 0
		e.openPosition(side, close)
	}
}

// openPosition creates a market order sized by risk.
func (e *EventDriven) openPosition(side types.Side, price float64) {
	qty := e.calcQty(price)
	if qty <= 0 {
		return
	}
	o := types.Order{
		Symbol:  e.Symbol,
		Side:    side,
		Qty:     qty,
		Price:   price,
		Comment: "EventDriven entry",
	}
	_ = e.submitOrder(o, "event_entry")
}

// manageOpenPosition applies stop‑loss, TP and trailing‑stop.
func (e *EventDriven) manageOpenPosition(currentPrice float64) {
	// Fixed ATR‑based stop‑loss.
	atrVals := e.Suite.GetATSO().GetATSOValues()
	if len(atrVals) == 0 {
		return
	}
	atr := atrVals[len(atrVals)-1]
	stopDist := atr * e.Cfg.StopLossPct
	if stopDist <= 0 {
		stopDist = 0.0001
	}
	qty, avg := e.Exec.Position(e.Symbol)
	if qty == 0 {
		return
	}
	// Stop‑loss.
	if qty > 0 && currentPrice <= avg-stopDist {
		e.closePosition(currentPrice, "event_sl")
		return
	}
	if qty < 0 && currentPrice >= avg+stopDist {
		e.closePosition(currentPrice, "event_sl")
		return
	}
	// Optional TP.
	if e.Cfg.TakeProfitPct > 0 {
		target := avg
		if qty > 0 {
			target = avg + atr*e.Cfg.TakeProfitPct
			if currentPrice >= target {
				e.closePosition(currentPrice, "event_tp")
				return
			}
		} else {
			target = avg - atr*e.Cfg.TakeProfitPct
			if currentPrice <= target {
				e.closePosition(currentPrice, "event_tp")
				return
			}
		}
	}
	// Trailing‑stop.
	if e.Cfg.TrailingPct > 0 {
		e.applyTrailingStop(currentPrice)
	}
}

// lastClose returns the most recent close price from the suite.
func (e *EventDriven) lastClose() float64 {
	closes := e.Suite.GetRSI().GetCloses()
	if len(closes) == 0 {
		return 0
	}
	return closes[len(closes)-1]
}
