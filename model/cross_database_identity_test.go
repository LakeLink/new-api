package model

import (
	"encoding/base64"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useCrossDatabaseIdentityTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB := DB
	previousType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		initCol()
	})
	require.NoError(t, db.AutoMigrate(models...))
	return db
}

func TestNamedResourcesEnforceActiveIdentityAndAllowReuseAfterDelete(t *testing.T) {
	useCrossDatabaseIdentityTestDB(t, &Model{}, &Vendor{}, &PrefillGroup{})

	modelMeta := &Model{ModelName: " GPT-Example "}
	require.NoError(t, modelMeta.Insert())
	assert.Equal(t, "GPT-Example", modelMeta.ModelName)
	require.Error(t, (&Model{ModelName: "gpt-example"}).Insert())
	require.NoError(t, DeleteModelByID(modelMeta.Id))
	require.NoError(t, (&Model{ModelName: "gpt-example"}).Insert())

	vendor := &Vendor{Name: " Example Vendor "}
	require.NoError(t, vendor.Insert())
	require.Error(t, (&Vendor{Name: "example vendor"}).Insert())
	require.NoError(t, DeleteVendorByID(vendor.Id))
	require.NoError(t, (&Vendor{Name: "EXAMPLE VENDOR"}).Insert())

	group := &PrefillGroup{Name: " Vision ", Type: "model"}
	require.NoError(t, group.Insert())
	require.Error(t, (&PrefillGroup{Name: "vision", Type: "model"}).Insert())
	require.NoError(t, DeletePrefillGroupByID(group.Id))
	require.NoError(t, (&PrefillGroup{Name: "VISION", Type: "model"}).Insert())
}

func TestNamedResourceMigrationRejectsAmbiguousActiveHistory(t *testing.T) {
	type legacyVendor struct {
		Id   int
		Name string `gorm:"size:128;not null"`
	}

	db := useCrossDatabaseIdentityTestDB(t)
	require.NoError(t, db.Table("vendors").AutoMigrate(&legacyVendor{}))
	require.NoError(t, db.Table("vendors").Create(&legacyVendor{Name: "Acme"}).Error)
	require.NoError(t, db.Table("vendors").Create(&legacyVendor{Name: "acme"}).Error)
	require.NoError(t, prepareCrossDatabaseIdentityColumns())
	require.NoError(t, db.AutoMigrate(&Model{}, &Vendor{}, &PrefillGroup{}))

	err := migrateNamedResourceIdentityHashes()
	require.Error(t, err)

	var populated int64
	require.NoError(t, db.Model(&Vendor{}).Where("name_hash IS NOT NULL").Count(&populated).Error)
	assert.Zero(t, populated)
}

func TestPasskeyCredentialIdentityCannotMoveBetweenUsers(t *testing.T) {
	useCrossDatabaseIdentityTestDB(t, &User{}, &PasskeyCredential{})
	firstUser := &User{Username: "passkey-owner-one", Password: "hash", AffCode: "passkey-aff-one"}
	secondUser := &User{Username: "passkey-owner-two", Password: "hash", AffCode: "passkey-aff-two"}
	require.NoError(t, DB.Create(firstUser).Error)
	require.NoError(t, DB.Create(secondUser).Error)

	rawCredentialID := []byte("credential-with-a-long-opaque-identifier")
	encodedCredentialID := base64.StdEncoding.EncodeToString(rawCredentialID)
	require.NoError(t, UpsertPasskeyCredential(&PasskeyCredential{
		UserID:       firstUser.Id,
		CredentialID: encodedCredentialID,
		PublicKey:    base64.StdEncoding.EncodeToString([]byte("public-key-one")),
	}))
	require.Error(t, UpsertPasskeyCredential(&PasskeyCredential{
		UserID:       secondUser.Id,
		CredentialID: encodedCredentialID,
		PublicKey:    base64.StdEncoding.EncodeToString([]byte("public-key-two")),
	}))

	persisted, err := GetPasskeyByCredentialID(rawCredentialID)
	require.NoError(t, err)
	assert.Equal(t, firstUser.Id, persisted.UserID)
}

func TestCompactHashesPreserveExactCasbinAndMetricIdentity(t *testing.T) {
	useCrossDatabaseIdentityTestDB(t, &CasbinRule{}, &PerfMetric{})

	rule := &CasbinRule{Ptype: "p", V0: "role:admin", V1: "channels", V2: "read"}
	require.NoError(t, DB.Create(rule).Error)
	require.Error(t, DB.Create(&CasbinRule{
		Ptype: "p",
		V0:    "role:admin",
		V1:    "channels",
		V2:    "read",
	}).Error)
	require.NoError(t, DB.Create(&CasbinRule{
		Ptype: "p",
		V0:    "role:admin",
		V1:    "Channels",
		V2:    "read",
	}).Error)

	require.NoError(t, UpsertPerfMetric(&PerfMetric{
		ModelName:    "model-A",
		Group:        "default",
		BucketTs:     100,
		RequestCount: 2,
	}))
	require.NoError(t, UpsertPerfMetric(&PerfMetric{
		ModelName:    "model-A",
		Group:        "default",
		BucketTs:     100,
		RequestCount: 3,
	}))
	var metric PerfMetric
	require.NoError(t, DB.Where(
		"model_name = ? AND "+commonGroupCol+" = ? AND bucket_ts = ?",
		"model-A",
		"default",
		100,
	).First(&metric).Error)
	assert.EqualValues(t, 5, metric.RequestCount)
}

func TestTopUpProviderPaymentIdentityIsUniqueAndCaseExact(t *testing.T) {
	useCrossDatabaseIdentityTestDB(t, &User{}, &TopUp{})
	firstUser := &User{
		Username: "payment-owner-one",
		Password: "hash",
		AffCode:  "payment-aff-one",
		Quota:    100,
	}
	secondUser := &User{
		Username: "payment-owner-two",
		Password: "hash",
		AffCode:  "payment-aff-two",
		Quota:    100,
	}
	require.NoError(t, DB.Create(firstUser).Error)
	require.NoError(t, DB.Create(secondUser).Error)

	first := &TopUp{
		UserId:            firstUser.Id,
		Quota:             10,
		Money:             1,
		TradeNo:           "merchant-one",
		ProviderPaymentId: "Pay_CASE",
		PaymentProvider:   PaymentProviderStripe,
		Status:            common.TopUpStatusSuccess,
	}
	second := &TopUp{
		UserId:            secondUser.Id,
		Quota:             10,
		Money:             1,
		TradeNo:           "merchant-two",
		ProviderPaymentId: "pay_case",
		PaymentProvider:   PaymentProviderStripe,
		Status:            common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(first).Error)
	require.NoError(t, DB.Create(second).Error)
	require.Error(t, DB.Create(&TopUp{
		UserId:            secondUser.Id,
		Quota:             10,
		Money:             1,
		TradeNo:           "merchant-three",
		ProviderPaymentId: first.ProviderPaymentId,
		PaymentProvider:   PaymentProviderStripe,
		Status:            common.TopUpStatusSuccess,
	}).Error)

	require.NoError(t, ReverseTopUpByProviderPayment(
		PaymentProviderStripe,
		first.ProviderPaymentId,
		0,
		true,
	))
	var users []User
	require.NoError(t, DB.Where("id IN ?", []int{firstUser.Id, secondUser.Id}).
		Order("id asc").
		Find(&users).Error)
	require.Len(t, users, 2)
	assert.Equal(t, 90, users[0].Quota)
	assert.Equal(t, 100, users[1].Quota)
}

func TestTradeNumbersAreUniqueAndCaseExact(t *testing.T) {
	useCrossDatabaseIdentityTestDB(t, &TopUp{}, &SubscriptionOrder{})

	firstTopUp := &TopUp{TradeNo: "Trade_CASE"}
	secondTopUp := &TopUp{TradeNo: "trade_case"}
	require.NoError(t, DB.Create(firstTopUp).Error)
	require.NoError(t, DB.Create(secondTopUp).Error)
	require.Error(t, DB.Create(&TopUp{TradeNo: "Trade_CASE"}).Error)
	assert.Equal(t, firstTopUp.Id, GetTopUpByTradeNo("Trade_CASE").Id)
	assert.Equal(t, secondTopUp.Id, GetTopUpByTradeNo("trade_case").Id)
	assert.Nil(t, GetTopUpByTradeNo("TRADE_CASE"))

	firstOrder := &SubscriptionOrder{
		TradeNo:                "Subscription_CASE",
		PaymentProvider:        PaymentProviderStripe,
		ProviderSubscriptionId: "Sub_CASE",
	}
	secondOrder := &SubscriptionOrder{
		TradeNo:                "subscription_case",
		PaymentProvider:        PaymentProviderStripe,
		ProviderSubscriptionId: "sub_case",
	}
	require.NoError(t, DB.Create(firstOrder).Error)
	require.NoError(t, DB.Create(secondOrder).Error)
	require.Error(t, DB.Create(&SubscriptionOrder{
		TradeNo:         "Subscription_CASE",
		PaymentProvider: PaymentProviderStripe,
	}).Error)

	assert.Equal(
		t,
		firstOrder.Id,
		GetSubscriptionOrderByTradeNo("Subscription_CASE").Id,
	)
	assert.Equal(
		t,
		secondOrder.Id,
		GetSubscriptionOrderByTradeNo("subscription_case").Id,
	)
	assert.Equal(
		t,
		firstOrder.Id,
		GetSubscriptionOrderByProviderSubscription(
			PaymentProviderStripe,
			"Sub_CASE",
		).Id,
	)
	assert.Equal(
		t,
		secondOrder.Id,
		GetSubscriptionOrderByProviderSubscription(
			PaymentProviderStripe,
			"sub_case",
		).Id,
	)
	assert.Nil(t, GetSubscriptionOrderByProviderSubscription(
		PaymentProviderStripe,
		"SUB_CASE",
	))
}

func TestTopUpIdentityMigrationRejectsDuplicateTradeNumbers(t *testing.T) {
	type legacyTopUp struct {
		Id      int
		TradeNo string `gorm:"type:varchar(255)"`
	}

	db := useCrossDatabaseIdentityTestDB(t)
	require.NoError(t, db.Table("top_ups").AutoMigrate(&legacyTopUp{}))
	require.NoError(t, db.Table("top_ups").Create(&legacyTopUp{TradeNo: "duplicate"}).Error)
	require.NoError(t, db.Table("top_ups").Create(&legacyTopUp{TradeNo: "duplicate"}).Error)
	require.NoError(t, prepareCrossDatabaseIdentityColumns())
	require.NoError(t, db.AutoMigrate(&TopUp{}))

	require.Error(t, migrateTopUpIdentityHashes())
	var populated int64
	require.NoError(t, db.Model(&TopUp{}).Where("trade_no_hash IS NOT NULL").Count(&populated).Error)
	assert.Zero(t, populated)
}

func TestSubscriptionIdentityMigrationBackfillsExactProviderIdentifiers(t *testing.T) {
	type legacyOrder struct {
		Id                     int
		TradeNo                string `gorm:"type:varchar(255)"`
		PaymentMethod          string `gorm:"type:varchar(50)"`
		PaymentProvider        string `gorm:"type:varchar(50)"`
		ProviderSubscriptionId string `gorm:"type:varchar(255)"`
		ProviderPaymentId      string `gorm:"type:varchar(255)"`
	}
	type legacySubscription struct {
		Id                     int
		SubscriptionOrderId    int
		PaymentProvider        string `gorm:"type:varchar(50)"`
		ProviderSubscriptionId string `gorm:"type:varchar(255)"`
	}

	db := useCrossDatabaseIdentityTestDB(t)
	legacy := &legacyOrder{
		TradeNo:                " Trade_CASE ",
		PaymentMethod:          PaymentMethodStripe,
		PaymentProvider:        " STRIPE ",
		ProviderSubscriptionId: " Sub_CASE ",
		ProviderPaymentId:      " Pay_CASE ",
	}
	require.NoError(t, db.Table("subscription_orders").AutoMigrate(&legacyOrder{}))
	require.NoError(t, db.Table("subscription_orders").Create(legacy).Error)
	require.NoError(t, db.Table("user_subscriptions").AutoMigrate(&legacySubscription{}))
	require.NoError(t, db.Table("user_subscriptions").Create(&legacySubscription{
		SubscriptionOrderId:    legacy.Id,
		ProviderSubscriptionId: legacy.ProviderSubscriptionId,
	}).Error)
	require.NoError(t, prepareCrossDatabaseIdentityColumns())
	require.NoError(t, db.AutoMigrate(
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionProviderPayment{},
	))

	require.NoError(t, migrateSubscriptionIdentityHashes())

	var order SubscriptionOrder
	require.NoError(t, db.First(&order, legacy.Id).Error)
	assert.Equal(t, legacy.TradeNo, order.TradeNo)
	assert.Equal(t, PaymentProviderStripe, order.PaymentProvider)
	assert.Equal(t, legacy.ProviderSubscriptionId, order.ProviderSubscriptionId)
	assert.Equal(t, legacy.ProviderPaymentId, order.ProviderPaymentId)
	require.NotNil(t, order.TradeNoHash)
	require.NotNil(t, order.ProviderSubscriptionHash)

	resolved := GetSubscriptionOrderByProviderSubscription(
		PaymentProviderStripe,
		legacy.ProviderSubscriptionId,
	)
	require.NotNil(t, resolved)
	assert.Equal(t, order.Id, resolved.Id)
	assert.Nil(t, GetSubscriptionOrderByProviderSubscription(
		PaymentProviderStripe,
		"Sub_CASE",
	))

	var subscription UserSubscription
	require.NoError(t, db.First(&subscription).Error)
	assert.Equal(t, PaymentProviderStripe, subscription.PaymentProvider)
	assert.Equal(t, legacy.ProviderSubscriptionId, subscription.ProviderSubscriptionId)
	require.NotNil(t, subscription.ProviderSubscriptionHash)

	var paymentReference SubscriptionProviderPayment
	require.NoError(t, db.First(&paymentReference).Error)
	assert.Equal(t, legacy.ProviderPaymentId, paymentReference.ProviderPaymentId)
	require.NotNil(t, paymentReference.PaymentIdentityHash)
}

func TestSubscriptionIdentityMigrationRejectsDuplicateTradeNumbers(t *testing.T) {
	type legacyOrder struct {
		Id      int
		TradeNo string `gorm:"type:varchar(255)"`
	}

	db := useCrossDatabaseIdentityTestDB(t)
	require.NoError(t, db.Table("subscription_orders").AutoMigrate(&legacyOrder{}))
	require.NoError(t, db.Table("subscription_orders").Create(&legacyOrder{TradeNo: "duplicate"}).Error)
	require.NoError(t, db.Table("subscription_orders").Create(&legacyOrder{TradeNo: "duplicate"}).Error)
	require.NoError(t, prepareCrossDatabaseIdentityColumns())
	require.NoError(t, db.AutoMigrate(
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionProviderPayment{},
	))

	require.Error(t, migrateSubscriptionIdentityHashes())
	var populated int64
	require.NoError(t, db.Model(&SubscriptionOrder{}).
		Where("trade_no_hash IS NOT NULL").
		Count(&populated).Error)
	assert.Zero(t, populated)
}

func TestSubscriptionProviderPaymentIdentityIsUniqueAndCaseExact(t *testing.T) {
	useCrossDatabaseIdentityTestDB(t, &SubscriptionOrder{}, &SubscriptionProviderPayment{})
	firstOrder := &SubscriptionOrder{
		TradeNo:         "subscription-merchant-one",
		PaymentProvider: PaymentProviderStripe,
	}
	secondOrder := &SubscriptionOrder{
		TradeNo:         "subscription-merchant-two",
		PaymentProvider: PaymentProviderStripe,
	}
	require.NoError(t, DB.Create(firstOrder).Error)
	require.NoError(t, DB.Create(secondOrder).Error)
	require.NoError(t, bindSubscriptionProviderPaymentsTx(
		DB,
		PaymentProviderStripe,
		firstOrder.Id,
		[]string{"Pay_CASE"},
	))
	require.NoError(t, bindSubscriptionProviderPaymentsTx(
		DB,
		PaymentProviderStripe,
		secondOrder.Id,
		[]string{"pay_case"},
	))
	require.Error(t, bindSubscriptionProviderPaymentsTx(
		DB,
		PaymentProviderStripe,
		secondOrder.Id,
		[]string{"Pay_CASE"},
	))

	resolved, err := findSubscriptionOrderByProviderPaymentTx(
		DB,
		PaymentProviderStripe,
		"Pay_CASE",
	)
	require.NoError(t, err)
	assert.Equal(t, firstOrder.Id, resolved.Id)
}
