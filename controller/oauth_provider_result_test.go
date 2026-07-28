package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type invalidResultOAuthProvider struct {
	token *oauth.OAuthToken
	user  *oauth.OAuthUser
}

func (p *invalidResultOAuthProvider) GetName() string { return "Invalid Result" }
func (p *invalidResultOAuthProvider) IsEnabled() bool { return true }
func (p *invalidResultOAuthProvider) ExchangeToken(
	context.Context,
	string,
	*gin.Context,
) (*oauth.OAuthToken, error) {
	return p.token, nil
}
func (p *invalidResultOAuthProvider) GetUserInfo(
	context.Context,
	*oauth.OAuthToken,
) (*oauth.OAuthUser, error) {
	return p.user, nil
}
func (p *invalidResultOAuthProvider) IsUserIDTaken(string) (bool, error) {
	return false, nil
}
func (p *invalidResultOAuthProvider) FillUserByProviderID(
	*model.User,
	string,
) error {
	return nil
}
func (p *invalidResultOAuthProvider) SetProviderUserID(*model.User, string) {}
func (p *invalidResultOAuthProvider) GetProviderPrefix() string             { return "invalid_" }

func TestHandleOAuthRejectsNilAndEmptyProviderResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.OAuthState{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })

	tests := []struct {
		name     string
		provider *invalidResultOAuthProvider
	}{
		{
			name:     "nil token",
			provider: &invalidResultOAuthProvider{},
		},
		{
			name: "nil user info",
			provider: &invalidResultOAuthProvider{
				token: &oauth.OAuthToken{AccessToken: "access-token"},
			},
		},
		{
			name: "empty provider user id",
			provider: &invalidResultOAuthProvider{
				token: &oauth.OAuthToken{AccessToken: "access-token"},
				user:  &oauth.OAuthUser{},
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const providerName = "invalid-result"
			oauth.Register(providerName, test.provider)
			t.Cleanup(func() { oauth.Unregister(providerName) })

			state := "invalid-result-state-" + time.Now().Add(
				time.Duration(index)*time.Second,
			).Format("150405.000000000")
			require.NoError(t, model.RotateOAuthState(
				"",
				state,
				time.Now().Add(time.Minute).Unix(),
			))

			router := gin.New()
			router.Use(sessions.Sessions(
				"session",
				cookie.NewStore([]byte("invalid-oauth-result-secret")),
			))
			router.GET("/seed", func(c *gin.Context) {
				session := sessions.Default(c)
				session.Set("oauth_state", state)
				require.NoError(t, session.Save())
				c.Status(http.StatusNoContent)
			})
			router.GET("/oauth/:provider", HandleOAuth)

			seedRecorder := httptest.NewRecorder()
			router.ServeHTTP(
				seedRecorder,
				httptest.NewRequest(http.MethodGet, "/seed", nil),
			)
			sessionCookie := browserSessionCookie(t, seedRecorder)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/oauth/"+providerName+"?state="+state+"&code=code",
				nil,
			)
			request.AddCookie(sessionCookie)
			router.ServeHTTP(recorder, request)

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}
