package strategy

import (
	"testing"

	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/testutils"
	"github.com/evdnx/gots/types"
)

// feedBars sends a slice of candles to the specified symbol inside the
// RiskParityRotation manager.
func feedBarsRiskParity(t *testing.T, rp *RiskParityRotation, symbol string, bars []candle) {
	for _, b := range bars {
		rp.ProcessBar(symbol, b.high, b.low, b.close, b.volume)
	}
}

// buildRiskParity creates a RiskParityRotation instance wired to a mock
// executor / logger.  All oscillator thresholds are set to extreme values
// so the RSI/MFI parts of the composite strength are effectively zero;
// the ATSO magnitude (derived from price volatility) will dominate the
// ranking, which makes the expected behaviour deterministic.
func buildRiskParity(t *testing.T,
	symbols []string, topK, intervalBars int) (*RiskParityRotation, *testutils.MockExecutor) {

	// Extreme thresholds – RSI/MFI never influence the strength score.
	cfg := config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		HMAPeriod:         9,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.01,  // 1 % of equity per trade
		StopLossPct:       0.015, // 1.5 %
		TakeProfitPct:     0.0,   // not needed for these tests
		TrailingPct:       0.0,   // not needed for these tests
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}

	mockExec := testutils.NewMockExecutor(10_000) // $10 k start equity
	mockLog := testutils.NewMockLogger()

	rp, err := NewRiskParityRotation(symbols, cfg, mockExec, topK, intervalBars, mockLog)
	if err != nil {
		t.Fatalf("NewRiskParityRotation failed: %v", err)
	}
	return rp, mockExec
}

/*
-----------------------------------------------------------------------
Test 1 – Initial rebalance opens positions for the top‑K symbols.
-----------------------------------------------------------------------
We use three symbols and `topK = 1`.  By feeding a **high‑volatility**
bar to “AAA” and a flat bar to “BBB”, the ATSO magnitude for “AAA”
will be larger, so its composite strength will be higher.  After the
first interval the manager should open a position for “AAA”.
*/
func TestRiskParity_InitialRebalanceOpensTopK(t *testing.T) {
	symbols := []string{"AAA", "BBB"}
	rp, exec := buildRiskParity(t, symbols, 1, 1)

	// ---- Bar 1 – volatile bar for AAA (large price swing) ----
	volBarAAA := []candle{
		{high: 110, low: 90, close: 100, volume: 1500},
	}
	feedBarsRiskParity(t, rp, "AAA", volBarAAA)

	// ---- Bar 1 – flat bar for BBB (tiny swing) ----
	flatBarBBB := []candle{
		{high: 101, low: 99, close: 100, volume: 1500},
	}
	feedBarsRiskParity(t, rp, "BBB", flatBarBBB)

	// After processing both symbols the interval (1 bar) has elapsed,
	// triggering a rebalance.  Expect exactly one order for the strongest
	// symbol (“AAA”).
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
Test 2 – Manager switches positions when the top‑K changes.
-----------------------------------------------------------------------
1️⃣ First interval: “AAA” is volatile → position opened for AAA.
2️⃣ Second interval: “BBB” becomes volatile while AAA goes flat → the

	manager should close AAA and open a new position for BBB.
*/
func TestRiskParity_SwitchesPositionsWhenTopKChanges(t *testing.T) {
	symbols := []string{"AAA", "BBB"}
	rp, exec := buildRiskParity(t, symbols, 1, 1)

	// ---- Interval 1 – AAA volatile, BBB flat (opens AAA) ----
	feedBarsRiskParity(t, rp, "AAA", []candle{{high: 110, low: 90, close: 100, volume: 1500}})
	feedBarsRiskParity(t, rp, "BBB", []candle{{high: 101, low: 99, close: 100, volume: 1500}})

	if len(exec.Orders()) != 1 || exec.Orders()[0].Symbol != "AAA" {
		t.Fatalf("expected initial order for AAA, got %+v", exec.Orders())
	}
	aaaOrderQty := exec.Orders()[0].Qty

	// ---- Interval 2 – BBB volatile, AAA flat (should swap) ----
	feedBarsRiskParity(t, rp, "AAA", []candle{{high: 101, low: 99, close: 100, volume: 1500}})
	feedBarsRiskParity(t, rp, "BBB", []candle{{high: 120, low: 80, close: 100, volume: 1500}})

	/*
	   After the second interval we expect two additional orders:
	     1. Close AAA (SELL) – quantity must match the original AAA order.
	     2. Open BBB (BUY or SELL depending on ATSO sign).
	*/
	if len(exec.Orders()) != 3 {
		t.Fatalf("expected three total orders after swap, got %d: %+v", len(exec.Orders()), exec.Orders())
	}
	closeAAA := exec.Orders()[1]
	openBBB := exec.Orders()[2]

	if closeAAA.Symbol != "AAA" || closeAAA.Side != types.Sell {
		t.Fatalf("expected SELL order to close AAA, got %+v", closeAAA)
	}
	if closeAAA.Qty != aaaOrderQty {
		t.Fatalf("close‑AAA quantity (%f) should equal original AAA quantity (%f)", closeAAA.Qty, aaaOrderQty)
	}
	if openBBB.Symbol != "BBB" {
		t.Fatalf("expected new order for BBB, got %s", openBBB.Symbol)
	}
	if openBBB.Qty <= 0 {
		t.Fatalf("expected positive quantity for BBB entry, got %f", openBBB.Qty)
	}
}

/*
-----------------------------------------------------------------------
Test 3 – All strengths drop to zero → manager closes any open positions.
-----------------------------------------------------------------------
We first open a position for “AAA” (volatile bar).  Then we feed flat
bars for *both* symbols, which yields an ATSO magnitude of essentially
zero, making every composite strength zero.  The next rebalance should
close the existing position.
*/
func TestRiskParity_ClosesAllWhenNoStrength(t *testing.T) {
	symbols := []string{"AAA", "BBB"}
	rp, exec := buildRiskParity(t, symbols, 1, 1)

	// ---- Interval 1 – open AAA (volatile) ----
	feedBarsRiskParity(t, rp, "AAA", []candle{{high: 110, low: 90, close: 100, volume: 1500}})
	feedBarsRiskParity(t, rp, "BBB", []candle{{high: 101, low: 99, close: 100, volume: 1500}})

	if len(exec.Orders()) != 1 || exec.Orders()[0].Symbol != "AAA" {
		t.Fatalf("expected initial order for AAA, got %+v", exec.Orders())
	}
	aaaQty := exec.Orders()[0].Qty

	// ---- Interval 2 – flat bars for both symbols (strength ≈ 0) ----
	flat := []candle{{high: 101, low: 99, close: 100, volume: 1500}}
	feedBarsRiskParity(t, rp, "AAA", flat)
	feedBarsRiskParity(t, rp, "BBB", flat)

	/*
	   After the second interval the manager should issue a single
	   SELL order that closes the AAA position.
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
Test 4 – Constructor rejects invalid topK values.
-----------------------------------------------------------------------
The `NewRiskParityRotation` function should return an error when
`topK <= 0` or `topK > len(symbols)`.  This test verifies the guard.
*/
func TestRiskParity_InvalidTopK(t *testing.T) {
	symbols := []string{"AAA", "BBB"}
	mockExec := testutils.NewMockExecutor(10_000)
	mockLog := testutils.NewMockLogger()

	cfg := config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		HMAPeriod:         9,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.01,
		StopLossPct:       0.015,
		TakeProfitPct:     0.0,
		TrailingPct:       0.0,
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}

	// topK = 0 (invalid)
	if _, err := NewRiskParityRotation(symbols, cfg, mockExec, 0, 1, mockLog); err == nil {
		t.Fatalf("expected error for topK=0, got nil")
	}
	// topK > len(symbols) (invalid)
	if _, err := NewRiskParityRotation(symbols, cfg, mockExec, 3, 1, mockLog); err == nil {
		t.Fatalf("expected error for topK > len(symbols), got nil")
	}
}
