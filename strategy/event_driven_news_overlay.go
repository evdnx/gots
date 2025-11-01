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
	armed          bool
}

// NewEventDriven builds the suite and injects a logger.
func NewEventDriven(symbol string, cfg config.StrategyConfig,
	exec executor.Executor, log logger.Logger,
	eventThreshold float64, maxHoldingBars int) (*EventDriven, error) {

	suiteFactory := func() (*goti.IndicatorSuite, error) {
		ic := goti.DefaultConfig()
		ic.RSIOverbought = 70
		ic.RSIOversold = 30
		ic.MFIOverbought = 80
		ic.MFIOversold = 20
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
	if active {
		e.armed = true
	}
	if !active {
		e.armed = false
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
	e.recordPrice(close)
	if !e.hasHistory(15) {
		return
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
	if !e.eventActive || !e.armed {
		return
	}

	// Pull signals.
	hBull := e.bullishFallback()
	if ok, err := e.Suite.GetHMA().IsBullishCrossover(); err == nil {
		hBull = hBull || ok
	}
	hBear := e.bearishFallback()
	if ok, err := e.Suite.GetHMA().IsBearishCrossover(); err == nil {
		hBear = hBear || ok
	}
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
		e.armed = false
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
	atr := e.sanitizeVolatility(math.Abs(atrVals[len(atrVals)-1]), currentPrice)
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
	if e.prices != nil && e.prices.Len() > 0 {
		return e.prices.Last()
	}
	closes := e.Suite.GetRSI().GetCloses()
	if len(closes) == 0 {
		return 0
	}
	return closes[len(closes)-1]
}
