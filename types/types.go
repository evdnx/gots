package types

type Side string

const (
	Buy  Side = "BUY"
	Sell Side = "SELL"
)

type Order struct {
	Symbol string
	Side   Side
	Qty    float64
	Price  float64 // limit price; 0 = market
	// meta
	Comment string
}
