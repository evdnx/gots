package logger

import (
	"testing"

	"github.com/evdnx/gots/testutils"
	"go.uber.org/zap"
)

func TestMockLogger(t *testing.T) {
	l := testutils.NewMockLogger()
	l.Info("hello", zap.String("k", "v"))
	if got := l.LastMessage(); got != "hello" {
		t.Fatalf("expected last message 'hello', got %q", got)
	}
}
