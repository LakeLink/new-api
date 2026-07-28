package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthStateIsConsumedAfterSuccessfulValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.OAuthState{}))
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("oauth-state-test-secret"))))
	router.GET("/seed", func(c *gin.Context) {
		session := sessions.Default(c)
		require.NoError(t, model.RotateOAuthState(
			"",
			"one-time-state",
			time.Now().Add(time.Minute).Unix(),
		))
		session.Set("oauth_state", "one-time-state")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/consume", func(c *gin.Context) {
		valid, err := consumeOAuthState(sessions.Default(c), c.Query("state"))
		require.NoError(t, err)
		c.JSON(http.StatusOK, gin.H{"valid": valid})
	})

	seedRecorder := httptest.NewRecorder()
	router.ServeHTTP(seedRecorder, httptest.NewRequest(http.MethodGet, "/seed", nil))
	require.Equal(t, http.StatusNoContent, seedRecorder.Code)
	require.NotEmpty(t, seedRecorder.Result().Cookies())

	firstRequest := httptest.NewRequest(http.MethodGet, "/consume?state=one-time-state", nil)
	for _, sessionCookie := range seedRecorder.Result().Cookies() {
		firstRequest.AddCookie(sessionCookie)
	}
	firstRecorder := httptest.NewRecorder()
	router.ServeHTTP(firstRecorder, firstRequest)
	assert.JSONEq(t, `{"valid":true}`, firstRecorder.Body.String())
	// Replay the original signed cookie rather than the cooperative rotated
	// cookie returned by the first callback. The authoritative DB claim, not
	// browser cookie replacement, must reject it.
	replayRequest := httptest.NewRequest(http.MethodGet, "/consume?state=one-time-state", nil)
	for _, sessionCookie := range seedRecorder.Result().Cookies() {
		replayRequest.AddCookie(sessionCookie)
	}
	replayRecorder := httptest.NewRecorder()
	router.ServeHTTP(replayRecorder, replayRequest)
	assert.JSONEq(t, `{"valid":false}`, replayRecorder.Body.String())
}

func TestOAuthStateRequestClearsStaleAffiliateCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.OAuthState{}))
	router := gin.New()
	router.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("oauth-affiliate-state-test-secret")),
	))
	router.POST("/state", GenerateOAuthCode)
	router.GET("/affiliate", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"affiliate": sessions.Default(c).Get("aff"),
		})
	})

	firstRecorder := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(
		http.MethodPost,
		"/state",
		bytes.NewBufferString(`{"aff":"Affiliate123"}`),
	)
	firstRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(firstRecorder, firstRequest)
	require.Equal(t, http.StatusOK, firstRecorder.Code)
	var firstResponse struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(
		firstRecorder.Body.Bytes(),
		&firstResponse,
	))
	require.True(t, firstResponse.Success)
	require.NotEmpty(t, firstRecorder.Result().Cookies())

	secondRecorder := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(
		http.MethodPost,
		"/state",
		bytes.NewBufferString(`{"aff":""}`),
	)
	secondRequest.Header.Set("Content-Type", "application/json")
	for _, sessionCookie := range firstRecorder.Result().Cookies() {
		secondRequest.AddCookie(sessionCookie)
	}
	router.ServeHTTP(secondRecorder, secondRequest)
	require.Equal(t, http.StatusOK, secondRecorder.Code)
	require.NotEmpty(t, secondRecorder.Result().Cookies())

	inspectRecorder := httptest.NewRecorder()
	inspectRequest := httptest.NewRequest(http.MethodGet, "/affiliate", nil)
	for _, sessionCookie := range secondRecorder.Result().Cookies() {
		inspectRequest.AddCookie(sessionCookie)
	}
	router.ServeHTTP(inspectRecorder, inspectRequest)
	assert.JSONEq(t, `{"affiliate":null}`, inspectRecorder.Body.String())
}
