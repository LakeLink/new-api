package passkey

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSignedPasskeySessionCookieCannotReplayChallenge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AuthenticationToken{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })

	router := gin.New()
	router.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("passkey-session-replay-secret")),
	))
	router.GET("/begin", func(c *gin.Context) {
		require.NoError(t, SaveSessionData(c, LoginSessionKey, &webauthn.SessionData{
			Challenge: "one-time-passkey-challenge",
			Expires:   time.Now().Add(time.Minute),
		}))
		c.Status(http.StatusNoContent)
	})
	router.POST("/finish", func(c *gin.Context) {
		if _, err := PopSessionData(c, LoginSessionKey); err != nil {
			c.Status(http.StatusConflict)
			return
		}
		c.Status(http.StatusNoContent)
	})

	beginRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		beginRecorder,
		httptest.NewRequest(http.MethodGet, "/begin", nil),
	)
	require.Equal(t, http.StatusNoContent, beginRecorder.Code)
	var originalCookie *http.Cookie
	for _, responseCookie := range beginRecorder.Result().Cookies() {
		if responseCookie.Name == "session" {
			originalCookie = responseCookie
			break
		}
	}
	require.NotNil(t, originalCookie)

	firstFinish := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodPost, "/finish", nil)
	firstRequest.AddCookie(originalCookie)
	router.ServeHTTP(firstFinish, firstRequest)
	assert.Equal(t, http.StatusNoContent, firstFinish.Code)

	replayFinish := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "/finish", nil)
	replayRequest.AddCookie(originalCookie)
	router.ServeHTTP(replayFinish, replayRequest)
	assert.Equal(t, http.StatusConflict, replayFinish.Code)
}
