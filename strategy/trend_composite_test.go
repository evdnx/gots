package strategy

import (
	"testing"

	"github.com/evdnx/gots/types"
)

func TestTrendComposite_LongEntry(t *testing.T) {
	tc, exec := buildTrendComposite(t)

	// 15 upward bars → bullish crossovers for HMA, AMDO, ATSO.
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, tc, bars)

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

func TestTrendComposite_ShortEntry(t *testing.T) {
	tc, exec := buildTrendComposite(t)

	// 15 downward bars → bearish crossovers for all three indicators.
	var bars []candle
	for i := 1; i <= 15; i++ {
		price := 115.0 - float64(i)
		bars = append(bars, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, tc, bars)

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

func TestTrendComposite_TrailingStop(t *testing.T) {
	tc, exec := buildTrendComposite(t)
	tc.Cfg.TrailingPct = 0.02 // 2 %

	// ---- entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, tc, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entry := exec.Orders()[0].Price

	// ---- price crosses trailing level ----
	trail := entry * 1.02
	tc.ProcessBar(trail+0.5, trail-0.5, trail+0.1, 1200)

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected trailing‑stop close order, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL to close trailing stop, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[1].Price < trail {
		t.Fatalf("trailing‑stop price %f below expected %f", exec.Orders()[1].Price, trail)
	}
}

func TestTrendComposite_TakeProfit(t *testing.T) {
	tc, exec := buildTrendComposite(t)
	tc.Cfg.TakeProfitPct = 2.0 // ATR‑multiple TP

	// ---- entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, tc, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	entry := exec.Orders()[0].Price

	// TP = entry + 2*ATR (ATSO≈2)
	tp := entry + 4.0
	tc.ProcessBar(tp+0.5, tp-0.5, tp+0.1, 1300)

	if len(exec.Orders()) != 2 {
		t.Fatalf("expected TP close order, got %d (orders: %+v)", len(exec.Orders()), exec.Orders())
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("expected SELL to close TP, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[1].Price < tp {
		t.Fatalf("TP price %f below expected %f", exec.Orders()[1].Price, tp)
	}
}

func TestTrendComposite_OppositeSideFlip(t *testing.T) {
	tc, exec := buildTrendComposite(t)

	// ---- long entry (upward ramp) ----
	var up []candle
	for i := 1; i <= 15; i++ {
		price := 100.0 + float64(i)
		up = append(up, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, tc, up)

	if len(exec.Orders()) != 1 || exec.Orders()[0].Side != types.Buy {
		t.Fatalf("expected initial BUY order, got %+v", exec.Orders())
	}
	longQty := exec.Orders()[0].Qty

	// ---- now a bearish crossover (downward ramp) ----
	var down []candle
	for i := 1; i <= 15; i++ {
		price := 115.0 - float64(i)
		down = append(down, candle{
			high:   price + 0.5,
			low:    price - 0.5,
			close:  price,
			volume: 1000,
		})
	}
	feedBars(t, tc, down)

	if len(exec.Orders()) != 3 {
		t.Fatalf("expected three orders (long, close, short), got %d: %+v", len(exec.Orders()), exec.Orders())
	}
	if exec.Orders()[1].Side != types.Sell {
		t.Fatalf("order 1 should be SELL to close long, got %s", exec.Orders()[1].Side)
	}
	if exec.Orders()[2].Side != types.Sell {
		t.Fatalf("order 2 should be SELL (short entry), got %s", exec.Orders()[2].Side)
	}
	if exec.Orders()[1].Qty != longQty {
		t.Fatalf("close‑long qty %f != entry qty %f", exec.Orders()[1].Qty, longQty)
	}
	if exec.Orders()[2].Qty <= 0 {
		t.Fatalf("short entry qty must be positive, got %f", exec.Orders()[2].Qty)
	}
}
