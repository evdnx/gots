package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	OrdersSubmitted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gots_orders_submitted_total",
			Help: "Total number of orders submitted (by strategy).",
		},
		[]string{"strategy"},
	)

	PositionsOpen = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gots_positions_open",
			Help: "Current number of open positions per strategy.",
		},
		[]string{"strategy"},
	)

	EquityGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gots_equity",
			Help: "Current equity of the executor (paper or live).",
		},
	)
)

func init() {
	prometheus.MustRegister(OrdersSubmitted, PositionsOpen, EquityGauge)
}
