// Package cli implements the agw-decision-guardrail command.
package cli

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/VictoriaMetrics/metrics"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/thde/agw-decision-guardrail/internal/decision"
	"github.com/thde/agw-decision-guardrail/internal/logctx"
	"github.com/thde/agw-decision-guardrail/internal/webhook"
)

var errNegativeRetries = errors.New("-max-retries must not be negative")

// Main runs the command with args, excluding the program name, and returns
// the exit code.
func Main(args []string) int {
	log := slog.New(logctx.NewHandler(slog.NewTextHandler(os.Stderr, nil)))
	// Also covers dependencies logging via package log or slog's default.
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// A second signal kills instead of waiting for shutdown.
	context.AfterFunc(ctx, stop)

	if err := run(ctx, args, log); err != nil {
		log.Error("exit", "err", err)
		return 1
	}
	return 0
}

// run serves until ctx is canceled, then shuts down gracefully.
func run(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var (
		listen        = fs.String("listen", "127.0.0.1:8000", "webhook listen address")
		baseURL       = fs.String("base-url", cmp.Or(os.Getenv("OPENAI_BASE_URL"), "http://127.0.0.1:4000/v1"), "Decisions API base URL, usually agentgateway")
		model         = fs.String("model", "guardrail", "Decisions model or agentgateway virtual model")
		questionsPath = fs.String("questions", "", "questions JSON file (default: built-in jailbreak, harmful, secrets)")
		reload        = fs.Duration("questions-reload", 10*time.Second, "interval to check the questions file for changes; 0 disables")
		window        = fs.Int("request-window", 1, "trailing prompt messages to check; 0 checks all")
		rejectStatus  = fs.Int("reject-status", http.StatusForbidden, "HTTP status returned to clients on reject")
		timeout       = fs.Duration("timeout", 8*time.Second, "check timeout; keep below agentgateway's webhook timeout")
		maxRetries    = fs.Int("max-retries", 2, "Decisions API retries; all attempts share -timeout")
	)
	if err := parseFlags(fs, envPrefix, args); err != nil {
		return err
	}
	if *maxRetries < 0 {
		return errNegativeRetries
	}

	raw, err := readQuestions(*questionsPath)
	if err != nil {
		return err
	}
	questions, err := parseQuestions(*questionsPath, raw)
	if err != nil {
		return err
	}

	// Best effort; Set logs its own failures.
	// Refresh follows pod resizes.
	_, _ = memlimit.Set(memlimit.WithLogger(log), memlimit.WithRefreshInterval(ctx, time.Minute))

	client := openai.NewClient(
		option.WithBaseURL(*baseURL),
		option.WithMaxRetries(*maxRetries),
		option.WithHTTPClient(&http.Client{Transport: instrumentedTransport{http.DefaultTransport}}),
	)
	checker, err := decision.NewChecker(&client, *model, questions, log)
	if err != nil {
		return fmt.Errorf("new checker: %w", err)
	}
	if *questionsPath != "" && *reload > 0 {
		go watchQuestions(ctx, *questionsPath, *reload, raw, checker.SetQuestions, log)
	}
	h := webhook.NewHandler(checker, webhook.Config{
		RequestWindow: *window,
		RejectStatus:  *rejectStatus,
		Timeout:       *timeout,
	}, log)

	mux := http.NewServeMux()
	mux.Handle("/", instrumentHandler(h))
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		metrics.WritePrometheus(w, true)
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
		// A signal must not abort in-flight checks Shutdown waits for.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", *listen, "model", *model, "questions", len(questions))

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down; signal again to force")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

const (
	name      = "agw-decision-guardrail"
	envPrefix = "GUARDRAIL_"
)

// parseFlags parses args.
// Unset flags fall back to prefix + upper-snake name,
// e.g. -base-url → GUARDRAIL_BASE_URL.
func parseFlags(fs *flag.FlagSet, prefix string, args []string) error {
	env := func(name string) string {
		return prefix + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	}
	fs.VisitAll(func(f *flag.Flag) { f.Usage += " (env " + env(f.Name) + ")" })
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var errs []error
	fs.VisitAll(func(f *flag.Flag) {
		v, ok := os.LookupEnv(env(f.Name))
		if !ok || set[f.Name] {
			return
		}
		if err := f.Value.Set(v); err != nil {
			errs = append(errs, fmt.Errorf("%s=%q: %w", env(f.Name), v, err))
		}
	})
	return errors.Join(errs...)
}
