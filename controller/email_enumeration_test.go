package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmailVerificationDoesNotRevealExistingAccount(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Username: "email-enumeration-user",
		Email:    "existing@example.com",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "email-enumeration-aff",
	}).Error)

	common.OptionMapRWMutex.Lock()
	optionMapWasNil := common.OptionMap == nil
	if optionMapWasNil {
		common.OptionMap = make(map[string]string)
	}
	oldDomainRestriction, hadDomainRestriction := common.OptionMap["EmailDomainRestrictionEnabled"]
	oldAliasRestriction, hadAliasRestriction := common.OptionMap["EmailAliasRestrictionEnabled"]
	common.OptionMap["EmailDomainRestrictionEnabled"] = "false"
	common.OptionMap["EmailAliasRestrictionEnabled"] = "false"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if optionMapWasNil {
			common.OptionMap = nil
			return
		}
		if hadDomainRestriction {
			common.OptionMap["EmailDomainRestrictionEnabled"] = oldDomainRestriction
		} else {
			delete(common.OptionMap, "EmailDomainRestrictionEnabled")
		}
		if hadAliasRestriction {
			common.OptionMap["EmailAliasRestrictionEnabled"] = oldAliasRestriction
		} else {
			delete(common.OptionMap, "EmailAliasRestrictionEnabled")
		}
	})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/verification",
		strings.NewReader(`{"email":"EXISTING@example.com"}`),
	)

	SendEmailVerification(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Empty(t, response.Message)
	assert.NotContains(t, recorder.Body.String(), "already")
	assert.NotContains(t, recorder.Body.String(), "已被使用")
}
