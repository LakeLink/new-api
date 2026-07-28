package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePaymentCallbackURLFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantURL string
	}{
		{
			name:    "HTTPS callback",
			rawURL:  "https://billing.example.com/api/payment/notify",
			wantURL: "https://billing.example.com/api/payment/notify",
		},
		{
			name:   "malformed URL",
			rawURL: "https://billing.example.com/%",
		},
		{
			name:   "relative URL",
			rawURL: "/api/payment/notify",
		},
		{
			name:   "missing hostname",
			rawURL: "https://:443/api/payment/notify",
		},
		{
			name:   "non HTTP scheme",
			rawURL: "javascript:alert(1)",
		},
		{
			name:   "credentials",
			rawURL: "https://user:password@billing.example.com/api/payment/notify",
		},
		{
			name:   "query injection",
			rawURL: "https://billing.example.com/api/payment/notify?next=evil",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := parsePaymentCallbackURL(test.rawURL)
			if test.wantURL == "" {
				require.Error(t, err)
				assert.Nil(t, parsed)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, parsed)
			assert.Equal(t, test.wantURL, parsed.String())
		})
	}
}
