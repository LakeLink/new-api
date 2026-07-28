package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateUserSettingRejectsUnrepresentableQuotaThreshold(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	user := model.User{
		Username: "quota-threshold-user",
		Password: "password",
		AffCode:  "quota-threshold-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)

	for _, threshold := range []string{
		fmt.Sprintf("%d", common.MaxQuota+1),
		"1e300",
	} {
		t.Run(threshold, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("id", user.Id)
			c.Request = httptest.NewRequest(
				http.MethodPut,
				"/api/user/setting",
				bytes.NewBufferString(
					`{"notify_type":"email","quota_warning_threshold":`+
						threshold+
						`}`,
				),
			)
			c.Request.Header.Set("Content-Type", "application/json")

			UpdateUserSetting(c)

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}

func TestUpdateUserSettingRejectsUnsafeNotificationURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		payload map[string]any
	}{
		{
			name: "relative webhook URL",
			payload: map[string]any{
				"notify_type":             "webhook",
				"quota_warning_threshold": 1,
				"webhook_url":             "/internal/hook",
			},
		},
		{
			name: "webhook URL credentials",
			payload: map[string]any{
				"notify_type":             "webhook",
				"quota_warning_threshold": 1,
				"webhook_url":             "https://user:secret@notify.example.com/hook",
			},
		},
		{
			name: "non HTTP Bark URL",
			payload: map[string]any{
				"notify_type":             "bark",
				"quota_warning_threshold": 1,
				"bark_url":                "javascript:alert(1)",
			},
		},
		{
			name: "Gotify URL fragment",
			payload: map[string]any{
				"notify_type":             "gotify",
				"quota_warning_threshold": 1,
				"gotify_url":              "https://notify.example.com/#ignored",
				"gotify_token":            "token",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := common.Marshal(test.payload)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("id", 1)
			c.Request = httptest.NewRequest(
				http.MethodPut,
				"/api/user/setting",
				bytes.NewReader(body),
			)
			c.Request.Header.Set("Content-Type", "application/json")

			require.NotPanics(t, func() {
				UpdateUserSetting(c)
			})

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}
