package service

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type codexOAuthRoundTripper func(*http.Request) (*http.Response, error)

func (fn codexOAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestRefreshCodexOAuthTokenRejectsOverflowingExpiry(t *testing.T) {
	client := &http.Client{Transport: codexOAuthRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"access_token":"access","refresh_token":"refresh","expires_in":%d}`,
				int64(math.MaxInt64),
			))),
			Request: req,
		}, nil
	})}

	result, err := refreshCodexOAuthToken(
		context.Background(),
		client,
		"https://auth.example.invalid/oauth/token",
		"client",
		"refresh",
	)

	require.ErrorContains(t, err, "invalid expires_in")
	assert.Nil(t, result)
}
