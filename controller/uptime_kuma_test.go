package controller

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

type uptimeKumaRejectUnexpectedRequestTransport struct {
	called bool
}

func (t *uptimeKumaRejectUnexpectedRequestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.called = true
	return nil, errors.New("unexpected outbound request")
}

func TestFetchGroupDataRejectsUnsafeLegacyURLBeforeDispatch(t *testing.T) {
	transport := &uptimeKumaRejectUnexpectedRequestTransport{}
	result := fetchGroupData(
		context.Background(),
		&http.Client{Transport: transport},
		map[string]interface{}{
			"url":          "https://user:password@status.example.com",
			"slug":         "public",
			"categoryName": "Status",
		},
	)

	assert.False(t, transport.called)
	assert.Equal(t, "Status", result.CategoryName)
	assert.Empty(t, result.Monitors)
}
