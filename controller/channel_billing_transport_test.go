package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetResponseBodyRejectsNonSuccessAndBoundsSuccessBody(t *testing.T) {
	originalLimit := constant.MaxUpstreamResponseBodyMB
	originalTimeout := common.RelayTimeout
	constant.MaxUpstreamResponseBodyMB = 1
	common.RelayTimeout = 5
	t.Cleanup(func() {
		constant.MaxUpstreamResponseBodyMB = originalLimit
		common.RelayTimeout = originalTimeout
	})
	service.InitHttpClient()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/error":
			http.Error(w, "provider details", http.StatusBadGateway)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
		}
	}))
	defer server.Close()

	channel := &model.Channel{}
	body, err := GetResponseBody(http.MethodGet, server.URL+"/error", channel, nil)
	require.ErrorContains(t, err, "status code: 502")
	assert.Nil(t, body)

	body, err = GetResponseBody(http.MethodGet, server.URL+"/oversized", channel, nil)
	require.ErrorContains(t, err, "response body exceeds")
	assert.Nil(t, body)
}
