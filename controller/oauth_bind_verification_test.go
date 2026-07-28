package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

type verificationGateOAuthProvider struct {
	exchangeCalls int
}

func (*verificationGateOAuthProvider) GetName() string { return "Verification Gate" }
func (*verificationGateOAuthProvider) IsEnabled() bool { return true }
func (provider *verificationGateOAuthProvider) ExchangeToken(
	context.Context,
	string,
	*gin.Context,
) (*oauth.OAuthToken, error) {
	provider.exchangeCalls++
	return nil, errors.New("exchange sentinel")
}
func (*verificationGateOAuthProvider) GetUserInfo(
	context.Context,
	*oauth.OAuthToken,
) (*oauth.OAuthUser, error) {
	return nil, errors.New("not reached")
}
func (*verificationGateOAuthProvider) IsUserIDTaken(string) (bool, error) {
	return false, nil
}
func (*verificationGateOAuthProvider) FillUserByProviderID(
	*model.User,
	string,
) error {
	return nil
}
func (*verificationGateOAuthProvider) SetProviderUserID(*model.User, string) {}
func (*verificationGateOAuthProvider) GetProviderPrefix() string {
	return "verification_gate_"
}

func TestOAuthBindingRequiresCurrentSecureVerificationBeforeTokenExchange(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	now := time.Now().Unix()
	tests := []struct {
		name          string
		verifiedAt    *int64
		method        string
		expectedCalls int
	}{
		{
			name: "missing verification",
		},
		{
			name:       "future verification",
			verifiedAt: func() *int64 { value := now + 31; return &value }(),
			method:     secureVerificationMethodPassword,
		},
		{
			name:          "current verification",
			verifiedAt:    &now,
			method:        secureVerificationMethodPassword,
			expectedCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &verificationGateOAuthProvider{}
			engine := gin.New()
			engine.Use(sessions.Sessions(
				"session",
				cookie.NewStore([]byte("oauth-bind-verification-secret")),
			))
			engine.GET("/bind", func(c *gin.Context) {
				c.Set("id", 1)
				if test.verifiedAt != nil {
					session := sessions.Default(c)
					session.Set(
						SecureVerificationSessionKey,
						*test.verifiedAt,
					)
					session.Set(
						secureVerificationMethodSessionKey,
						test.method,
					)
					session.Set(secureVerificationUserIDSessionKey, 1)
					session.Set(
						secureVerificationBrowserSessionKey,
						"browser-session-a",
					)
					session.Set(
						constant.SessionKeyBrowserSessionID,
						"browser-session-a",
					)
				}
				handleOAuthBind(
					c,
					provider,
					&model.User{Id: 1, Username: "binding-user"},
				)
			})

			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/bind?code=test", nil),
			)

			assert.Equal(t, test.expectedCalls, provider.exchangeCalls)
			if test.expectedCalls == 0 {
				assert.Equal(t, http.StatusForbidden, recorder.Code)
			}
		})
	}
}
