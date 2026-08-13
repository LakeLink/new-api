package router

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine, assets WebAssets) {
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetVideoRouter(router)
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if common.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		common.SysLog("FRONTEND_BASE_URL is ignored on master node")
	}
	var parsedFrontendBaseURL *url.URL
	if frontendBaseUrl != "" {
		var err error
		parsedFrontendBaseURL, err = url.Parse(frontendBaseUrl)
		if err != nil ||
			(parsedFrontendBaseURL.Scheme != "http" && parsedFrontendBaseURL.Scheme != "https") ||
			parsedFrontendBaseURL.Host == "" ||
			parsedFrontendBaseURL.User != nil ||
			parsedFrontendBaseURL.RawQuery != "" ||
			parsedFrontendBaseURL.Fragment != "" {
			frontendBaseUrl = ""
			parsedFrontendBaseURL = nil
			common.SysError("FRONTEND_BASE_URL must be an absolute HTTP(S) origin without credentials, query, or fragment")
		}
	}
	if frontendBaseUrl == "" {
		SetWebRouter(router, assets)
	} else {
		parsedFrontendBaseURL.Path = strings.TrimSuffix(parsedFrontendBaseURL.Path, "/")
		router.NoRoute(func(c *gin.Context) {
			c.Set(middleware.RouteTagKey, "web")
			redirectURL := *parsedFrontendBaseURL
			redirectURL.Path += c.Request.URL.Path
			redirectURL.RawPath = ""
			redirectURL.RawQuery = c.Request.URL.RawQuery
			c.Redirect(http.StatusMovedPermanently, redirectURL.String())
		})
	}
}
