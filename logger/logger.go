package logger

import (
	"github.com/evdnx/golog"
)

// Field re-exports golog.Field so callers do not depend on the concrete logger.
type Field = golog.Field

// Logger defines the minimal logging surface used across the codebase.
type Logger interface {
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
}

// gologLogger adapts golog.Logger to the local Logger interface.
type gologLogger struct {
	inner *golog.Logger
}

func (l *gologLogger) Info(msg string, fields ...Field) {
	l.inner.Info(msg, fields...)
}

func (l *gologLogger) Warn(msg string, fields ...Field) {
	l.inner.Warn(msg, fields...)
}

func (l *gologLogger) Error(msg string, fields ...Field) {
	l.inner.Error(msg, fields...)
}

// NewZapLogger creates a production‑ready logger wired to golog with JSON output.
func NewZapLogger() (Logger, error) {
	l, err := golog.NewLogger(
		golog.WithStdOutProvider(golog.JSONEncoder),
		golog.WithLevel(golog.InfoLevel),
	)
	if err != nil {
		return nil, err
	}
	return &gologLogger{inner: l}, nil
}

// Structured field helpers re-exported for convenience.
var (
	String   = golog.String
	Int      = golog.Int
	Float64  = golog.Float64
	Any      = golog.Any
	Err      = golog.Err
	Duration = golog.Duration
)
