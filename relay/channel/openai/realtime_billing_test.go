package openai

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPreConsumeUsageDoesNotDuplicateDeliveredUsageAfterReserveFailure(t *testing.T) {
	originalDB := model.DB
	db, err := gorm.Open(sqlite.Open("file:realtime-billing-once?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	model.DB = db
	t.Cleanup(func() { model.DB = originalDB })

	token := model.Token{UserId: 1, Key: "realtime-billing-key", RemainQuota: 1000}
	require.NoError(t, db.Create(&token).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/", nil)
	common.SetContextKey(ctx, constant.ContextKeyAutoGroup, 42)
	info := &relaycommon.RelayInfo{
		UserId:          1,
		TokenId:         token.Id,
		TokenKey:        "sk-" + token.Key,
		OriginModelName: "gpt-realtime",
		UsingGroup:      "default",
		UserGroup:       "default",
	}
	delta := &dto.RealtimeUsage{
		TotalTokens:  12,
		InputTokens:  7,
		OutputTokens: 5,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens:  6,
			AudioTokens: 1,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:  4,
			AudioTokens: 1,
		},
	}
	total := &dto.RealtimeUsage{}

	require.ErrorContains(t, preConsumeUsage(ctx, info, delta, total), "invalid automatic billing group")
	assert.Equal(t, 12, total.TotalTokens)
	assert.Equal(t, 7, total.InputTokens)
	assert.Equal(t, 5, total.OutputTokens)
	assert.Zero(t, delta.TotalTokens, "the delivered delta must not survive for the shutdown flush")

	require.ErrorContains(t, preConsumeUsage(ctx, info, delta, total), "invalid automatic billing group")
	assert.Equal(t, 12, total.TotalTokens)
	assert.Equal(t, 7, total.InputTokens)
	assert.Equal(t, 5, total.OutputTokens)
}
