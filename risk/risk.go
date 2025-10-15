package risk

import (
	"math"

	"github.com/evdnx/gots/config"
)

// CalcQty returns a quantity that respects the config's precision, min‑qty
// and step‑size.  It also caps the quantity to the nearest valid step.
func CalcQty(equity, maxRisk, stopLossPct, price float64, cfg config.StrategyConfig) float64 {
	// Dollar risk per trade
	riskAmt := equity * maxRisk
	// Stop‑loss distance in dollars
	slDist := price * stopLossPct
	if slDist <= 0 {
		return 0
	}
	rawQty := riskAmt / slDist

	// Apply step‑size rounding
	if cfg.StepSize > 0 {
		rawQty = math.Floor(rawQty/cfg.StepSize) * cfg.StepSize
	}
	// Apply precision rounding (e.g. 2 dp)
	if cfg.QuantityPrecision > 0 {
		factor := math.Pow10(cfg.QuantityPrecision)
		rawQty = math.Floor(rawQty*factor) / factor
	}
	// Enforce minimum quantity
	if rawQty < cfg.MinQty {
		return 0
	}
	return rawQty
}
