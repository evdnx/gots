package executor

import (
	"testing"

	"github.com/evdnx/gots/types"
)

func TestPaperExecutor_SubmitAndPosition(t *testing.T) {
	ex := NewPaperExecutor(10_000)

	o := types.Order{
		Symbol: "BTCUSD",
		Side:   types.Buy,
		Qty:    0.5,
		Price:  20_000,
	}
	if err := ex.Submit(o); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if eq := ex.Equity(); eq != 0 {
		t.Fatalf("expected equity 0 after buying 0.5*20000, got %v", eq)
	}
	qty, avg := ex.Position("BTCUSD")
	if qty != 0.5 || avg != 20_000 {
		t.Fatalf("unexpected position: qty=%v avg=%v", qty, avg)
	}
}

func TestPaperExecutor_InsufficientCash(t *testing.T) {
	ex := NewPaperExecutor(1000)
	o := types.Order{
		Symbol: "ETHUSD",
		Side:   types.Buy,
		Qty:    1,
		Price:  2000,
	}
	if err := ex.Submit(o); err != nil {
		t.Fatalf("expected graceful handling, got error %v", err)
	}
	if eq := ex.Equity(); eq != 1000 {
		t.Fatalf("equity should stay unchanged on insufficient cash")
	}
}
