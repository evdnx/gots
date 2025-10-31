package strategy

import (
	"testing"

	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/types"
)

/*
   -----------------------------------------------------------------------
   Why the extreme thresholds?
   -----------------------------------------------------------------------
   The AdaptiveBandMR strategy checks three things before opening a trade:

   1️⃣  low ≤ lowerBand   (or high ≥ upperBand for shorts)
   2️⃣  RSI ≤ RSIOversold   &&   MFI ≤ MFIOversold   &&   !hmaBull (for longs)
   3️⃣  The symmetric checks for shorts.

   By setting RSIOversold = -1e9 and RSIOverbought = +1e9 (similarly for MFI),
   conditions 2 are *always* true regardless of what the indicator suite
   actually computes.  This lets the test focus exclusively on the adaptive‑band
   logic, which is the part we want to validate deterministically.
*/

func extremeCfg() config.StrategyConfig {
	return config.StrategyConfig{
		RSIOverbought:     1e9,
		RSIOversold:       -1e9,
		MFIOverbought:     1e9,
		MFIOversold:       -1e9,
		HMAPeriod:         9,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.01, // 1 % risk per trade
		StopLossPct:       0.01, // 1 % stop‑loss (used also as band factor)
		TakeProfitPct:     0.0,  // enable per‑test
		TrailingPct:       0.0,  // enable per‑test
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}
}

func TestAdaptiveBandMR_LongEntry(t *testing.T) {
	ab, exec := buildAdaptive(t)

	// Low ≤ lowerBand (close=100, ATR≈2 → lowerBand≈97)
	high, low, close, vol := 101.0, 96.0, 100.0, 1500.0
	ab.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected exactly one BUY order, got %d", len(exec.Orders()))
	}
	o := exec.Orders()[0]
	if o.Side != types.Buy {
		t.Fatalf("expected BUY, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("quantity must be positive, got %f", o.Qty)
	}
}

func TestAdaptiveBandMR_ShortEntry(t *testing.T) {
	ab, exec := buildAdaptive(t)

	// High ≥ upperBand (close=100, ATR≈2 → upperBand≈103)
	high, low, close, vol := 104.0, 99.0, 100.0, 1500.0
	ab.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected exactly one SELL order, got %d", len(exec.Orders()))
	}
	o := exec.Orders()[0]
	if o.Side != types.Sell {
		t.Fatalf("expected SELL, got %s", o.Side)
	}
	if o.Qty <= 0 {
		t.Fatalf("quantity must be positive, got %f", o.Qty)
	}
}

func TestAdaptiveBandMR_TrailingStop(t *testing.T) {
	ab, exec := buildAdaptive(t)
	ab.Cfg.TrailingPct = 0.02 // 2 %

	// ---- entry ----
	high, low, close, vol := 101.0, 96.0, 100.0, 1500.0
	ab.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected entry order, got %d", len(exec.Orders()))
	}
	entry := exec.Orders()[0].Price // ≈100

	// ---- price crosses trailing level (entry*1.02) ----
	trail := entry * 1.02
	high, low, close, vol = 103.0, 101.0, trail+0.1, 1600.0
	ab.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected trailing‑stop close order, got %d", len(exec.Orders()))
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL to close trailing stop, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[1].Price < trail {
		t.Fatalf("trailing‑stop price %f below expected %f", exec.Orders()[1].Price, trail)
	}
}

func TestAdaptiveBandMR_TakeProfit(t *testing.T) {
	ab, exec := buildAdaptive(t)
	ab.Cfg.TakeProfitPct = 2.0 // ATR‑multiple TP

	// ---- entry ----
	high, low, close, vol := 101.0, 96.0, 100.0, 1500.0
	ab.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 1 {
		t.Fatalf("expected entry order, got %d", len(exec.Orders()))
	}
	entry := exec.Orders()[0].Price // ≈100

	// ---- price reaches TP (entry + 2*ATR).  ATSO≈2 for this series.
	tp := entry + 4.0
	high, low, close, vol = 105.0, 103.0, tp+0.5, 1600.0
	ab.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected TP close order, got %d", len(exec.Orders()))
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL to close TP, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[1].Price < tp {
		t.Fatalf("TP price %f below expected %f", exec.Orders()[1].Price, tp)
	}
}

func TestAdaptiveBandMR_OppositeClose(t *testing.T) {
	ab, exec := buildAdaptive(t)

	// ---- long entry ----
	high, low, close, vol := 101.0, 96.0, 100.0, 1500.0
	ab.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	longQty := exec.Orders()[0].Qty

	// ---- now a short signal arrives (high ≥ upperBand) ----
	high, low, close, vol = 104.0, 99.0, 100.0, 1600.0
	ab.ProcessBar(high, low, close, vol)

	if len(exec.Orders()) != 3 {
		t.Fatalf("expected three orders (entry, close‑long, short), got %d: %+v",
			len(exec.Orders()), exec.Orders())
	}
	if exec.Orders()[1].Side != types.Sell || exec.Orders()[2].Side != types.Sell {
		t.Fatalf("expected SELL orders for close‑long and new short, got %+v", exec.Orders()[1:])
	}
	if exec.Orders()[1].Qty != longQty {
		t.Fatalf("close‑long qty %f != entry qty %f", exec.Orders()[1].Qty, longQty)
	}
	if exec.Orders()[2].Qty <= 0 {
		t.Fatalf("short entry qty must be positive, got %f", exec.Orders()[2].Qty)
	}
}
