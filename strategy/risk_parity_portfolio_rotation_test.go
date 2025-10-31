package strategy

import (
	"testing"

	"github.com/evdnx/gots/testutils"
	"github.com/evdnx/gots/types"
)

/*
-----------------------------------------------------------------------
Test 1 – Initial rebalance opens a position for the top‑K symbol.
-----------------------------------------------------------------------
We use two symbols, `topK = 1`, and a single‑bar interval.
“AAA” receives a volatile bar (large ATSO magnitude) while “BBB” gets a
flat bar, so the strength of AAA is higher and the manager should open a
BUY order for AAA.
*/
func TestRiskParity_InitialRebalanceOpensTopK(t *testing.T) {
	symbols := []string{"AAA", "BBB"}
	rp, exec := buildRiskParity(t, symbols, 1, 1)

	// ---- volatile bar for AAA ----
	rp.ProcessBar("AAA", 110, 90, 100, 1500)

	// ---- flat bar for BBB ----
	rp.ProcessBar("BBB", 101, 99, 100, 1500)

	// After processing both symbols the interval (1 bar) has elapsed,
	// triggering a rebalance.
	if len(exec.Orders()) != 1 {
		t.Fatalf("expected one order after initial rebalance, got %d", len(exec.Orders()))
	}
	o := exec.Orders()[0]
	if o.Symbol != "AAA" {
		t.Fatalf("expected order for AAA (top‑K), got %s", o.Symbol)
	}
	if o.Side != types.Buy && o.Side != types.Sell {
		t.Fatalf("expected BUY or SELL side, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("expected positive quantity, got %f", o.Qty)
	}
}

/*
-----------------------------------------------------------------------
Test 2 – Top‑K changes on the next interval → old position closed,
new position opened.
-----------------------------------------------------------------------
Interval = 1, `topK = 1`.  First interval: AAA volatile → position opened.
Second interval: BBB becomes volatile, AAA becomes flat → manager should
close AAA and open a position for BBB.
*/
func TestRiskParity_SwitchesPositionsWhenTopKChanges(t *testing.T) {
	symbols := []string{"AAA", "BBB"}
	rp, exec := buildRiskParity(t, symbols, 1, 1)

	/* ---------- Interval 1 – AAA volatile, BBB flat ---------- */
	rp.ProcessBar("AAA", 110, 90, 100, 1500) // high ATSO magnitude
	rp.ProcessBar("BBB", 101, 99, 100, 1500) // low ATSO magnitude

	if len(exec.Orders()) != 1 || exec.Orders()[0].Symbol != "AAA" {
		t.Fatalf("expected initial order for AAA, got %+v", exec.Orders())
	}
	aaaQty := exec.Orders()[0].Qty

	/* ---------- Interval 2 – BBB volatile, AAA flat ---------- */
	rp.ProcessBar("AAA", 101, 99, 100, 1500) // now flat
	rp.ProcessBar("BBB", 120, 80, 100, 1500) // high ATSO magnitude

	/*
	   After the second interval we expect two additional orders:
	     1. Close AAA (SELL)
	     2. Open BBB (BUY or SELL depending on ATSO sign)
	*/
	if len(exec.Orders()) != 3 {
		t.Fatalf("expected three total orders after switch, got %d: %+v", len(exec.Orders()), exec.Orders())
	}
	closeAAA := exec.Orders()[1]
	if closeAAA.Symbol != "AAA" || closeAAA.Side != types.Sell {
		t.Fatalf("expected SELL order to close AAA, got %+v", closeAAA)
	}
	if closeAAA.Qty != aaaQty {
		t.Fatalf("close‑AAA quantity (%f) should equal original AAA quantity (%f)", closeAAA.Qty, aaaQty)
	}
	openBBB := exec.Orders()[2]
	if openBBB.Symbol != "BBB" {
		t.Fatalf("expected new order for BBB, got %s", openBBB.Symbol)
	}
	if openBBB.Qty <= 0 {
		t.Fatalf("expected positive quantity for BBB entry, got %f", openBBB.Qty)
	}
}

/*
-----------------------------------------------------------------------
Test 3 – All strengths drop to zero → any open position is closed.
-----------------------------------------------------------------------
First interval opens a position for AAA (volatile bar).  Second interval
supplies flat bars for both symbols, making ATSO magnitude ≈ 0, so the
composite strength becomes zero for every symbol.  The manager should
close the AAA position.
*/
func TestRiskParity_ClosesAllWhenNoStrength(t *testing.T) {
	symbols := []string{"AAA", "BBB"}
	rp, exec := buildRiskParity(t, symbols, 1, 1)

	/* ---------- Interval 1 – open AAA ---------- */
	rp.ProcessBar("AAA", 110, 90, 100, 1500) // volatile → strength > 0
	rp.ProcessBar("BBB", 101, 99, 100, 1500) // flat → strength ≈ 0

	if len(exec.Orders()) != 1 || exec.Orders()[0].Symbol != "AAA" {
		t.Fatalf("expected initial order for AAA, got %+v", exec.Orders())
	}
	aaaQty := exec.Orders()[0].Qty

	/* ---------- Interval 2 – flat bars for both ---------- */
	flat := []candle{
		{
			high:   100.1,
			low:    99.9,
			close:  100.0,
			volume: 1500,
		},
	}
	// Feed flat bar to both symbols.
	rp.ProcessBar("AAA", flat[0].high, flat[0].low, flat[0].close, flat[0].volume)
	rp.ProcessBar("BBB", flat[0].high, flat[0].low, flat[0].close, flat[0].volume)

	/*
	   After the second interval the manager should issue a SELL order that
	   closes the AAA position.
	*/
	if len(exec.Orders()) != 2 {
		t.Fatalf("expected a second order to close AAA, got %d: %+v", len(exec.Orders()), exec.Orders())
	}
	closeAAA := exec.Orders()[1]
	if closeAAA.Symbol != "AAA" || closeAAA.Side != types.Sell {
		t.Fatalf("expected SELL order to close AAA, got %+v", closeAAA)
	}
	if closeAAA.Qty != aaaQty {
		t.Fatalf("close‑AAA quantity (%f) should equal original AAA quantity (%f)", closeAAA.Qty, aaaQty)
	}
}

/*
-----------------------------------------------------------------------
Test 4 – Invalid topK is rejected (already covered in the helper,
but we keep a sanity‑check here).
-----------------------------------------------------------------------
*/
func TestRiskParity_InvalidTopK(t *testing.T) {
	symbols := []string{"AAA", "BBB"}
	_, err := NewRiskParityRotation(symbols, buildConfig(), testutils.NewMockExecutor(10_000), 0, 1, testutils.NewMockLogger())
	if err == nil {
		t.Fatalf("expected error for topK=0, got nil")
	}
	_, err = NewRiskParityRotation(symbols, buildConfig(), testutils.NewMockExecutor(10_000), 3, 1, testutils.NewMockLogger())
	if err == nil {
		t.Fatalf("expected error for topK > len(symbols), got nil")
	}
}
