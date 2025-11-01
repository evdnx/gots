# gots

`gots` is a Go toolkit for prototyping algorithmic trading strategies together with the plumbing required to execute, monitor, and risk‑manage them. The repository ships with a collection of fully unit‑tested strategies, reusable configuration helpers, and lightweight mocks that make it easy to simulate fills or integrate with a real broker.

## Features

- **Strategy library** – mean reversion, breakout momentum, adaptive band, divergence swing, trend composite, volatility‑scaled positions, hybrid trend/mean reversion, multi‑timeframe confirmation, risk parity rotation, and a news/event driven overlay. Each strategy embeds shared tooling (position sizing, trailing stops, take‑profit logic, logging, metrics, risk controls).
- **Backtest friendly** – deterministic mocks (`testutils`) capture submitted orders and position changes, allowing end‑to‑end scenario tests without external dependencies.
- **Risk module** – exchange‑aware quantity calculation with step size, precision, and minimum quantity enforcement.
- **Config validation** – safeguards catch invalid thresholds or impossible risk parameters before a strategy is instantiated.
- **Metrics/logging** – adapters using `go.uber.org/zap` and Prometheus compatible collectors (see `metrics` package).

## Project layout

```
config/      Strategy configuration structs and validation
executor/    Execution interfaces (real + mock) and helpers
logger/      Logging adapters
metrics/     Prometheus collectors and instrumentation helpers
risk/        Position sizing and risk management utilities
strategy/    Concrete trading strategies and tests
testutils/   In‑memory mocks for deterministic testing
types/       Shared domain types (order side, order struct, etc.)
```

## Getting started

### Requirements

- Go 1.25 or newer (see `go.mod`)

### Install dependencies

All dependencies are Go modules, so a standard `go mod download` is sufficient:

```bash
go mod download
```

### Run the test suite

The project is covered by unit tests across strategies and core packages. Run everything with:

```bash
go test ./...
```

To focus on the strategy behaviours:

```bash
go test ./strategy
```

### Using a strategy in your own code

Each strategy exposes a constructor returning a type that implements a simple `ProcessBar` contract:

```go
exec := executor.NewPaper()
log  := logger.NewZap()
cfg  := config.StrategyConfig{/* ... thresholds & risk ... */}

strat, err := strategy.NewMeanReversion("BTCUSDT", cfg, exec, log)
if err != nil {
    log.Error("failed to init strategy", zap.Error(err))
    return
}

for _, bar := range historicalBars {
    strat.ProcessBar(bar.High, bar.Low, bar.Close, bar.Volume)
}
```

Orders are submitted through the `executor.Executor` interface, so plugging a live broker or an exchange simulator only requires implementing that interface.

## Development workflow

1. Run `go fmt ./...` before committing (the repo uses standard formatting).
2. Keep the test suite green – the scenarios in `strategy/*_test.go` act as regression tests for the trading logic.
3. When adding a new strategy, embed `*strategy.BaseStrategy` and reuse the shared helpers for position sizing, trailing stops, take‑profit logic, and logging/metrics.

## Contributing

Issues and pull requests are welcome. Please accompany behavioural changes with tests – the mocks in `testutils` make it straightforward to simulate fills and verify the resulting order flow.

## License

MIT-0
