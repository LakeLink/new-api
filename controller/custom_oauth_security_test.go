package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchCustomOAuthDiscoveryRejectsPrivateTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = nil
	fetchSetting.ApplyIPFilterForDomain = true

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/custom/discovery",
		bytes.NewBufferString(`{"well_known_url":"http://127.0.0.1/.well-known/openid-configuration"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request

	FetchCustomOAuthDiscovery(c)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "安全策略拒绝")
}

func TestFetchCustomOAuthDiscoveryRejectsURLCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/custom/discovery",
		bytes.NewBufferString(`{"well_known_url":"https://user:secret@example.com/.well-known/openid-configuration"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request

	FetchCustomOAuthDiscovery(c)

	assert.NotContains(t, recorder.Body.String(), "secret")
	assert.Contains(t, recorder.Body.String(), "Discovery URL")
}
