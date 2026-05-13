package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserAuthWritesSessionGroupToRelayContextKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-secret"))))
	router.GET(
		"/test",
		func(c *gin.Context) {
			session := sessions.Default(c)
			session.Set("username", "admin")
			session.Set("role", common.RoleAdminUser)
			session.Set("id", 1)
			session.Set("status", common.UserStatusEnabled)
			session.Set("group", "user:admin")
			require.NoError(t, session.Save())
			c.Next()
		},
		UserAuth(),
		func(c *gin.Context) {
			assert.Equal(t, "user:admin", common.GetContextKeyString(c, constant.ContextKeyUserGroup))
			assert.Equal(t, "user:admin", common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
			c.Status(http.StatusNoContent)
		},
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/test", nil)
	request.Header.Set("New-Api-User", "1")
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}
