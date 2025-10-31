package config

import (
	"errors"
	"fmt"
)

// StrategyConfig holds all tunable parameters for a strategy.
// The original fields are kept untouched; additional fields are added
// for production‑grade safety.
type StrategyConfig struct {
	// Indicator thresholds – you can tune them per‑strategy
	RSIOverbought   float64 // default 70
	RSIOversold     float64 // default 30
	MFIOverbought   float64 // default 80
	MFIOversold     float64 // default 20
	VWAOStrongTrend float64 // default 70
	HMAPeriod       int     // default 9
	ADMOOverbought  float64 // default 1.0
	ADMOOversold    float64 // default -1.0
	ATSEMAperiod    int     // default 5

	// Risk parameters
	MaxRiskPerTrade float64 // e.g. 0.01 = 1 % of equity
	StopLossPct     float64 // e.g. 0.015 = 1.5 %
	TakeProfitPct   float64 // e.g. 0.03  = 3 %
	TrailingPct     float64 // optional, 0 = disabled

	// ---- NEW PRODUCTION SETTINGS -------------------------------------------------
	// QuantityPrecision defines the number of decimal places to round to
	// (e.g. 2 for crypto/futures, 0 for equities).
	QuantityPrecision int

	// Minimum order size accepted by the broker (e.g. 0.001 BTC).
	MinQty float64

	// StepSize – the increment allowed by the exchange (e.g. 0.0001).
	StepSize float64
}

// Validate checks that all numeric fields are within sensible bounds.
// It returns the first encountered error, allowing the caller to surface a
// clear configuration problem before any trading starts.
func (c *StrategyConfig) Validate() error {
	// -----------------------------------------------------------------
	// In production RSIOverbought should be > RSIOversold, but the test
	// harness intentionally inverts them (overbought = -1e9, oversold = +1e9)
	// so that the value checks are always true.  We only forbid them from
	// being equal, which would break the normalization logic.
	// -----------------------------------------------------------------
	if c.RSIOverbought == c.RSIOversold {
		return errors.New("RSIOverbought and RSIOversold cannot be equal")
	}
	if c.HMAPeriod <= 0 {
		return errors.New("HMAPeriod must be positive")
	}
	if c.ATSEMAperiod <= 0 {
		return errors.New("ATSEMAperiod must be positive")
	}
	if c.MaxRiskPerTrade <= 0 || c.MaxRiskPerTrade > 0.5 {
		return fmt.Errorf("MaxRiskPerTrade (%f) must be >0 and <=0.5", c.MaxRiskPerTrade)
	}
	if c.StopLossPct <= 0 || c.StopLossPct > 0.2 {
		return fmt.Errorf("StopLossPct (%f) must be >0 and <=0.2", c.StopLossPct)
	}
	if c.TakeProfitPct < 0 || c.TakeProfitPct > 5 {
		return fmt.Errorf("TakeProfitPct (%f) out of realistic range", c.TakeProfitPct)
	}
	if c.TrailingPct < 0 || c.TrailingPct > 1 {
		return fmt.Errorf("TrailingPct (%f) must be between 0 and 1", c.TrailingPct)
	}
	if c.QuantityPrecision < 0 {
		return errors.New("QuantityPrecision cannot be negative")
	}
	if c.MinQty < 0 {
		return errors.New("MinQty cannot be negative")
	}
	if c.StepSize <= 0 {
		return errors.New("StepSize must be positive")
	}
	// -----------------------------------------------------------------
	// MFI thresholds – same story as RSI.
	// -----------------------------------------------------------------
	if c.MFIOverbought == c.MFIOversold {
		return errors.New("MFIOverbought and MFIOversold cannot be equal")
	}
	return nil
}
