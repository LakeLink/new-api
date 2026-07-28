package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New(
		"Authorization: Bearer credential-value\n" +
			"request to https://user:password@api.example.com/v1?key=query-secret failed",
	)
}

func TestDoUpstreamRequestSanitizesTransportError(t *testing.T) {
	request, err := http.NewRequest(
		http.MethodGet,
		"https://api.example.com/v1",
		nil,
	)
	require.NoError(t, err)

	_, err = DoUpstreamRequest(
		&http.Client{Transport: failingRoundTripper{}},
		request,
	)
	require.Error(t, err)
	message := err.Error()
	assert.NotContains(t, message, "credential-value")
	assert.NotContains(t, message, "query-secret")
	assert.NotContains(t, message, "password")
	assert.NotContains(t, message, "\n")
}

func TestSanitizeNetworkErrorPreservesCancellationIdentity(t *testing.T) {
	original := fmt.Errorf(
		"request to https://api.example.com/v1?key=query-secret failed: %w",
		context.DeadlineExceeded,
	)

	sanitized := SanitizeNetworkError(original)

	require.Error(t, sanitized)
	assert.ErrorIs(t, sanitized, context.DeadlineExceeded)
	assert.NotContains(t, sanitized.Error(), "query-secret")
}

func TestDoUpstreamRequestRejectsNilInputs(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	require.NoError(t, err)

	_, err = DoUpstreamRequest(nil, request)
	require.ErrorContains(t, err, "client is nil")

	_, err = DoUpstreamRequest(&http.Client{}, nil)
	require.ErrorContains(t, err, "request is nil")
}
