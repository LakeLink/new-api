package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeferredTaskResponseWriterPublishesOnlyAfterCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writer := newDeferredTaskResponseWriter(ctx.Writer)

	writer.Header().Set("X-Task-ID", "task_accepted")
	writer.WriteHeader(http.StatusAccepted)
	_, err := writer.WriteString(`{"id":"task_accepted"}`)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Empty(t, recorder.Body.String())
	assert.Empty(t, recorder.Header().Get("X-Task-ID"))
	assert.Equal(t, http.StatusAccepted, writer.Status())
	assert.True(t, writer.Written())

	require.NoError(t, writer.Commit())
	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.JSONEq(t, `{"id":"task_accepted"}`, recorder.Body.String())
	assert.Equal(t, "task_accepted", recorder.Header().Get("X-Task-ID"))
}

func TestDeferredTaskResponseWriterCanBeDiscarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	original := ctx.Writer
	writer := newDeferredTaskResponseWriter(original)
	ctx.Writer = writer

	_, err := writer.WriteString(`{"id":"untracked"}`)
	require.NoError(t, err)
	ctx.Writer = original
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "persist failed"})

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "untracked")
	assert.JSONEq(t, `{"error":"persist failed"}`, recorder.Body.String())
}

func TestDeferredTaskResponseWriterResetDiscardsFailedRetryState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	recorder.Header().Set("X-Gateway", "new-api")
	ctx, _ := gin.CreateTestContext(recorder)
	writer := newDeferredTaskResponseWriter(ctx.Writer)

	writer.Header().Set("X-Upstream-Attempt", "failed")
	writer.WriteHeader(http.StatusBadGateway)
	_, err := writer.WriteString(`{"error":"temporary upstream failure"}`)
	require.NoError(t, err)

	writer.Reset()
	assert.Equal(t, http.StatusOK, writer.Status())
	assert.Equal(t, -1, writer.Size())
	assert.False(t, writer.Written())
	assert.Equal(t, "new-api", writer.Header().Get("X-Gateway"))
	assert.Empty(t, writer.Header().Get("X-Upstream-Attempt"))

	writer.Header().Set("X-Upstream-Attempt", "successful")
	writer.WriteHeader(http.StatusAccepted)
	_, err = writer.WriteString(`{"id":"persisted-task"}`)
	require.NoError(t, err)
	require.NoError(t, writer.Commit())

	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.JSONEq(t, `{"id":"persisted-task"}`, recorder.Body.String())
	assert.Equal(t, "successful", recorder.Header().Get("X-Upstream-Attempt"))
	assert.NotContains(t, recorder.Body.String(), "temporary upstream failure")
}

func TestTaskRelayDoesNotRepeatAcceptedSubmissionAfterResponseError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	const configuredRetries = 3

	doRequestCalls := 0
	for retriesLeft := configuredRetries; ; retriesLeft-- {
		doRequestCalls++
		taskErr := &dto.TaskError{
			StatusCode: http.StatusInternalServerError,
			SkipRetry:  true,
		}
		if !shouldRetryTaskRelay(ctx, 1, taskErr, retriesLeft) {
			break
		}
	}

	assert.Equal(t, 1, doRequestCalls)
}
