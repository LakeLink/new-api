package model

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingAdjustmentTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	oldDB := DB
	oldLogDB := LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldRedisClient := common.RDB
	oldBatchEnabled := common.BatchUpdateEnabled

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", url.QueryEscape(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	DB = db
	LOG_DB = db
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false

	models = append(models, &BillingReservation{}, &TaskBillingFinalization{}, &QuotaData{})
	require.NoError(t, db.AutoMigrate(models...))

	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRedisClient
		common.BatchUpdateEnabled = oldBatchEnabled
		require.NoError(t, sqlDB.Close())
	})
	return db
}
