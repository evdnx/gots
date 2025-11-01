package testutils

import "github.com/evdnx/gots/logger"

// logEntry captures a single log invocation for inspection in tests.
type logEntry struct {
	level  string
	msg    string
	fields []logger.Field
}

// MockLogger implements the Logger interface but stores entries in-memory.
type MockLogger struct {
	entries []logEntry
}

// NewMockLogger returns a logger that records everything.
func NewMockLogger() *MockLogger { return &MockLogger{} }

func (l *MockLogger) record(level, msg string, fields ...logger.Field) {
	copiedFields := append([]logger.Field(nil), fields...)
	l.entries = append(l.entries, logEntry{level: level, msg: msg, fields: copiedFields})
}

func (l *MockLogger) Info(msg string, fields ...logger.Field) {
	l.record("info", msg, fields...)
}
func (l *MockLogger) Warn(msg string, fields ...logger.Field) {
	l.record("warn", msg, fields...)
}
func (l *MockLogger) Error(msg string, fields ...logger.Field) {
	l.record("error", msg, fields...)
}

// LastMessage returns the message associated with the most recent log entry.
func (l *MockLogger) LastMessage() string {
	if len(l.entries) == 0 {
		return ""
	}
	return l.entries[len(l.entries)-1].msg
}
