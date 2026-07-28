package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecureVerificationRejectsFutureExpiredAndIncompleteState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().Unix()
	tests := []struct {
		name       string
		verifiedAt int64
		method     string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "valid",
			verifiedAt: now,
			method:     "password",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "implausibly future",
			verifiedAt: now + secureVerificationFutureSkew + 1,
			method:     "password",
			wantStatus: http.StatusForbidden,
			wantCode:   "VERIFICATION_INVALID",
		},
		{
			name:       "expired",
			verifiedAt: now - SecureVerificationTimeout,
			method:     "2fa",
			wantStatus: http.StatusForbidden,
			wantCode:   "VERIFICATION_EXPIRED",
		},
		{
			name:       "missing method",
			verifiedAt: now,
			wantStatus: http.StatusForbidden,
			wantCode:   "VERIFICATION_INVALID",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handlerCalled := false
			router := gin.New()
			router.Use(sessions.Sessions(
				"session",
				cookie.NewStore([]byte("secure-verification-test-secret")),
			))
			router.GET(
				"/protected",
				func(c *gin.Context) {
					c.Set("id", 1)
					session := sessions.Default(c)
					session.Set(SecureVerificationSessionKey, test.verifiedAt)
					if test.method != "" {
						session.Set(secureVerificationMethodSessionKey, test.method)
					}
					session.Set(secureVerificationUserIDSessionKey, 1)
					session.Set(
						secureVerificationBrowserSessionKey,
						"browser-session-a",
					)
					session.Set(
						constant.SessionKeyBrowserSessionID,
						"browser-session-a",
					)
					require.NoError(t, session.Save())
					c.Next()
				},
				SecureVerificationRequired(),
				func(c *gin.Context) {
					handlerCalled = true
					c.Status(http.StatusNoContent)
				},
			)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/protected", nil),
			)

			assert.Equal(t, test.wantStatus, recorder.Code)
			assert.Equal(t, test.wantStatus == http.StatusNoContent, handlerCalled)
			if test.wantCode != "" {
				var response struct {
					Code string `json:"code"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				assert.Equal(t, test.wantCode, response.Code)
			}
		})
	}
}

func TestSecureVerificationIsBoundToUserAndBrowserSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name                   string
		currentUserID          int
		verifiedUserID         int
		currentBrowserSession  string
		verifiedBrowserSession string
	}{
		{
			name:                   "different user",
			currentUserID:          2,
			verifiedUserID:         1,
			currentBrowserSession:  "browser-session-a",
			verifiedBrowserSession: "browser-session-a",
		},
		{
			name:                   "different browser session",
			currentUserID:          1,
			verifiedUserID:         1,
			currentBrowserSession:  "browser-session-b",
			verifiedBrowserSession: "browser-session-a",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handlerCalled := false
			router := gin.New()
			router.Use(sessions.Sessions(
				"session",
				cookie.NewStore([]byte("secure-verification-binding-secret")),
			))
			router.GET(
				"/protected",
				func(c *gin.Context) {
					c.Set("id", test.currentUserID)
					session := sessions.Default(c)
					session.Set(
						SecureVerificationSessionKey,
						time.Now().Unix(),
					)
					session.Set(
						secureVerificationMethodSessionKey,
						"password",
					)
					session.Set(
						secureVerificationUserIDSessionKey,
						test.verifiedUserID,
					)
					session.Set(
						secureVerificationBrowserSessionKey,
						test.verifiedBrowserSession,
					)
					session.Set(
						constant.SessionKeyBrowserSessionID,
						test.currentBrowserSession,
					)
					require.NoError(t, session.Save())
					c.Next()
				},
				SecureVerificationRequired(),
				func(c *gin.Context) {
					handlerCalled = true
					c.Status(http.StatusNoContent)
				},
			)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/protected", nil),
			)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.False(t, handlerCalled)
			var response struct {
				Code string `json:"code"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, "VERIFICATION_INVALID", response.Code)
		})
	}
}
