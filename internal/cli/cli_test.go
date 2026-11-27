package cli

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thde/agw-decision-guardrail/internal/decision"
)

func TestDefaultQuestionsAreValid(t *testing.T) {
	t.Parallel()
	b, err := readQuestions("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseQuestions("", b); err != nil {
		t.Fatal(err)
	}
}

func TestParseFlagsEnv(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string, *time.Duration) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		listen := fs.String("listen", "default", "")
		wait := fs.Duration("max-wait", time.Second, "")
		return fs, listen, wait
	}

	t.Setenv("T_LISTEN", "env")
	t.Setenv("T_MAX_WAIT", "5s")
	fs, listen, wait := newFS()
	if err := parseFlags(fs, "T_", []string{"-listen", "flag"}); err != nil {
		t.Fatal(err)
	}
	if *listen != "flag" || *wait != 5*time.Second {
		t.Errorf("got listen=%q max-wait=%v, want flag, 5s", *listen, *wait)
	}

	t.Setenv("T_MAX_WAIT", "soon")
	fs, _, _ = newFS()
	if err := parseFlags(fs, "T_", nil); err == nil {
		t.Error("invalid env value accepted")
	}
}

func TestWatchQuestions(t *testing.T) {
	t.Parallel()
	const (
		valid   = `[{"threshold": 0.5, "question": {"type": "predicate", "name": "a", "instructions": "x"}}]`
		changed = `[{"threshold": 0.5, "question": {"type": "predicate", "name": "b", "instructions": "x"}}]`
	)
	path := filepath.Join(t.TempDir(), "questions.json")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(valid)

	got := make(chan string, 10)
	set := func(qs []decision.Question) error { //nolint:unparam // Must match watchQuestions.
		got <- qs[0].Question.OfPredicate.Name.Value
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watchQuestions(ctx, path, time.Millisecond, []byte(valid), set, slog.New(slog.DiscardHandler))
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })

	// Invalid content must not reach set; the following valid change must.
	write(`[]`)
	time.Sleep(20 * time.Millisecond)
	write(changed)
	select {
	case name := <-got:
		if name != "b" {
			t.Errorf("set %q, want b", name)
		}
	case <-time.After(time.Second):
		t.Fatal("change not reloaded")
	}
	time.Sleep(20 * time.Millisecond)
	if len(got) != 0 {
		t.Errorf("unchanged file reloaded %d times", len(got))
	}
}
