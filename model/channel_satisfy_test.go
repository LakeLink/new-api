package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelAbilityLookupPropagatesDatabaseFailures(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Ability{})
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	require.NoError(t, db.Create(&Ability{
		Group:     "default",
		Model:     "db-error-model",
		ChannelId: 42,
		Enabled:   true,
	}).Error)
	enabled, err := IsChannelEnabledForGroupModel("default", "db-error-model", 42)
	require.NoError(t, err)
	assert.True(t, enabled)

	injectedErr := errors.New("injected ability lookup failure")
	const callbackName = "test:fail_channel_ability_lookup"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "abilities" {
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(callbackName))
	})

	enabled, err = IsChannelEnabledForGroupModel("default", "db-error-model", 42)
	require.ErrorIs(t, err, injectedErr)
	assert.False(t, enabled)
	enabled, err = IsChannelEnabledForAnyGroupModel(
		[]string{"default", "fallback"},
		"db-error-model",
		42,
	)
	require.ErrorIs(t, err, injectedErr)
	assert.False(t, enabled)
}
