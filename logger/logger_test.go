package logger_test

import (
	"testing"

	"github.com/evdnx/gots/logger"
	"github.com/evdnx/gots/testutils"
)

func TestMockLogger(t *testing.T) {
	l := testutils.NewMockLogger()
	l.Info("hello", logger.String("k", "v"))
	if got := l.LastMessage(); got != "hello" {
		t.Fatalf("expected last message 'hello', got %q", got)
	}
}
