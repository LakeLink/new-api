package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogSearchAndExportRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	for _, route := range []string{
		http.MethodGet + " /api/log/expr/schema",
		http.MethodGet + " /api/log/export",
		http.MethodGet + " /api/log/self/export",
	} {
		_, ok := routes[route]
		require.True(t, ok, "missing route %s", route)
	}

	_, hasPatternSearch := routes[http.MethodGet+" /api/log/"]
	assert.True(t, hasPatternSearch)
}
