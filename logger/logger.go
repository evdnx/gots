package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is a thin wrapper around zap.SugaredLogger that provides the
// three log levels we need throughout the codebase.
type Logger interface {
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
}

// zapLogger implements Logger using a SugaredLogger internally.
type zapLogger struct {
	sugar *zap.SugaredLogger
}

func (l *zapLogger) Info(msg string, fields ...zap.Field) {
	l.sugar.Infow(msg, zapFieldsToMap(fields)...)
}
func (l *zapLogger) Warn(msg string, fields ...zap.Field) {
	l.sugar.Warnw(msg, zapFieldsToMap(fields)...)
}
func (l *zapLogger) Error(msg string, fields ...zap.Field) {
	l.sugar.Errorw(msg, zapFieldsToMap(fields)...)
}

// NewZapLogger creates a production‑ready logger (JSON encoding, level INFO).
func NewZapLogger() (Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.Encoding = "json"
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	z, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return &zapLogger{sugar: z.Sugar()}, nil
}

// Helper – converts zap.Field slice to a map for SugaredLogger.
func zapFieldsToMap(fields []zap.Field) []interface{} {
	out := make([]interface{}, 0, len(fields)*2)
	for _, f := range fields {
		out = append(out, f.Key, f.Interface)
	}
	return out
}
