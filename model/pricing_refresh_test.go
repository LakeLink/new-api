package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPricingRefreshTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB := DB
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = originalDB
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&Ability{}, &Channel{}, &Model{}, &Vendor{}))
	return db
}

func TestUpdatePricingKeepsLastGoodSnapshotOnMetadataQueryFailure(t *testing.T) {
	db := setupPricingRefreshTestDB(t)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"test:fail_pricing_metadata_query",
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "models" {
				_ = tx.AddError(errors.New("injected pricing metadata failure"))
			}
		},
	))

	updatePricingLock.Lock()
	originalPricing := pricingMap
	originalVendors := vendorsList
	originalUpdatedAt := lastGetPricingTime
	snapshotTime := time.Unix(123, 0)
	pricingMap = []Pricing{{ModelName: "last-good-model"}}
	vendorsList = []PricingVendor{{ID: 7, Name: "last-good-vendor"}}
	lastGetPricingTime = snapshotTime
	updatePricingLock.Unlock()
	t.Cleanup(func() {
		updatePricingLock.Lock()
		pricingMap = originalPricing
		vendorsList = originalVendors
		lastGetPricingTime = originalUpdatedAt
		updatePricingLock.Unlock()
	})

	updatePricingLock.Lock()
	modelSupportEndpointsLock.Lock()
	updatePricing()
	modelSupportEndpointsLock.Unlock()
	actualPricing := append([]Pricing(nil), pricingMap...)
	actualVendors := append([]PricingVendor(nil), vendorsList...)
	actualUpdatedAt := lastGetPricingTime
	updatePricingLock.Unlock()

	assert.Equal(t, []Pricing{{ModelName: "last-good-model"}}, actualPricing)
	assert.Equal(t, []PricingVendor{{ID: 7, Name: "last-good-vendor"}}, actualVendors)
	assert.Equal(t, snapshotTime, actualUpdatedAt)
}

func TestUpdatePricingKeepsLastGoodSnapshotOnDefaultVendorInsertFailure(t *testing.T) {
	db := setupPricingRefreshTestDB(t)
	channel := Channel{Name: "pricing-default-vendor-channel", Type: constant.ChannelTypeOpenAI}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&Ability{
		Group:     "default",
		Model:     "gpt-default-vendor-insert-failure",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error)

	injectedErr := errors.New("injected default vendor insert failure")
	const callbackName = "test:fail_default_vendor_insert"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "vendors" {
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Create().Remove(callbackName))
	})

	updatePricingLock.Lock()
	originalPricing := pricingMap
	originalVendors := vendorsList
	originalUpdatedAt := lastGetPricingTime
	snapshotTime := time.Unix(456, 0)
	pricingMap = []Pricing{{ModelName: "last-good-model"}}
	vendorsList = []PricingVendor{{ID: 9, Name: "last-good-vendor"}}
	lastGetPricingTime = snapshotTime
	updatePricingLock.Unlock()
	t.Cleanup(func() {
		updatePricingLock.Lock()
		pricingMap = originalPricing
		vendorsList = originalVendors
		lastGetPricingTime = originalUpdatedAt
		updatePricingLock.Unlock()
	})

	updatePricingLock.Lock()
	modelSupportEndpointsLock.Lock()
	updatePricing()
	modelSupportEndpointsLock.Unlock()
	actualPricing := append([]Pricing(nil), pricingMap...)
	actualVendors := append([]PricingVendor(nil), vendorsList...)
	actualUpdatedAt := lastGetPricingTime
	updatePricingLock.Unlock()

	assert.Equal(t, []Pricing{{ModelName: "last-good-model"}}, actualPricing)
	assert.Equal(t, []PricingVendor{{ID: 9, Name: "last-good-vendor"}}, actualVendors)
	assert.Equal(t, snapshotTime, actualUpdatedAt)
}
