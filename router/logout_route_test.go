package router

import (
	"net/http"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestLogoutIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("logout-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/user/logout" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestTelegramLoginIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("telegram-login-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/oauth/telegram/login" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestAccessTokenGenerationIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("access-token-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/user/token" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestPasswordResetEmailIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("password-reset-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/reset_password" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestEmailVerificationIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("email-verification-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/verification" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestWeChatLoginIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("wechat-login-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/oauth/wechat" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestTokenSearchIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("token-search-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/token/search" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestWaffoPancakeCatalogIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("waffo-catalog-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/option/waffo-pancake/catalog" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestOAuthStateGenerationIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("oauth-state-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/oauth/state" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestAffiliateCodeGenerationIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("affiliate-code-route-test-secret")),
	))
	SetApiRouter(engine)

	var hasPost bool
	var hasGet bool
	for _, route := range engine.Routes() {
		if route.Path != "/api/user/aff" {
			continue
		}
		hasPost = hasPost || route.Method == http.MethodPost
		hasGet = hasGet || route.Method == http.MethodGet
	}

	assert.True(t, hasPost)
	assert.False(t, hasGet)
}

func TestMutatingChannelOperationsArePostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("channel-operation-route-test-secret")),
	))
	SetApiRouter(engine)

	paths := []string{
		"/api/channel/test",
		"/api/channel/test/:id",
		"/api/channel/update_balance",
		"/api/channel/update_balance/:id",
	}
	for _, path := range paths {
		var hasPost bool
		var hasGet bool
		for _, route := range engine.Routes() {
			if route.Path != path {
				continue
			}
			hasPost = hasPost || route.Method == http.MethodPost
			hasGet = hasGet || route.Method == http.MethodGet
		}
		assert.True(t, hasPost, path)
		assert.False(t, hasGet, path)
	}
}
