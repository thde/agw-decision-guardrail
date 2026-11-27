package webhook

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/thde/agw-decision-guardrail/internal/logctx"
)

var (
	errUnexpectedCall = errors.New("must not be called")
	errBoom           = errors.New("boom")
)

type ctxChecker struct{ log *slog.Logger }

func (c ctxChecker) Check(ctx context.Context, _ Direction, _ []Message) (Verdict, error) {
	c.log.InfoContext(ctx, "checked")
	return Verdict{}, nil
}

func TestCorrelationLogged(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(logctx.NewHandler(slog.NewTextHandler(&buf, nil)))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/request", strings.NewReader(prompt))
	req.Header.Set("X-Request-ID", "abc")
	NewHandler(ctxChecker{log}, Config{}, log).ServeHTTP(httptest.NewRecorder(), req)
	if got := buf.String(); !strings.Contains(got, "path=/request x-request-id=abc") {
		t.Errorf("log = %q", got)
	}
}

type fakeChecker struct {
	verdict Verdict
	err     error
	got     []Message
	dir     Direction
}

func (f *fakeChecker) Check(_ context.Context, dir Direction, msgs []Message) (Verdict, error) {
	f.got = msgs
	f.dir = dir
	return f.verdict, f.err
}

func serve(t *testing.T, c Checker, cfg Config, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(c, cfg, slog.New(slog.DiscardHandler))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body)))
	return rec
}

const prompt = `{"body":{"messages":[
	{"role":"system","content":"s"},
	{"role":"user","content":"u1"},
	{"role":"user","content":"u2"}]}}`

func TestRequestPass(t *testing.T) {
	t.Parallel()
	c := &fakeChecker{verdict: Verdict{Reason: "ok"}}
	rec := serve(t, c, Config{RequestWindow: 1, RejectStatus: 403}, http.MethodPost, "/request", prompt)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got, want := rec.Body.String(), `{"action":{"reason":"ok"}}`+"\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if want := []Message{{Role: "user", Content: "u2"}}; !reflect.DeepEqual(c.got, want) {
		t.Errorf("checked %v, want %v", c.got, want)
	}
	if c.dir != DirectionRequest {
		t.Errorf("direction = %q", c.dir)
	}
}

func TestRequestWindow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		window int
		want   int
	}{{0, 3}, {2, 2}, {10, 3}} {
		c := &fakeChecker{}
		serve(t, c, Config{RequestWindow: tc.window}, http.MethodPost, "/request", prompt)
		if len(c.got) != tc.want {
			t.Errorf("window %d: checked %d messages, want %d", tc.window, len(c.got), tc.want)
		}
	}
}

func TestResponseReject(t *testing.T) {
	t.Parallel()
	c := &fakeChecker{verdict: Verdict{Violations: []string{"jailbreak", "secrets"}, Reason: "r"}}
	body := `{"body":{"choices":[
		{"message":{"role":"assistant","content":"a"}},
		{"message":{"role":"assistant","content":"b"}}]}}`
	rec := serve(t, c, Config{RejectStatus: 451}, http.MethodPost, "/response", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	want := `{"action":{"body":"Rejected by guardrail: jailbreak, secrets","status_code":451,"reason":"r"}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if len(c.got) != 2 {
		t.Errorf("checked %d choices, want 2", len(c.got))
	}
	if c.dir != DirectionResponse {
		t.Errorf("direction = %q", c.dir)
	}
}

func TestEmptyPassesWithoutCheck(t *testing.T) {
	t.Parallel()
	c := &fakeChecker{err: errUnexpectedCall}
	rec := serve(t, c, Config{}, http.MethodPost, "/request", `{"body":{"messages":[]}}`)
	if rec.Code != http.StatusOK || c.got != nil {
		t.Errorf("status = %d, checked = %v", rec.Code, c.got)
	}
}

func TestCheckErrorIsNotJSON(t *testing.T) {
	t.Parallel()
	c := &fakeChecker{err: errBoom}
	rec := serve(t, c, Config{}, http.MethodPost, "/request", prompt)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if strings.HasPrefix(rec.Body.String(), "{") {
		t.Errorf("error body %q looks like JSON; agentgateway could parse it as an action", rec.Body.String())
	}
}

func TestBadInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/request", "{", http.StatusBadRequest},
		{http.MethodGet, "/request", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/other", prompt, http.StatusNotFound},
	} {
		rec := serve(t, &fakeChecker{}, Config{}, tc.method, tc.path, tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s %s: status = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}
