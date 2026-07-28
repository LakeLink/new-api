package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrontendRedirectKeepsConfiguredOriginForNetworkPathRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = originalMaster })
	t.Setenv("FRONTEND_BASE_URL", "https://frontend.example/app")

	engine := gin.New()
	SetRouter(engine, ThemeAssets{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"http://gateway.example//attacker.example/path?next=%2Fdashboard",
		nil,
	)
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusMovedPermanently, recorder.Code)
	assert.Equal(
		t,
		"https://frontend.example/app//attacker.example/path?next=%2Fdashboard",
		recorder.Header().Get("Location"),
	)
}
