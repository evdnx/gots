package strategy

import (
	"math"

	"github.com/evdnx/goti"
	"github.com/evdnx/gots/config"
	"github.com/evdnx/gots/executor"
	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/metrics"
	"github.com/evdnx/gots/risk"
	"github.com/evdnx/gots/types"
)

// BaseStrategy bundles the common dependencies and helpers.
type BaseStrategy struct {
	Exec   executor.Executor
	Log    logger.Logger
	Cfg    config.StrategyConfig
	Suite  *goti.IndicatorSuite
	Symbol string
	prices *priceBuffer
}

// NewBaseStrategy creates the indicator suite (using the supplied factory)
// and validates the config.  All concrete strategies should call this from
// their own constructors.
func NewBaseStrategy(symbol string, cfg config.StrategyConfig,
	exec executor.Executor,
	suiteFactory func() (*goti.IndicatorSuite, error),
	log logger.Logger) (*BaseStrategy, error) {

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	suite, err := suiteFactory()
	if err != nil {
		return nil, err
	}
	return &BaseStrategy{
		Exec:   exec,
		Log:    log,
		Cfg:    cfg,
		Suite:  suite,
		Symbol: symbol,
		prices: newPriceBuffer(64),
	}, nil
}

// submitOrder is a thin wrapper that records metrics and logs.
func (b *BaseStrategy) submitOrder(o types.Order, ctx string) error {
	err := b.Exec.Submit(o)
	if err != nil {
		b.Log.Error("order_submit_failed",
			logger.String("symbol", o.Symbol),
			logger.String("side", string(o.Side)),
			logger.Float64("qty", o.Qty),
			logger.Err(err),
		)
		return err
	}
	b.Log.Info("order_submitted",
		logger.String("symbol", o.Symbol),
		logger.String("side", string(o.Side)),
		logger.Float64("qty", o.Qty),
		logger.Float64("price", o.Price),
		logger.String("ctx", ctx),
	)
	metrics.OrdersSubmitted.WithLabelValues(ctx).Inc()
	return nil
}

// calcQty delegates to the risk package using the stored config.
func (b *BaseStrategy) calcQty(price float64) float64 {
	return risk.CalcQty(b.Exec.Equity(), b.Cfg.MaxRiskPerTrade, b.Cfg.StopLossPct, price, b.Cfg)
}

// trailingStopLevel returns the price level at which a trailing stop would fire.
func (b *BaseStrategy) trailingStopLevel(entryAvg, side float64) float64 {
	if side > 0 { // long
		return entryAvg * (1 + b.Cfg.TrailingPct)
	}
	// short
	return entryAvg * (1 - b.Cfg.TrailingPct)
}

// applyTrailingStop checks the current price against the trailing level and
// closes the position if needed.
func (b *BaseStrategy) applyTrailingStop(currentPrice float64) {
	if b.Cfg.TrailingPct <= 0 {
		return
	}
	qty, avg := b.Exec.Position(b.Symbol)
	if qty == 0 {
		return
	}
	level := b.trailingStopLevel(avg, math.Copysign(1, qty))
	if (qty > 0 && currentPrice >= level) || (qty < 0 && currentPrice <= level) {
		b.closePosition(currentPrice, "trailing_stop")
	}
}

// closePosition flattens the current position at the supplied price.
func (b *BaseStrategy) closePosition(price float64, ctx string) {
	qty, _ := b.Exec.Position(b.Symbol)
	if qty == 0 {
		return
	}
	side := types.Sell
	if qty < 0 {
		side = types.Buy
	}
	o := types.Order{
		Symbol:  b.Symbol,
		Side:    side,
		Qty:     math.Abs(qty),
		Price:   price,
		Comment: ctx,
	}
	_ = b.submitOrder(o, ctx)
}

func (b *BaseStrategy) recordPrice(close float64) {
	if b.prices != nil {
		b.prices.Add(close)
	}
}

func (b *BaseStrategy) bullishFallback() bool {
	if b.prices == nil || b.prices.Len() < 3 {
		return false
	}
	return b.prices.Trend() > 0 && b.prices.Slope() > 0
}

func (b *BaseStrategy) bearishFallback() bool {
	if b.prices == nil || b.prices.Len() < 3 {
		return false
	}
	return b.prices.Trend() < 0 && b.prices.Slope() < 0
}

func (b *BaseStrategy) swingVolatility() float64 {
	if b.prices == nil {
		return 0
	}
	return b.prices.Volatility()
}

func (b *BaseStrategy) lastPriceChange() float64 {
	if b.prices == nil || b.prices.Len() < 2 {
		return 0
	}
	return b.prices.Last() - b.prices.Prev()
}

func (b *BaseStrategy) sanitizeVolatility(raw, price float64) float64 {
	if price <= 0 {
		price = 1
	}
	if math.IsNaN(raw) || math.IsInf(raw, 0) || raw <= 0 || raw > price*0.1 {
		fallback := b.swingVolatility()
		if fallback <= 0 {
			fallback = price * 0.02
		}
		return math.Max(fallback, 0.0001)
	}
	return raw
}

func (b *BaseStrategy) hasHistory(n int) bool {
	if n <= 0 {
		return true
	}
	if b.prices == nil {
		return false
	}
	return b.prices.Len() >= n
}

func (b *BaseStrategy) bullishReversal() bool {
	if b.prices == nil {
		return false
	}
	vals := b.prices.Values()
	n := len(vals)
	if n < 4 {
		return false
	}
	if !(vals[n-1] > vals[n-2] && vals[n-2] > vals[n-3]) {
		return false
	}
	window := 6
	if window > n {
		window = n
	}
	segment := vals[n-window:]
	drop := false
	for i := 1; i < len(segment)-2; i++ {
		if segment[i] < segment[i-1] {
			drop = true
			break
		}
	}
	if !drop {
		return false
	}
	minVal := segment[0]
	minIdx := 0
	for i, v := range segment {
		if v < minVal {
			minVal = v
			minIdx = i
		}
	}
	return minIdx <= window-3 && minVal < segment[len(segment)-1]
}

func (b *BaseStrategy) bearishReversal() bool {
	if b.prices == nil {
		return false
	}
	vals := b.prices.Values()
	n := len(vals)
	if n < 4 {
		return false
	}
	if !(vals[n-1] < vals[n-2] && vals[n-2] < vals[n-3]) {
		return false
	}
	window := 6
	if window > n {
		window = n
	}
	segment := vals[n-window:]
	rally := false
	for i := 1; i < len(segment)-2; i++ {
		if segment[i] > segment[i-1] {
			rally = true
			break
		}
	}
	if !rally {
		return false
	}
	maxVal := segment[0]
	maxIdx := 0
	for i, v := range segment {
		if v > maxVal {
			maxVal = v
			maxIdx = i
		}
	}
	return maxIdx <= window-3 && maxVal > segment[len(segment)-1]
}
