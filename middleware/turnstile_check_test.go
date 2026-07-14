package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestTurnstileChecksEveryProtectedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldEnabled := common.TurnstileCheckEnabled
	oldSecret := common.TurnstileSecretKey
	oldClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	var calls atomic.Int32
	turnstileHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = oldEnabled
		common.TurnstileSecretKey = oldSecret
		turnstileHTTPClient = oldClient
	})

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-secret"))))
	router.GET("/protected", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("turnstile", true) // legacy marker must not bypass validation
		_ = session.Save()
		c.Next()
	}, TurnstileCheck(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/protected?turnstile=single-use-token", nil)
		router.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusNoContent, recorder.Code)
	}
	assert.Equal(t, int32(2), calls.Load())
}
