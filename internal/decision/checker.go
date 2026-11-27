package decision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/openai/openai-go/v3"
	"github.com/thde/agw-decision-guardrail/internal/webhook"
)

var (
	questionsLoaded   = metrics.NewGauge("guardrail_questions", nil)
	questionsLoadedAt = metrics.NewGauge("guardrail_questions_loaded_timestamp_seconds", nil)
)

// Error reasons; fixed to bound label cardinality.
var (
	errRefusal   = errors.New("refusal")
	errMalformed = errors.New("malformed answer")
)

var errNoModel = errors.New("model is required")

func errorReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout"
	case errors.Is(err, errRefusal):
		return "refusal"
	case errors.Is(err, errMalformed):
		return "malformed"
	default:
		return "api"
	}
}

func probabilityMetric(question string) string {
	return fmt.Sprintf(`guardrail_probability{question=%q}`, question)
}

func violationsMetric(question string) string {
	return fmt.Sprintf(`guardrail_violations_total{question=%q}`, question)
}

// Checker implements webhook.Checker with the Decisions API.
type Checker struct {
	client *openai.Client
	model  string
	// Swapped whole so a check never mixes old and new questions.
	set atomic.Pointer[questionSet]
	log *slog.Logger
}

type questionSet struct {
	questions []Question
	params    []openai.DecisionNewParamsQuestionUnion
}

// NewChecker returns a Checker asking questions of model.
func NewChecker(client *openai.Client, model string, questions []Question, log *slog.Logger) (*Checker, error) {
	if model == "" {
		return nil, errNoModel
	}
	c := &Checker{client: client, model: model, log: log}
	if err := c.SetQuestions(questions); err != nil {
		return nil, err
	}
	return c, nil
}

// SetQuestions replaces the questions for subsequent checks.
// On invalid questions, the current ones are kept.
func (c *Checker) SetQuestions(questions []Question) error {
	if err := validate(questions); err != nil {
		return err
	}
	params := make([]openai.DecisionNewParamsQuestionUnion, len(questions))
	current := make(map[string]bool, len(questions))
	for i, q := range questions {
		params[i] = q.Question
		current[q.Question.OfPredicate.Name.Value] = true
	}
	old := c.set.Swap(&questionSet{questions: questions, params: params})
	questionsLoaded.Set(float64(len(questions)))
	questionsLoadedAt.Set(float64(time.Now().Unix()))

	// Drop series of removed questions.
	// A check still on the old set may recreate one; harmless.
	if old != nil {
		for _, q := range old.questions {
			if name := q.Question.OfPredicate.Name.Value; !current[name] {
				metrics.UnregisterMetric(probabilityMetric(name))
				metrics.UnregisterMetric(violationsMetric(name))
			}
		}
	}
	return nil
}

// Check rejects when any answer's probability reaches its question's threshold.
// Refusals and malformed answers are errors, left to agentgateway's failureMode.
func (c *Checker) Check(ctx context.Context, dir webhook.Direction, msgs []webhook.Message) (webhook.Verdict, error) {
	start := time.Now()
	v, err := c.check(ctx, msgs)
	// Includes errors, so timeouts show.
	metrics.GetOrCreateHistogram(fmt.Sprintf(`guardrail_decision_duration_seconds{direction=%q}`, dir)).UpdateDuration(start)
	var result string
	switch {
	case err != nil:
		result = fmt.Sprintf(`result="error",reason=%q`, errorReason(err))
	case len(v.Violations) > 0:
		result = `result="reject"`
	default:
		result = `result="pass"`
	}
	metrics.GetOrCreateCounter(fmt.Sprintf(`guardrail_decisions_total{direction=%q,%s}`, dir, result)).Inc()
	return v, err
}

func (c *Checker) check(ctx context.Context, msgs []webhook.Message) (webhook.Verdict, error) {
	set := c.set.Load()
	start := time.Now()
	d, err := c.client.Decisions.New(ctx, openai.DecisionNewParams{
		Model:     c.model,
		Input:     openai.DecisionNewParamsInputUnion{OfDecisionNewsInputArray: input(msgs)},
		Questions: set.params,
	})
	if err != nil {
		return webhook.Verdict{}, fmt.Errorf("create decision: %w", err)
	}
	latency := time.Since(start)
	if len(d.Answers) != len(set.questions) {
		return webhook.Verdict{}, fmt.Errorf("%w: got %d answers for %d questions", errMalformed, len(d.Answers), len(set.questions))
	}

	var (
		violations []string
		scores     = make([]string, 0, len(d.Answers))
	)
	for i, a := range d.Answers {
		q := set.questions[i]
		name := q.Question.OfPredicate.Name.Value
		switch v := a.AsAny().(type) {
		case openai.DecisionAnswerPredicate:
			// Answers follow question order per docs; the name check guards against drift.
			if v.Name != name {
				return webhook.Verdict{}, fmt.Errorf("%w: answer %d is for %q, want %q", errMalformed, i, v.Name, name)
			}
			metrics.GetOrCreateHistogram(probabilityMetric(name)).Update(v.Probability)
			if v.Probability >= q.Threshold {
				violations = append(violations, name)
				metrics.GetOrCreateCounter(violationsMetric(name)).Inc()
			}
			scores = append(scores, fmt.Sprintf("%s=%.2f", name, v.Probability))
		case openai.DecisionAnswerRefusal:
			return webhook.Verdict{}, fmt.Errorf("%w: model %s refused question %q", errRefusal, d.Model, name)
		default:
			return webhook.Verdict{}, fmt.Errorf("%w: unexpected answer type %q for question %q", errMalformed, a.Type, name)
		}
	}

	reason := strings.Join(scores, " ")
	c.log.InfoContext(ctx, "decision",
		"model", d.Model, "latency", latency, "scores", reason, "violations", violations)
	return webhook.Verdict{Violations: violations, Reason: reason}, nil
}

// input keeps roles structured, not "role: content" text, so content cannot
// impersonate another role.
func input(msgs []webhook.Message) []openai.DecisionNewParamsInputArrayItemUnion {
	items := make([]openai.DecisionNewParamsInputArrayItemUnion, len(msgs))
	for i, m := range msgs {
		text := openai.String(m.Content)
		switch m.Role {
		case "assistant":
			items[i].OfDecisionInputAssistantMessage = &openai.DecisionNewParamsInputArrayItemDecisionInputAssistantMessage{
				Content: openai.DecisionNewParamsInputArrayItemDecisionInputAssistantMessageContentUnion{OfString: text},
			}
		case "system":
			items[i].OfDecisionInputSystemMessage = &openai.DecisionNewParamsInputArrayItemDecisionInputSystemMessage{
				Content: openai.DecisionNewParamsInputArrayItemDecisionInputSystemMessageContentUnion{OfString: text},
			}
		case "developer":
			items[i].OfDecisionInputDeveloperMessage = &openai.DecisionNewParamsInputArrayItemDecisionInputDeveloperMessage{
				Content: openai.DecisionNewParamsInputArrayItemDecisionInputDeveloperMessageContentUnion{OfString: text},
			}
		default:
			// user, tool and unknown roles carry untrusted content.
			items[i].OfDecisionInputUserMessage = &openai.DecisionNewParamsInputArrayItemDecisionInputUserMessage{
				Content: openai.DecisionNewParamsInputArrayItemDecisionInputUserMessageContentUnion{OfString: text},
			}
		}
	}
	return items
}
