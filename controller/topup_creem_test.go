package controller

import (
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreemAmountCentsUsesDecimalRounding(t *testing.T) {
	cents, err := creemAmountCents(10.005)
	require.NoError(t, err)
	assert.Equal(t, 1001, cents)

	cents, err = creemAmountCents(0.01)
	require.NoError(t, err)
	assert.Equal(t, 1, cents)
}

func TestParseCreemCheckoutResponseRejectsStatusAndOversizedBody(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
		want string
	}{
		{
			name: "non-success",
			resp: &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader(`{"checkout_url":"https://unexpected"}`)),
			},
			want: "http status 502",
		},
		{
			name: "oversized success",
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", (1<<20)+1))),
			},
			want: "response body exceeds",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkout, err := parseCreemCheckoutResponse(test.resp)
			require.ErrorContains(t, err, test.want)
			assert.Nil(t, checkout)
		})
	}
}

func TestParseCreemCheckoutResponseAcceptsValidSuccess(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Body: io.NopCloser(strings.NewReader(
			`{"checkout_url":"https://checkout.creem.example/session","id":"checkout-1"}`,
		)),
	}

	checkout, err := parseCreemCheckoutResponse(resp)

	require.NoError(t, err)
	require.NotNil(t, checkout)
	assert.Equal(t, "https://checkout.creem.example/session", checkout.CheckoutUrl)
	assert.Equal(t, "checkout-1", checkout.Id)
}

func TestCreemAmountCentsRejectsUnsafePrices(t *testing.T) {
	for _, price := range []float64{0, -1, math.NaN(), math.Inf(1), float64(math.MaxInt32)/100 + 1} {
		_, err := creemAmountCents(price)
		require.Error(t, err)
	}
}
