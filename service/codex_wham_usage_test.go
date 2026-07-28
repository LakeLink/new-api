package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type codexWhamRoundTripper func(*http.Request) (*http.Response, error)

func (f codexWhamRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

func TestCodexWhamRequestsRejectOversizedResponsesAndCloseBodies(t *testing.T) {
	type requestFunc func(context.Context, *http.Client, string, string, string) (int, []byte, error)
	requests := map[string]requestFunc{
		"usage":   FetchCodexWhamUsage,
		"credits": FetchCodexWhamRateLimitResetCredits,
		"consume": ConsumeCodexWhamRateLimitResetCredit,
	}

	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			body := &closeTrackingBody{
				Reader: strings.NewReader(strings.Repeat("x", int(maxCodexWhamResponseBytes)+1)),
			}
			client := &http.Client{Transport: codexWhamRoundTripper(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       body,
					Header:     make(http.Header),
					Request:    req,
				}, nil
			})}

			status, responseBody, err := request(
				context.Background(),
				client,
				"https://chatgpt.example",
				"access-token",
				"account-id",
			)

			require.ErrorContains(t, err, "response body exceeds")
			assert.Equal(t, http.StatusOK, status)
			assert.Nil(t, responseBody)
			assert.True(t, body.closed)
		})
	}
}
