package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteSubscriptionPlanMigrationAddsRequiredColumnsToPopulatedTable(t *testing.T) {
	db := useCrossDatabaseIdentityTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TABLE subscription_plans (
			id integer PRIMARY KEY,
			enabled numeric DEFAULT 1
		)
	`).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO subscription_plans (id, enabled) VALUES (?, ?)",
		1,
		true,
	).Error)

	require.NoError(t, ensureSubscriptionPlanTableSQLite())

	var plan SubscriptionPlan
	require.NoError(t, db.First(&plan, 1).Error)
	assert.Empty(t, plan.Title)
	assert.Zero(t, plan.PriceAmount)
	assert.Equal(t, "USD", plan.Currency)
	assert.Equal(t, SubscriptionDurationMonth, plan.DurationUnit)
	assert.Equal(t, 1, plan.DurationValue)
	assert.True(t, plan.Enabled)
	assert.Equal(t, SubscriptionResetNever, plan.QuotaResetPeriod)

	// The migration is idempotent and must preserve the legacy row.
	require.NoError(t, ensureSubscriptionPlanTableSQLite())
	var count int64
	require.NoError(t, db.Table("subscription_plans").Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
