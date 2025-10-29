package testutils

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// MockLogger implements the Logger interface but writes to an in‑memory buffer.
type MockLogger struct {
	entries []zapcore.Entry
	fields  []zapcore.Field
}

// NewMockLogger returns a logger that records everything.
func NewMockLogger() *MockLogger { return &MockLogger{} }

func (l *MockLogger) record(level zapcore.Level, msg string, fields ...zap.Field) {
	l.entries = append(l.entries, zapcore.Entry{Level: level, Message: msg})
	l.fields = append(l.fields, fields...)
}

func (l *MockLogger) Info(msg string, fields ...zap.Field) {
	l.record(zapcore.InfoLevel, msg, fields...)
}
func (l *MockLogger) Warn(msg string, fields ...zap.Field) {
	l.record(zapcore.WarnLevel, msg, fields...)
}
func (l *MockLogger) Error(msg string, fields ...zap.Field) {
	l.record(zapcore.ErrorLevel, msg, fields...)
}

// Helper to inspect the last logged message.
func (l *MockLogger) LastMessage() string {
	if len(l.entries) == 0 {
		return ""
	}
	return l.entries[len(l.entries)-1].Message
}
