package logctx

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(NewHandler(slog.NewTextHandler(&buf, nil)))

	parent := With(context.Background(), slog.String("a", "1"))
	child := With(parent, slog.String("b", "2"))
	_ = With(parent, slog.String("c", "3")) // must not leak into child

	log.InfoContext(child, "msg")
	if got := buf.String(); !strings.Contains(got, "a=1 b=2") || strings.Contains(got, "c=3") {
		t.Errorf("log = %q", got)
	}
	buf.Reset()
	log.Info("plain")
	if got := buf.String(); strings.Contains(got, "a=1") {
		t.Errorf("log without context = %q", got)
	}
}
