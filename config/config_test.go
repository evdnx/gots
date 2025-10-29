package config

import "testing"

func TestValidateSuccess(t *testing.T) {
	cfg := StrategyConfig{
		RSIOverbought:     70,
		RSIOversold:       30,
		MFIOverbought:     80,
		MFIOversold:       20,
		VWAOStrongTrend:   70,
		HMAPeriod:         9,
		ADMOOverbought:    1.0,
		ADMOOversold:      -1.0,
		ATSEMAperiod:      5,
		MaxRiskPerTrade:   0.02,
		StopLossPct:       0.015,
		TakeProfitPct:     0.03,
		TrailingPct:       0.0,
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateFailsOnBadRisk(t *testing.T) {
	cfg := StrategyConfig{
		MaxRiskPerTrade:   -0.01, // invalid
		StopLossPct:       0.015,
		TakeProfitPct:     0.03,
		QuantityPrecision: 2,
		MinQty:            0.001,
		StepSize:          0.0001,
		RSIOverbought:     70,
		RSIOversold:       30,
		MFIOverbought:     80,
		MFIOversold:       20,
		HMAPeriod:         9,
		ATSEMAperiod:      5,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for negative MaxRiskPerTrade")
	}
}
