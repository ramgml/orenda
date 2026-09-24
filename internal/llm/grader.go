package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Verdict is the graded outcome of one quiz answer. It maps onto
// course.QuizResult in the service layer; the LLM never sees this
// struct — it only emits the JSON the grader parses into it.
type Verdict struct {
	// Passed is the semantic judgement: did the student's answer
	// convey the same meaning as the expected answer.
	Passed bool `json:"passed"`
	// Feedback is a short human-readable explanation (one or two
	// sentences): what was wrong, or why the answer is accepted.
	Feedback string `json:"feedback"`
}

// graderSystemPrompt instructs the model to act as a fair grader and
// to answer with a bare JSON object. No markdown fences: parsing
// stays a strict json.Unmarshal with one tolerance pass (see
// stripFences) because small local models love wrapping JSON in
// ```json blocks.
const graderSystemPrompt = `You grade a student's free-form answer against the expected answer of a quiz question.

Judge by MEANING, not by string equality. The student may phrase it differently, give a valid synonym, a correct example, or a superset of the expected answer. Answer in the same language the question and expected answer are written in.

Reply with ONLY a JSON object, no other text, no markdown fences:
{"passed": true|false, "feedback": "<1-2 sentences: why it passes, or what is wrong / what is missing>"}`

// graderUserPrompt is the per-answer payload.
func graderUserPrompt(question, expected, answer string) string {
	return fmt.Sprintf("Question:\n%s\n\nExpected answer:\n%s\n\nStudent answer:\n%s",
		question, expected, answer)
}

// Grader grades quiz answers through an LLM client.
type Grader struct {
	client *Client
}

// NewGrader wraps a chat client as a quiz grader.
func NewGrader(c *Client) *Grader { return &Grader{client: c} }

// Grade asks the model to judge the student answer semantically and
// returns the parsed verdict. A malformed or off-contract reply is
// an error — the caller must never guess a grade.
func (g *Grader) Grade(ctx context.Context, question, expected, answer string) (Verdict, error) {
	raw, err := g.client.Complete(ctx, graderSystemPrompt, graderUserPrompt(question, expected, answer))
	if err != nil {
		return Verdict{}, fmt.Errorf("llm: grade: %w", err)
	}
	var v Verdict
	if err := json.Unmarshal([]byte(stripFences(raw)), &v); err != nil {
		return Verdict{}, fmt.Errorf("llm: grade: verdict is not valid JSON: %w", err)
	}
	return v, nil
}

// stripFences removes a markdown code fence some models wrap around
// the JSON payload despite the instruction. Only the outermost pair
// is considered; anything else is left to the JSON error path.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the first line (the fence, possibly with a language tag).
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	} else {
		return s
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
