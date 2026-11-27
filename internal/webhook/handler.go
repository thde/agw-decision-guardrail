package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/thde/agw-decision-guardrail/internal/logctx"
)

// maxBodyBytes bounds payloads; long prompt histories can be large.
const maxBodyBytes = 32 << 20

// Direction says whether messages come from a prompt or an LLM response.
type Direction string

const (
	// DirectionRequest marks prompt messages.
	DirectionRequest Direction = "request"
	// DirectionResponse marks LLM response messages.
	DirectionResponse Direction = "response"
)

// correlationHeaders are logged if agentgateway's webhook config forwards them.
var correlationHeaders = []string{"X-Request-Id", "Traceparent"}

// Checker evaluates messages.
type Checker interface {
	Check(ctx context.Context, dir Direction, messages []Message) (Verdict, error)
}

// Verdict is the outcome of a check.
type Verdict struct {
	// Violations names failed checks; empty means pass.
	Violations []string
	// Reason explains the outcome to operators.
	Reason string
}

// Config controls how the handler calls the Checker and answers agentgateway.
type Config struct {
	// RequestWindow is how many trailing prompt messages to check; 0 means all.
	RequestWindow int
	// RejectStatus is the HTTP status agentgateway returns on reject.
	RejectStatus int
	// Timeout bounds a single check.
	Timeout time.Duration
}

type handler struct {
	checker Checker
	cfg     Config
	log     *slog.Logger
}

// NewHandler serves POST /request and POST /response.
func NewHandler(checker Checker, cfg Config, log *slog.Logger) http.Handler {
	h := &handler{checker: checker, cfg: cfg, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /request", h.serveRequest)
	mux.HandleFunc("POST /response", h.serveResponse)
	return mux
}

func (h *handler) serveRequest(w http.ResponseWriter, r *http.Request) {
	r = withLogAttrs(r)
	var req promptRequest
	if !h.decode(w, r, &req) {
		return
	}
	msgs := req.Body.Messages
	if n := h.cfg.RequestWindow; n > 0 && len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	h.check(w, r, DirectionRequest, msgs)
}

func (h *handler) serveResponse(w http.ResponseWriter, r *http.Request) {
	r = withLogAttrs(r)
	var req responseRequest
	if !h.decode(w, r, &req) {
		return
	}
	// Choices are alternative completions, so each is checked separately.
	msgs := make([]Message, 0, len(req.Body.Choices))
	for _, c := range req.Body.Choices {
		msgs = append(msgs, c.Message)
	}
	h.check(w, r, DirectionResponse, msgs)
}

func (h *handler) check(w http.ResponseWriter, r *http.Request, dir Direction, msgs []Message) {
	if len(msgs) == 0 {
		writeJSON(w, response{Action: passAction{Reason: "no messages"}})
		return
	}

	ctx := r.Context()
	if h.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.cfg.Timeout)
		defer cancel()
	}

	v, err := h.checker.Check(ctx, dir, msgs)
	if err != nil {
		// A non-JSON body fails agentgateway's decoding, so its failureMode decides.
		h.log.ErrorContext(ctx, "check failed", "err", err)
		http.Error(w, "check failed", http.StatusBadGateway)
		return
	}

	if len(v.Violations) == 0 {
		writeJSON(w, response{Action: passAction{Reason: v.Reason}})
		return
	}
	// The call succeeds; status_code tells agentgateway to reject.
	writeJSON(w, response{Action: rejectAction{
		Body:       "Rejected by guardrail: " + strings.Join(v.Violations, ", "),
		StatusCode: h.cfg.RejectStatus,
		Reason:     v.Reason,
	}})
}

// withLogAttrs adds the path and correlation headers to logs for r.
func withLogAttrs(r *http.Request) *http.Request {
	attrs := []slog.Attr{slog.String("path", r.URL.Path)}
	for _, k := range correlationHeaders {
		if v := r.Header.Get(k); v != "" {
			attrs = append(attrs, slog.String(strings.ToLower(k), v))
		}
	}
	return r.WithContext(logctx.With(r.Context(), attrs...))
}

func (h *handler) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(v); err != nil {
		// Otherwise a changed agentgateway payload only shows as more 400s.
		h.log.WarnContext(r.Context(), "decode body", "err", err)
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
