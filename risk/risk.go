package risk

import "math"

func CalcQty(equity, maxRisk, stopLossPct, price float64) float64 {
	// Dollar risk per trade
	riskAmt := equity * maxRisk
	// Stop‑loss distance in dollars
	slDist := price * stopLossPct
	if slDist == 0 {
		return 0
	}
	qty := riskAmt / slDist
	// Round to 2‑decimal places (typical for crypto/futures)
	return math.Floor(qty*100) / 100
}
