package risk

import (
	"testing"

	"github.com/evdnx/gots/config"
)

func TestCalcQtyBasic(t *testing.T) {
	cfg := config.StrategyConfig{
		StepSize:          0.01,
		QuantityPrecision: 2,
		MinQty:            0.05,
	}
	qty := CalcQty(10_000, 0.01, 0.015, 100, cfg) // risk $100, SL $1.5 => raw 66.66
	if qty != 66.66 {                             // floor to step 0.01, then 2‑dp -> 66.66
		t.Fatalf("unexpected qty: %v", qty)
	}
}

func TestCalcQtyRespectsMinQty(t *testing.T) {
	cfg := config.StrategyConfig{
		StepSize:          0.001,
		QuantityPrecision: 3,
		MinQty:            0.1,
	}
	qty := CalcQty(1000, 0.001, 0.02, 5000, cfg) // raw ~0.01 < MinQty
	if qty != 0 {
		t.Fatalf("expected 0 (below MinQty), got %v", qty)
	}
}

func TestCalcQtyZeroStepSizePanicsSafe(t *testing.T) {
	cfg := config.StrategyConfig{
		StepSize:          0,
		QuantityPrecision: 2,
		MinQty:            0.001,
	}
	// Should fall back to raw qty because step‑size <=0 is ignored.
	qty := CalcQty(5000, 0.02, 0.01, 50, cfg)
	if qty <= 0 {
		t.Fatalf("expected positive qty despite zero StepSize, got %v", qty)
	}
}
