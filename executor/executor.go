package executor

import (
	"log"
	"sync"

	"github.com/evdnx/gots/metrics"
	"github.com/evdnx/gots/types"
)

// Executor interface unchanged – see original file for definition.
type Executor interface {
	Submit(o types.Order) error
	Equity() float64
	Position(symbol string) (qty float64, avgPrice float64)
}

// PaperExecutor – simple in‑memory paper trader with mutex protection.
type PaperExecutor struct {
	mu        sync.RWMutex
	equity    float64
	positions map[string]float64 // qty (positive = long, negative = short)
	avgPrice  map[string]float64
}

// NewPaperExecutor creates a fresh executor with the supplied starting equity.
func NewPaperExecutor(startEquity float64) *PaperExecutor {
	return &PaperExecutor{
		equity:    startEquity,
		positions: make(map[string]float64),
		avgPrice:  make(map[string]float64),
	}
}

// Submit processes a market order (perfect fills, no slippage).
func (p *PaperExecutor) Submit(o types.Order) error {
	if o.Qty == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	cost := o.Price * o.Qty
	if o.Side == types.Buy {
		if cost > p.equity {
			return log.Output(2, "paper executor: insufficient cash")
		}
		p.equity -= cost
		p.positions[o.Symbol] += o.Qty
		prev := p.avgPrice[o.Symbol]
		newAvg := (prev*(p.positions[o.Symbol]-o.Qty) + cost) / p.positions[o.Symbol]
		p.avgPrice[o.Symbol] = newAvg
	} else { // Sell / short
		p.equity += cost
		p.positions[o.Symbol] -= o.Qty
		prev := p.avgPrice[o.Symbol]
		newAvg := (prev*(p.positions[o.Symbol]+o.Qty) + cost) / p.positions[o.Symbol]
		p.avgPrice[o.Symbol] = newAvg
	}
	metrics.OrdersSubmitted.WithLabelValues("paper").Inc()
	metrics.EquityGauge.Set(p.equity)

	log.Printf("[EXEC] %s %s %.4f @ %.2f (eq: %.2f)",
		o.Side, o.Symbol, o.Qty, o.Price, p.equity)
	return nil
}

// Equity returns the current cash balance (thread‑safe).
func (p *PaperExecutor) Equity() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.equity
}

// Position returns the current quantity and average entry price for a symbol.
func (p *PaperExecutor) Position(sym string) (float64, float64) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.positions[sym], p.avgPrice[sym]
}
