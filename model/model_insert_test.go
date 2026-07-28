package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelInsertRollsBackWhenDefaultCorrectionFails(t *testing.T) {
	const callbackName = "test:fail_model_default_correction"
	injectedErr := errors.New("injected model update failure")
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "models" {
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(callbackName))
	})

	modelName := "transactional-model-insert-" + t.Name()
	entry := &Model{ModelName: modelName, Status: 0, SyncOfficial: 0}
	require.ErrorIs(t, entry.Insert(), injectedErr)

	var count int64
	require.NoError(t, DB.Unscoped().Model(&Model{}).Where("model_name = ?", modelName).Count(&count).Error)
	assert.Zero(t, count)
}
