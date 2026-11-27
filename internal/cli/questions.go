package cli

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/thde/agw-decision-guardrail/internal/decision"
)

//go:embed questions.json
var defaultQuestions []byte

var (
	reloadOK    = metrics.NewCounter(`guardrail_questions_reloads_total{result="ok"}`)
	reloadError = metrics.NewCounter(`guardrail_questions_reloads_total{result="error"}`)
)

// readQuestions returns the questions file, or the built-in questions if path is empty.
func readQuestions(path string) ([]byte, error) {
	if path == "" {
		return defaultQuestions, nil
	}
	return os.ReadFile(path) //nolint:gosec,wrapcheck // Operator-supplied path; the error names it.
}

func parseQuestions(path string, b []byte) ([]decision.Question, error) {
	qs, err := decision.ParseQuestions(bytes.NewReader(b))
	if err != nil {
		if path == "" {
			path = "built-in"
		}
		return nil, fmt.Errorf("%s questions: %w", path, err)
	}
	return qs, nil
}

// watchQuestions polls path and passes changed questions to set until ctx ends.
// Polling, unlike file events, catches ConfigMap updates,
// which swap a symlinked directory.
// Invalid content is logged and the current questions kept.
func watchQuestions(ctx context.Context, path string, interval time.Duration, last []byte, set func([]decision.Question) error, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b, err := os.ReadFile(path) //nolint:gosec // Operator-supplied path.
		if err != nil {
			reloadError.Inc()
			log.ErrorContext(ctx, "reload questions", "err", err)
			continue
		}
		if bytes.Equal(b, last) {
			continue
		}
		// Also remember invalid content so it is reported once.
		last = b
		qs, err := parseQuestions(path, b)
		if err == nil {
			err = set(qs)
		}
		if err != nil {
			reloadError.Inc()
			log.ErrorContext(ctx, "reload questions", "err", err)
			continue
		}
		reloadOK.Inc()
		log.InfoContext(ctx, "questions reloaded", "path", path, "questions", len(qs))
	}
}
