package decision

import (
	"strings"
	"testing"
)

func TestParseQuestions(t *testing.T) {
	t.Parallel()
	qs, err := ParseQuestions(strings.NewReader(`[
		{"threshold": 0.5, "question": {"type": "predicate", "name": "jailbreak", "instructions": "a?"}},
		{"threshold": 1, "question": {"type": "predicate", "name": "secrets", "instructions": "b?"}}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 {
		t.Fatalf("got %d questions", len(qs))
	}
	p := qs[0].Question.OfPredicate
	if p == nil || p.Name.Value != "jailbreak" || p.Instructions.OfString.Value != "a?" || qs[0].Threshold != 0.5 {
		t.Errorf("question 0 = %+v", qs[0])
	}
}

func TestParseQuestionsInvalid(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"empty":         `[]`,
		"not json":      `{`,
		"unknown field": `[{"threshold": 0.5, "extra": 1, "question": {"type": "predicate", "name": "a", "instructions": "x"}}]`,
		"choice":        `[{"threshold": 0.5, "question": {"type": "choice", "name": "a", "instructions": "x", "choices": [{"value": "y"}, {"value": "n"}]}}]`,
		"no name":       `[{"threshold": 0.5, "question": {"type": "predicate", "instructions": "x"}}]`,
		"no instr":      `[{"threshold": 0.5, "question": {"type": "predicate", "name": "a"}}]`,
		"duplicate":     `[{"threshold": 0.5, "question": {"type": "predicate", "name": "a", "instructions": "x"}}, {"threshold": 0.5, "question": {"type": "predicate", "name": "a", "instructions": "y"}}]`,
		"zero":          `[{"threshold": 0, "question": {"type": "predicate", "name": "a", "instructions": "x"}}]`,
		"above one":     `[{"threshold": 1.1, "question": {"type": "predicate", "name": "a", "instructions": "x"}}]`,
	} {
		if _, err := ParseQuestions(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
