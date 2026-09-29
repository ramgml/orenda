package service_test

// AnswerQuiz — semantic grading through the LLM grader seam. The old
// exact-kind string compare is gone; every answer is graded by
// meaning and the verdict alone decides the result. Covers:
//
//   - exact quizzes: verdict → result, feedback carried through,
//     no review task
//   - open quizzes: verdict + best-effort tutor review task
//   - error paths: no grader wired, grader failure, quiz not found
import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/llm"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// stubGrader records Grade calls and replays a canned verdict.
type stubGrader struct {
	verdict llm.Verdict
	err     error
	calls   []gradeCall
}

type gradeCall struct{ question, expected, answer string }

func (s *stubGrader) Grade(_ context.Context, question, expected, answer string) (llm.Verdict, error) {
	s.calls = append(s.calls, gradeCall{question, expected, answer})
	if s.err != nil {
		return llm.Verdict{}, s.err
	}
	return s.verdict, nil
}

func TestAnswerQuiz_ExactGradedByVerdict(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Lesson"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, QuestionMD: "Capital?", ExpectedMD: "Paris"}
	svc := coursesvc.New(repo).WithGrader(&stubGrader{verdict: llm.Verdict{Passed: true, Feedback: "correct"}})

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "paris, france"})
	require.NoError(t, err)
	assert.True(t, res.Correct, "the grader verdict decides, not string equality")
	assert.Equal(t, "correct", res.FeedbackMD)
	assert.Empty(t, res.ReviewTaskID, "exact quizzes never spawn review tasks")
}

func TestAnswerQuiz_FailedVerdictCarriesFeedback(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, QuestionMD: "Q?", ExpectedMD: "42"}
	svc := coursesvc.New(repo).WithGrader(&stubGrader{verdict: llm.Verdict{Passed: false, Feedback: "43 is not 42"}})

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "43"})
	require.NoError(t, err)
	assert.False(t, res.Correct)
	assert.Equal(t, "43 is not 42", res.FeedbackMD)
}

func TestAnswerQuiz_GraderReceivesQuestionExpectedAnswer(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, QuestionMD: "2+2?", ExpectedMD: "4"}
	grader := &stubGrader{verdict: llm.Verdict{Passed: true}}
	svc := coursesvc.New(repo).WithGrader(grader)

	_, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "four"})
	require.NoError(t, err)
	require.Len(t, grader.calls, 1)
	assert.Equal(t, "2+2?", grader.calls[0].question)
	assert.Equal(t, "4", grader.calls[0].expected)
	assert.Equal(t, "four", grader.calls[0].answer)
}

func TestAnswerQuiz_NoGrader_Errors(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, ExpectedMD: "x"}
	svc := coursesvc.New(repo) // no grader wired

	_, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "x"})
	assert.ErrorIs(t, err, coursesvc.ErrGraderNotWired)
}

func TestAnswerQuiz_GraderErrorBubblesUp(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, ExpectedMD: "x"}
	svc := coursesvc.New(repo).WithGrader(&stubGrader{err: errors.New("api down")})

	_, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api down")
}

func TestAnswerQuiz_OpenVerdictPlusReviewTask(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Lesson"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizOpen, QuestionMD: "Why?"}
	repo.moduleOwners["m1"] = "u-owner"
	tasks := &stubTaskCreator{}
	svc := coursesvc.New(repo).WithTaskCreator(tasks).WithGrader(&stubGrader{verdict: llm.Verdict{Passed: true, Feedback: "solid reasoning"}})

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "my essay"})
	require.NoError(t, err)
	assert.True(t, res.Correct, "the grader verdict decides, not the quiz kind")
	assert.Equal(t, "solid reasoning", res.FeedbackMD)
	assert.Equal(t, "rev-task-q1", res.ReviewTaskID, "open quizzes still spawn the tutor review task")
	assert.Equal(t, []string{"my essay"}, tasks.revCalls)
}

func TestAnswerQuiz_OpenReviewTaskFailureKeepsVerdict(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizOpen, QuestionMD: "Why?"}
	repo.moduleOwners["m1"] = "u-owner"
	tasks := &stubTaskCreator{revErr: errors.New("backend down")}
	svc := coursesvc.New(repo).WithTaskCreator(tasks).WithGrader(&stubGrader{verdict: llm.Verdict{Passed: true, Feedback: "good"}})

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "x"})
	require.NoError(t, err, "review-task failure must not discard the LLM verdict")
	assert.True(t, res.Correct)
	assert.Equal(t, "good", res.FeedbackMD)
	assert.Empty(t, res.ReviewTaskID)
}

func TestAnswerQuiz_OpenWithoutTaskCreator_StillGrades(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizOpen}
	svc := coursesvc.New(repo).WithGrader(&stubGrader{verdict: llm.Verdict{Passed: false, Feedback: "vague"}}) // no task creator

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "x"})
	require.NoError(t, err)
	assert.False(t, res.Correct)
	assert.Equal(t, "vague", res.FeedbackMD)
}

func TestAnswerQuiz_NotFound(t *testing.T) {
	repo := newStubRepo()
	svc := coursesvc.New(repo).WithGrader(&stubGrader{})
	_, err := svc.AnswerQuiz(context.Background(), "missing", course.QuizAnswer{})
	assert.ErrorIs(t, err, coursesvc.ErrNotFound)
}
