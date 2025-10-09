package gots

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
}
