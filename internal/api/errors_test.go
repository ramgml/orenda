package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// unknownErr is not mapped by writeError; it hits the 500 default branch.
type unknownErr struct{}

func (unknownErr) Error() string { return "something broke" }

// NOTE: these tests mutate the shared package-level apiLogger and must NOT
// run in parallel — sequential execution is required for correctness.

func TestSetAPILogger_NilDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		SetAPILogger(nil)
	})
}

func TestWriteError_NilLoggerReturns500(t *testing.T) {
	SetAPILogger(nil) // nop logger installed

	w := httptest.NewRecorder()
	writeError(w, unknownErr{})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "internal")
}

func TestWriteError_NonNilLoggerLogs(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	l := zap.New(core)

	SetAPILogger(l)
	t.Cleanup(func() { SetAPILogger(nil) })

	w := httptest.NewRecorder()
	writeError(w, unknownErr{})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.Len(t, logs.All(), 1, "expected exactly one log entry")
	assert.Contains(t, logs.All()[0].Message, "api internal error")
}
