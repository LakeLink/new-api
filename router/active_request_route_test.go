package router

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/active_request_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func activeRequestsRouter(t *testing.T, role int) (*gin.Engine, *gorm.DB, string, *model.UserSession, <-chan struct{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	oldTracker := service.GlobalActiveRequestTracker
	service.GlobalActiveRequestTracker = &service.ActiveRequestTracker{}

	setting := active_request_setting.GetActiveRequestSetting()
	oldRetentionSeconds := setting.CompletedRetentionSeconds
	setting.CompletedRetentionSeconds = 10
	oldDB := model.DB
	oldRedis := common.RedisEnabled
	oldLogDB := model.LOG_DB
	common.RedisEnabled = false
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	databaseType := common.DatabaseType(os.Getenv("ACTIVE_REQUEST_TEST_DRIVER"))
	var dialector gorm.Dialector
	switch databaseType {
	case common.DatabaseTypeMySQL:
		dialector = mysql.Open(os.Getenv("ACTIVE_REQUEST_TEST_DSN"))
	case common.DatabaseTypePostgreSQL:
		dialector = postgres.Open(os.Getenv("ACTIVE_REQUEST_TEST_DSN"))
	default:
		databaseType = common.DatabaseTypeSQLite
		dialector = sqlite.Open(filepath.Join(t.TempDir(), "active-requests.db"))
	}
	common.SetDatabaseTypes(databaseType, databaseType)
	db, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	// These are dedicated test databases; reset fixtures for each authorization case.
	require.NoError(t, db.Migrator().DropTable(&model.AuditLog{}, &model.UserAccessToken{}, &model.UserSession{}, &model.Option{}, &model.User{}))
	if t.Name() == "TestActiveRequestsRouteIsRegistered" {
		versionSQL := "select version()"
		if databaseType == common.DatabaseTypeSQLite {
			versionSQL = "select sqlite_version()"
		}
		var version string
		require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
		t.Logf("Database: %s %s", databaseType, version)
	}
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Option{}, &model.AuditLog{}))
	require.NoError(t, db.Create(&model.User{
		Id:          1,
		Username:    "admin",
		Role:        role,
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}).Error)
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		service.GlobalActiveRequestTracker = oldTracker
		setting.CompletedRetentionSeconds = oldRetentionSeconds
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
		common.SetDatabaseTypes(oldMainType, oldLogType)
	})

	router := gin.New()
	finished := make(chan struct{}, 1)
	router.Use(func(c *gin.Context) {
		c.Next()
		if c.Request.URL.Path == "/api/active-requests/stream" {
			finished <- struct{}{}
		}
	})
	now := time.Now().Unix()
	session := &model.UserSession{SID: "active-request-route-test", UserID: 1, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "test-refresh-hash", LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600}
	require.NoError(t, model.CreateUserSession(session))
	token, _, err := service.IssueAccessToken(service.AuthIdentity{UserID: 1, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1})
	require.NoError(t, err)
	SetApiRouter(router)
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.RequestURI, "/api") {
			controller.RelayNotFound(c)
			return
		}
		c.Status(http.StatusNotFound)
	})

	return router, db, token, session, finished
}

func TestActiveRequestsRouteIsRegistered(t *testing.T) {
	router, _, token, _, _ := activeRequestsRouter(t, common.RoleAdminUser)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/active-requests", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, "GET /api/active-requests must be registered and accept an authenticated admin")
}

// Read complete SSE frames from a real HTTP connection, not a buffered recorder.
func readActiveRequestEvent(t *testing.T, reader *bufio.Reader) (string, []byte) {
	t.Helper()
	var event string
	var data []byte
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return event, data
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			event = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			data = []byte(value)
		}
	}
}

func openActiveRequestStream(t *testing.T, router *gin.Engine, token string) (*http.Response, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/active-requests/stream", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	assert.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	assert.Equal(t, "no", response.Header.Get("X-Accel-Buffering"))
	assert.Empty(t, response.Header.Get("Content-Encoding"), "gzip must not buffer SSE")
	return response, bufio.NewReader(response.Body), cancel
}

func TestActiveRequestsStreamUpdatesAndDisconnects(t *testing.T) {
	router, _, token, _, finished := activeRequestsRouter(t, common.RoleAdminUser)
	_, reader, cancel := openActiveRequestStream(t, router, token)
	event, payload := readActiveRequestEvent(t, reader)
	require.Equal(t, "snapshot", event)
	var snapshot struct {
		Success   bool                            `json:"success"`
		Data      []service.ActiveRequestSnapshot `json:"data"`
		Retention int                             `json:"completed_retention_seconds"`
	}
	require.NoError(t, common.Unmarshal(payload, &snapshot))
	assert.True(t, snapshot.Success)
	assert.Empty(t, snapshot.Data)
	assert.Equal(t, 10, snapshot.Retention)
	relayContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	service.GlobalActiveRequestTracker.Register(&relaycommon.RelayInfo{RequestId: "live", OriginModelName: "gpt-test"}, relayContext)
	event, payload = readActiveRequestEvent(t, reader)
	require.Equal(t, "snapshot", event)
	require.NoError(t, common.Unmarshal(payload, &snapshot))
	require.Len(t, snapshot.Data, 1)
	assert.Equal(t, "live", snapshot.Data[0].RequestId)
	assert.Equal(t, "active", snapshot.Data[0].Status)
	service.GlobalActiveRequestTracker.Deregister("live")
	_, payload = readActiveRequestEvent(t, reader)
	require.NoError(t, common.Unmarshal(payload, &snapshot))
	require.Len(t, snapshot.Data, 1)
	assert.Equal(t, "completed", snapshot.Data[0].Status)
	assert.False(t, snapshot.Data[0].CanTerminate)
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnected stream handler did not exit")
	}
}

func TestActiveRequestsStreamRejectsUnauthorizedCredentials(t *testing.T) {
	for _, tc := range []struct {
		name       string
		role       int
		credential string
		status     int
	}{
		{"anonymous", common.RoleAdminUser, "", http.StatusUnauthorized},
		{"tampered token", common.RoleAdminUser, "invalid", http.StatusUnauthorized},
		{"ordinary user", common.RoleCommonUser, "session", http.StatusForbidden},
		{"missing scope", common.RoleAdminUser, "pat", http.StatusForbidden},
		{"expired PAT", common.RoleAdminUser, "expired", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, db, token, _, _ := activeRequestsRouter(t, tc.role)
			if tc.credential == "pat" || tc.credential == "expired" {
				token = model.AccessTokenPrefix + "stream-test"
				pat := &model.UserAccessToken{UserId: 1, Name: "stream", TokenHash: model.AccessTokenFingerprint(token), Scopes: `["ops:write"]`}
				if tc.credential == "expired" {
					pat.ExpiresAt = time.Now().Unix() - 1
				}
				require.NoError(t, db.Create(pat).Error)
			} else if tc.credential != "session" {
				token = tc.credential
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/active-requests/stream", nil)
			if token != "" {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			router.ServeHTTP(recorder, request)
			assert.Equal(t, tc.status, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "event: snapshot")
		})
	}
}

func TestActiveRequestsStreamStopsAfterAccessRevocation(t *testing.T) {
	for _, reason := range []string{"logout", "session expiry", "disabled user", "role downgrade", "PAT scope removed", "PAT deleted", "PAT expired"} {
		t.Run(reason, func(t *testing.T) {
			router, db, token, session, _ := activeRequestsRouter(t, common.RoleAdminUser)
			var pat *model.UserAccessToken
			if strings.HasPrefix(reason, "PAT") {
				token = model.AccessTokenPrefix + "stream-test"
				pat = &model.UserAccessToken{UserId: 1, Name: "stream", TokenHash: model.AccessTokenFingerprint(token), Scopes: `["ops:read"]`}
				require.NoError(t, db.Create(pat).Error)
			}
			response, reader, _ := openActiveRequestStream(t, router, token)
			event, _ := readActiveRequestEvent(t, reader)
			require.Equal(t, "snapshot", event)
			switch reason {
			case "logout":
				revoked, err := model.RevokeUserSession(1, session.SID, "logout")
				require.NoError(t, err)
				require.True(t, revoked)
			case "session expiry":
				require.NoError(t, db.Model(session).Update("expires_at", time.Now().Unix()-1).Error)
			case "disabled user":
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("status", common.UserStatusDisabled).Error)
			case "role downgrade":
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("role", common.RoleCommonUser).Error)
			case "PAT scope removed":
				_, err := model.UpdateUserAccessToken(1, pat.Id, "stream", []string{"ops:write"})
				require.NoError(t, err)
			case "PAT deleted":
				_, err := model.DeleteUserAccessToken(1, pat.Id)
				require.NoError(t, err)
			case "PAT expired":
				require.NoError(t, db.Model(pat).Update("expires_at", time.Now().Unix()-1).Error)
			}
			event, _ = readActiveRequestEvent(t, reader)
			assert.Equal(t, "unauthorized", event)
			rest, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			assert.Empty(t, rest, "authorization loss must end the feed without another snapshot")
		})
	}
}
