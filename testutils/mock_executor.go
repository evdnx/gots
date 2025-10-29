package testutils

import (
	"sync"

	"github.com/evdnx/gots/types"
)

// MockExecutor implements the Executor interface in‑memory.
type MockExecutor struct {
	mu        sync.RWMutex
	equity    float64
	positions map[string]float64 // qty (signed)
	avgPrice  map[string]float64
	orders    []types.Order // captured for assertions
}

// NewMockExecutor creates a fresh executor with the supplied starting equity.
func NewMockExecutor(startEquity float64) *MockExecutor {
	return &MockExecutor{
		equity:    startEquity,
		positions: make(map[string]float64),
		avgPrice:  make(map[string]float64),
	}
}

// Submit records the order and updates equity/position exactly like PaperExecutor.
func (m *MockExecutor) Submit(o types.Order) error {
	if o.Qty == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	cost := o.Price * o.Qty
	if o.Side == types.Buy {
		if cost > m.equity {
			return nil // mimic “insufficient cash” – no panic
		}
		m.equity -= cost
		m.positions[o.Symbol] += o.Qty
		prev := m.avgPrice[o.Symbol]
		newAvg := (prev*(m.positions[o.Symbol]-o.Qty) + cost) / m.positions[o.Symbol]
		m.avgPrice[o.Symbol] = newAvg
	} else { // Sell / short
		m.equity += cost
		m.positions[o.Symbol] -= o.Qty
		prev := m.avgPrice[o.Symbol]
		newAvg := (prev*(m.positions[o.Symbol]+o.Qty) + cost) / m.positions[o.Symbol]
		m.avgPrice[o.Symbol] = newAvg
	}
	m.orders = append(m.orders, o)
	return nil
}

// Equity returns the current cash balance.
func (m *MockExecutor) Equity() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.equity
}

// Position returns qty & avg price for a symbol.
func (m *MockExecutor) Position(symbol string) (float64, float64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.positions[symbol], m.avgPrice[symbol]
}

// Orders returns a copy of all submitted orders (useful for assertions).
func (m *MockExecutor) Orders() []types.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]types.Order, len(m.orders))
	copy(out, m.orders)
	return out
}
