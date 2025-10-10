package executor

import (
	"log"

	"github.com/evdnx/gots/types"
)

type Executor interface {
	Submit(o types.Order) error
	// For back‑testing we expose the portfolio state
	Equity() float64
	Position(symbol string) (qty float64, avgPrice float64)
}

// Very simple paper‑trader – perfect fills, no slippage
type PaperExecutor struct {
	equity    float64
	positions map[string]float64 // qty (positive = long, negative = short)
	avgPrice  map[string]float64
}

func NewPaperExecutor(startEquity float64) *PaperExecutor {
	return &PaperExecutor{
		equity:    startEquity,
		positions: make(map[string]float64),
		avgPrice:  make(map[string]float64),
	}
}

func (p *PaperExecutor) Submit(o types.Order) error {
	if o.Qty == 0 {
		return nil
	}
	// market fill – price = current market price (passed in Order.Price)
	cost := o.Price * o.Qty
	if o.Side == types.Buy {
		if cost > p.equity {
			return log.Output(2, "paper executor: insufficient cash")
		}
		p.equity -= cost
		p.positions[o.Symbol] += o.Qty
		// simple VWAP for avg price
		prev := p.avgPrice[o.Symbol]
		newAvg := (prev*(p.positions[o.Symbol]-o.Qty) + cost) / p.positions[o.Symbol]
		p.avgPrice[o.Symbol] = newAvg
	} else { // Sell / short
		p.equity += cost
		p.positions[o.Symbol] -= o.Qty
		// avg price for shorts is handled the same way
		prev := p.avgPrice[o.Symbol]
		newAvg := (prev*(p.positions[o.Symbol]+o.Qty) + cost) / p.positions[o.Symbol]
		p.avgPrice[o.Symbol] = newAvg
	}
	log.Printf("[EXEC] %s %s %.4f @ %.2f (eq: %.2f)", o.Side, o.Symbol, o.Qty, o.Price, p.equity)
	return nil
}

func (p *PaperExecutor) Equity() float64 { return p.equity }

func (p *PaperExecutor) Position(sym string) (float64, float64) {
	return p.positions[sym], p.avgPrice[sym]
}
