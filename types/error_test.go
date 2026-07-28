package types

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOpenAIErrorHandlesNilCause(t *testing.T) {
	var apiErr *NewAPIError
	require.NotPanics(t, func() {
		apiErr = NewOpenAIError(nil, ErrorCodeBadResponse, http.StatusBadGateway)
	})

	require.NotNil(t, apiErr)
	assert.Equal(t, ErrorCodeBadResponse, apiErr.GetErrorCode())
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.ErrorContains(t, apiErr, string(ErrorCodeBadResponse))
}
