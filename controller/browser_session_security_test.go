package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupBrowserSessionSecurityRouter(
	t *testing.T,
) (*gin.Engine, model.User) {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	user := model.User{
		Username: "browser-session-controller",
		Password: "password",
		AffCode:  "browser-session-controller-aff",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)

	router := gin.New()
	router.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("browser-session-controller-secret")),
	))
	router.GET("/login", func(c *gin.Context) {
		setupLogin(&user, c)
	})
	router.GET("/legacy", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", user.Id)
		session.Set("username", user.Username)
		session.Set("role", user.Role)
		session.Set("status", user.Status)
		session.Set("group", user.Group)
		session.Set("session_version", user.SessionVersion)
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.POST("/logout", Logout)
	router.POST("/logout-all", middleware.UserAuth(), LogoutAll)
	router.GET(
		"/protected",
		middleware.UserAuth(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	return router, user
}

func browserSessionCookie(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, responseCookie := range recorder.Result().Cookies() {
		if responseCookie.Name == "session" {
			return responseCookie
		}
	}
	require.FailNow(t, "session cookie was not issued")
	return nil
}

func performBrowserSessionRequest(
	router *gin.Engine,
	method string,
	path string,
	userID int,
	sessionCookie *http.Cookie,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	if userID > 0 {
		request.Header.Set("New-Api-User", strconv.Itoa(userID))
	}
	if sessionCookie != nil {
		request.AddCookie(sessionCookie)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestLogoutRevokesStolenCookieButKeepsOtherBrowserSession(t *testing.T) {
	router, user := setupBrowserSessionSecurityRouter(t)

	firstLogin := performBrowserSessionRequest(router, http.MethodGet, "/login", 0, nil)
	require.Equal(t, http.StatusOK, firstLogin.Code)
	firstCookie := browserSessionCookie(t, firstLogin)
	secondLogin := performBrowserSessionRequest(router, http.MethodGet, "/login", 0, nil)
	require.Equal(t, http.StatusOK, secondLogin.Code)
	secondCookie := browserSessionCookie(t, secondLogin)

	assert.Equal(t, http.StatusNoContent, performBrowserSessionRequest(
		router,
		http.MethodGet,
		"/protected",
		user.Id,
		firstCookie,
	).Code)
	assert.Equal(t, http.StatusNoContent, performBrowserSessionRequest(
		router,
		http.MethodGet,
		"/protected",
		user.Id,
		secondCookie,
	).Code)

	logout := performBrowserSessionRequest(
		router,
		http.MethodPost,
		"/logout",
		0,
		firstCookie,
	)
	assert.Equal(t, http.StatusOK, logout.Code)

	assert.Equal(t, http.StatusUnauthorized, performBrowserSessionRequest(
		router,
		http.MethodGet,
		"/protected",
		user.Id,
		firstCookie,
	).Code)
	assert.Equal(t, http.StatusNoContent, performBrowserSessionRequest(
		router,
		http.MethodGet,
		"/protected",
		user.Id,
		secondCookie,
	).Code)
}

func TestLogoutAllAndLegacyCookieAreRejected(t *testing.T) {
	router, user := setupBrowserSessionSecurityRouter(t)

	firstLogin := performBrowserSessionRequest(router, http.MethodGet, "/login", 0, nil)
	firstCookie := browserSessionCookie(t, firstLogin)
	secondLogin := performBrowserSessionRequest(router, http.MethodGet, "/login", 0, nil)
	secondCookie := browserSessionCookie(t, secondLogin)

	logoutAll := performBrowserSessionRequest(
		router,
		http.MethodPost,
		"/logout-all",
		user.Id,
		firstCookie,
	)
	assert.Equal(t, http.StatusOK, logoutAll.Code)
	assert.Equal(t, http.StatusUnauthorized, performBrowserSessionRequest(
		router,
		http.MethodGet,
		"/protected",
		user.Id,
		secondCookie,
	).Code)

	legacySeed := performBrowserSessionRequest(router, http.MethodGet, "/legacy", 0, nil)
	require.Equal(t, http.StatusNoContent, legacySeed.Code)
	legacyCookie := browserSessionCookie(t, legacySeed)
	assert.Equal(t, http.StatusUnauthorized, performBrowserSessionRequest(
		router,
		http.MethodGet,
		"/protected",
		user.Id,
		legacyCookie,
	).Code)
}
