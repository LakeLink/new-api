package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManageUserRejectsZeroIDAndUnknownAction(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "zero id",
			body: `{"id":0,"action":"disable"}`,
		},
		{
			name: "unknown action",
			body: `{"id":1,"action":"unexpected"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupModelListControllerTestDB(t)
			user := model.User{
				Id:       1,
				Username: "managed-input-user",
				Role:     common.RoleCommonUser,
				Status:   common.UserStatusEnabled,
				Group:    "default",
				AffCode:  "managed-input-aff",
			}
			require.NoError(t, db.Create(&user).Error)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("role", common.RoleRootUser)
			c.Request = httptest.NewRequest(
				http.MethodPost,
				"/api/user/manage",
				bytes.NewBufferString(test.body),
			)

			ManageUser(c)

			var response struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(
				recorder.Body.Bytes(),
				&response,
			))
			assert.False(t, response.Success)
			var stored model.User
			require.NoError(t, db.First(&stored, user.Id).Error)
			assert.Equal(t, common.UserStatusEnabled, stored.Status)
			assert.Equal(t, common.RoleCommonUser, stored.Role)
		})
	}
}

func TestCreateUserRejectsUndefinedRole(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("role", common.RoleRootUser)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/user",
		bytes.NewBufferString(`{
			"username":"undefined-role-user",
			"password":"Password123",
			"role":5
		}`),
	)

	CreateUser(c)

	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	var count int64
	require.NoError(t, db.Model(&model.User{}).
		Where("username = ?", "undefined-role-user").
		Count(&count).Error)
	assert.Zero(t, count)
}
