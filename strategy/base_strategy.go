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
	"go.uber.org/zap"
)

// BaseStrategy bundles the common dependencies and helpers.
type BaseStrategy struct {
	Exec   executor.Executor
	Log    logger.Logger
	Cfg    config.StrategyConfig
	Suite  *goti.IndicatorSuite
	Symbol string
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
	}, nil
}

// submitOrder is a thin wrapper that records metrics and logs.
func (b *BaseStrategy) submitOrder(o types.Order, ctx string) error {
	err := b.Exec.Submit(o)
	if err != nil {
		b.Log.Error("order_submit_failed", zap.String("symbol", o.Symbol), zap.String("side", string(o.Side)), zap.Float64("qty", o.Qty), zap.Error(err))
		return err
	}
	b.Log.Info("order_submitted", zap.String("symbol", o.Symbol), zap.String("side", string(o.Side)), zap.Float64("qty", o.Qty), zap.Float64("price", o.Price), zap.String("ctx", ctx))
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
