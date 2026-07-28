package oauth

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeOAuthJSONResponseBoundsProviderPayload(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", int(maxOAuthResponseBodyBytes)+1))),
	}

	var payload map[string]any
	err := decodeOAuthJSONResponse(response, &payload)

	require.Error(t, err)
	assert.ErrorContains(t, err, "exceeds")
}

func TestRequireOAuthSuccessStatusRejectsErrorResponse(t *testing.T) {
	err := requireOAuthSuccessStatus(&http.Response{StatusCode: http.StatusBadGateway})

	require.Error(t, err)
	assert.ErrorContains(t, err, "502")
}

func TestDecodeOAuthJSONResponseAcceptsSmallPayload(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"access_token":"token"}`)),
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}

	require.NoError(t, decodeOAuthJSONResponse(response, &payload))
	assert.Equal(t, "token", payload.AccessToken)
}
