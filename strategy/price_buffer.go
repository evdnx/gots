package strategy

import "math"

// priceBuffer keeps a rolling window of recent closing prices and exposes
// lightweight statistics (trend direction, slope, volatility) that the
// strategies can use without relying on heavyweight indicator state.
type priceBuffer struct {
	max int
	buf []float64
}

func newPriceBuffer(max int) *priceBuffer {
	if max <= 0 {
		max = 16
	}
	return &priceBuffer{max: max}
}

func (p *priceBuffer) Add(v float64) {
	p.buf = append(p.buf, v)
	if len(p.buf) > p.max {
		p.buf = p.buf[len(p.buf)-p.max:]
	}
}

func (p *priceBuffer) Values() []float64 {
	out := make([]float64, len(p.buf))
	copy(out, p.buf)
	return out
}

func (p *priceBuffer) Len() int {
	return len(p.buf)
}

func (p *priceBuffer) Last() float64 {
	if len(p.buf) == 0 {
		return 0
	}
	return p.buf[len(p.buf)-1]
}

func (p *priceBuffer) Prev() float64 {
	if len(p.buf) < 2 {
		return 0
	}
	return p.buf[len(p.buf)-2]
}

func (p *priceBuffer) Trend() int {
	if len(p.buf) < 2 {
		return 0
	}
	lookback := 6
	if lookback >= len(p.buf) {
		lookback = len(p.buf) - 1
	}
	start := len(p.buf) - lookback - 1
	if start < 0 {
		start = 0
	}
	score := 0
	for i := start + 1; i < len(p.buf); i++ {
		switch {
		case p.buf[i] > p.buf[i-1]:
			score++
		case p.buf[i] < p.buf[i-1]:
			score--
		}
	}
	threshold := lookback / 3
	if threshold < 2 {
		threshold = 2
	}
	if score >= threshold {
		return 1
	}
	if score <= -threshold {
		return -1
	}
	return 0
}

func (p *priceBuffer) Slope() float64 {
	n := len(p.buf)
	if n < 2 {
		return 0
	}
	lookback := 8
	if lookback >= n {
		lookback = n - 1
	}
	start := n - lookback - 1
	if start < 0 {
		start = 0
	}
	sumX, sumY := 0.0, 0.0
	sumXY, sumXX := 0.0, 0.0
	idx := 0
	for i := start; i < n; i++ {
		x := float64(idx)
		y := p.buf[i]
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
		idx++
	}
	count := float64(idx)
	den := count*sumXX - sumX*sumX
	if den == 0 {
		return 0
	}
	return (count*sumXY - sumX*sumY) / den
}

func (p *priceBuffer) Volatility() float64 {
	n := len(p.buf)
	if n < 2 {
		return 0
	}
	lookback := 8
	if lookback >= n {
		lookback = n - 1
	}
	start := n - lookback - 1
	if start < 0 {
		start = 0
	}
	diffSum := 0.0
	count := 0
	for i := start + 1; i < n; i++ {
		diffSum += math.Abs(p.buf[i] - p.buf[i-1])
		count++
	}
	if count == 0 {
		return 0
	}
	return diffSum / float64(count)
}
