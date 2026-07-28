package model

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfiguredSQLMaxLifetimeRejectsOverflow(t *testing.T) {
	t.Setenv("SQL_MAX_LIFETIME", "0")
	assert.Zero(t, configuredSQLMaxLifetime())

	t.Setenv("SQL_MAX_LIFETIME", "-1")
	assert.Equal(t, 60*time.Second, configuredSQLMaxLifetime())

	if strconv.IntSize == 64 {
		overflowingSeconds := int(math.MaxInt64/int64(time.Second) + 1)
		t.Setenv("SQL_MAX_LIFETIME", strconv.FormatInt(int64(overflowingSeconds), 10))
		assert.Equal(t, 60*time.Second, configuredSQLMaxLifetime())
	}
}

func TestSubscriptionPriceMigrationFailsClosedOnMetadataError(t *testing.T) {
	originalMainType := common.MainDatabaseType()
	originalLogType := common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeMySQL, originalLogType)
	t.Cleanup(func() {
		common.SetDatabaseTypes(originalMainType, originalLogType)
	})

	err := migrateSubscriptionPlanPriceAmount()
	require.Error(t, err)
	assert.ErrorContains(t, err, "inspect MySQL column")
}
