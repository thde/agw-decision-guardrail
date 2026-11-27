// Package decision checks messages with the OpenAI Decisions API, which
// agentgateway can route to Jev.
package decision

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/openai/openai-go/v3"
)

// maxQuestions is the Decisions API limit.
const maxQuestions = 400

var errInvalid = errors.New("invalid questions")

// Question is a Decisions API question and the probability at which it rejects.
type Question struct {
	Threshold float64                               `json:"threshold"`
	Question  openai.DecisionNewParamsQuestionUnion `json:"question"`
}

// ParseQuestions reads a JSON array of questions.
//
//	[{"threshold": 0.5, "question": {"type": "predicate", "name": "jailbreak", "instructions": "..."}}]
func ParseQuestions(r io.Reader) ([]Question, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var qs []Question
	if err := dec.Decode(&qs); err != nil {
		return nil, fmt.Errorf("decode questions: %w", err)
	}
	if err := validate(qs); err != nil {
		return nil, err
	}
	return qs, nil
}

func validate(qs []Question) error {
	if len(qs) == 0 || len(qs) > maxQuestions {
		return fmt.Errorf("%w: need 1 to %d questions, got %d", errInvalid, maxQuestions, len(qs))
	}
	seen := make(map[string]bool, len(qs))
	for i, q := range qs {
		// Only predicates have an obvious reject rule: probability >= threshold.
		p := q.Question.OfPredicate
		if p == nil {
			return fmt.Errorf("%w: question %d: only predicate questions are supported", errInvalid, i)
		}
		name := p.Name.Value
		if name == "" {
			return fmt.Errorf("%w: question %d: name is required", errInvalid, i)
		}
		if seen[name] {
			return fmt.Errorf("%w: question %d: duplicate name %q", errInvalid, i, name)
		}
		seen[name] = true
		if p.Instructions.OfString.Value == "" && len(p.Instructions.OfDecisionQuestionInputPartArray) == 0 {
			return fmt.Errorf("%w: question %q: instructions are required", errInvalid, name)
		}
		if q.Threshold <= 0 || q.Threshold > 1 {
			return fmt.Errorf("%w: question %q: threshold must be in (0, 1], got %v", errInvalid, name, q.Threshold)
		}
	}
	return nil
}
