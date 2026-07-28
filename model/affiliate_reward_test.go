package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setAffiliateRewardTestQuotas(t *testing.T, newUser int, invitee int, inviter int) {
	t.Helper()
	originalNewUser := common.QuotaForNewUser
	originalInvitee := common.QuotaForInvitee
	originalInviter := common.QuotaForInviter
	common.QuotaForNewUser = newUser
	common.QuotaForInvitee = invitee
	common.QuotaForInviter = inviter
	t.Cleanup(func() {
		common.QuotaForNewUser = originalNewUser
		common.QuotaForInvitee = originalInvitee
		common.QuotaForInviter = originalInviter
	})
}

func TestAffiliateRewardFinalizationIsExactlyOncePerInvitee(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{}, &Log{})
	setAffiliateRewardTestQuotas(t, 100, 40, 25)

	inviter := User{Username: "affiliate-inviter", Status: common.UserStatusEnabled, AffCode: "inviter-code"}
	require.NoError(t, db.Create(&inviter).Error)
	invitee := User{
		Username:  "affiliate-invitee",
		Status:    common.UserStatusEnabled,
		Role:      common.RoleCommonUser,
		InviterId: inviter.Id,
	}
	require.NoError(t, invitee.Insert(inviter.Id))

	// Both public finalization paths may be retried by their callers. The
	// invitee-keyed record must make every retry a no-op.
	invitee.FinishInsert(inviter.Id)
	invitee.FinalizeOAuthUserCreation(inviter.Id)
	require.NoError(t, ProcessPendingAffiliateRewards(10))

	var persistedInvitee User
	require.NoError(t, db.First(&persistedInvitee, invitee.Id).Error)
	assert.Equal(t, 140, persistedInvitee.Quota)
	var persistedInviter User
	require.NoError(t, db.First(&persistedInviter, inviter.Id).Error)
	assert.Equal(t, 1, persistedInviter.AffCount)
	assert.Equal(t, 25, persistedInviter.AffQuota)
	assert.Equal(t, 25, persistedInviter.AffHistoryQuota)

	var reward AffiliateReward
	require.NoError(t, db.First(&reward, "invitee_id = ?", invitee.Id).Error)
	assert.Equal(t, affiliateRewardStatusApplied, reward.Status)
	assert.Equal(t, inviter.Id, reward.InviterId)
	assert.Equal(t, 40, reward.InviteeQuota)
	assert.Equal(t, 25, reward.InviterQuota)
	assert.Positive(t, reward.AppliedAt)

	var inviteeRewardLogs int64
	require.NoError(t, db.Model(&Log{}).
		Where("user_id = ? AND content LIKE ?", invitee.Id, "使用邀请码赠送%").
		Count(&inviteeRewardLogs).Error)
	assert.Equal(t, int64(1), inviteeRewardLogs)
	var inviterRewardLogs int64
	require.NoError(t, db.Model(&Log{}).
		Where("user_id = ? AND content LIKE ?", inviter.Id, "邀请用户赠送%").
		Count(&inviterRewardLogs).Error)
	assert.Equal(t, int64(1), inviterRewardLogs)
}

func TestAffiliateRewardFailureRollsBackBothSidesAndRemainsRetryable(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{}, &Log{})
	setAffiliateRewardTestQuotas(t, 100, 40, 25)

	inviter := User{
		Username:        "affiliate-overflow-inviter",
		Status:          common.UserStatusEnabled,
		AffCode:         "overflow-code",
		AffQuota:        common.MaxQuota - 10,
		AffHistoryQuota: common.MaxQuota - 10,
	}
	require.NoError(t, db.Create(&inviter).Error)
	invitee := User{Username: "affiliate-retry-invitee", Status: common.UserStatusEnabled}

	// Registration still succeeds, matching the existing API behavior, while
	// the all-or-nothing reward transaction remains pending after overflow.
	require.NoError(t, invitee.Insert(inviter.Id))
	var persistedInvitee User
	require.NoError(t, db.First(&persistedInvitee, invitee.Id).Error)
	assert.Equal(t, 100, persistedInvitee.Quota)
	var persistedInviter User
	require.NoError(t, db.First(&persistedInviter, inviter.Id).Error)
	assert.Zero(t, persistedInviter.AffCount)
	assert.Equal(t, common.MaxQuota-10, persistedInviter.AffQuota)
	assert.Equal(t, common.MaxQuota-10, persistedInviter.AffHistoryQuota)
	var reward AffiliateReward
	require.NoError(t, db.First(&reward, "invitee_id = ?", invitee.Id).Error)
	assert.Equal(t, affiliateRewardStatusPending, reward.Status)

	// Once the blocking balance is repaired, the durable worker awards both
	// sides once. Reprocessing cannot repeat either credit.
	require.NoError(t, db.Model(&User{}).Where("id = ?", inviter.Id).Updates(map[string]interface{}{
		"aff_quota":   0,
		"aff_history": 0,
	}).Error)
	require.NoError(t, ProcessPendingAffiliateRewards(10))
	require.NoError(t, ProcessPendingAffiliateRewards(10))

	require.NoError(t, db.First(&persistedInvitee, invitee.Id).Error)
	assert.Equal(t, 140, persistedInvitee.Quota)
	require.NoError(t, db.First(&persistedInviter, inviter.Id).Error)
	assert.Equal(t, 1, persistedInviter.AffCount)
	assert.Equal(t, 25, persistedInviter.AffQuota)
	assert.Equal(t, 25, persistedInviter.AffHistoryQuota)
	require.NoError(t, db.First(&reward, "invitee_id = ?", invitee.Id).Error)
	assert.Equal(t, affiliateRewardStatusApplied, reward.Status)
}

func TestAffiliateRewardRecordRollsBackWithUserCreation(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{})
	setAffiliateRewardTestQuotas(t, 100, 40, 25)
	inviter := User{Username: "affiliate-rollback-inviter", Status: common.UserStatusEnabled, AffCode: "rollback-code"}
	require.NoError(t, db.Create(&inviter).Error)
	invitee := User{Username: "affiliate-rollback-invitee", Status: common.UserStatusEnabled}
	expectedErr := errors.New("abort outer registration")

	err := db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, invitee.InsertWithTx(tx, inviter.Id))
		return expectedErr
	})
	require.ErrorIs(t, err, expectedErr)

	var inviteeCount int64
	require.NoError(t, db.Model(&User{}).Where("username = ?", invitee.Username).Count(&inviteeCount).Error)
	assert.Zero(t, inviteeCount)
	var rewardCount int64
	require.NoError(t, db.Model(&AffiliateReward{}).Count(&rewardCount).Error)
	assert.Zero(t, rewardCount)
}

func TestUserInsertRejectsMissingInviterWithoutLeavingPendingReward(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{})
	setAffiliateRewardTestQuotas(t, 100, 40, 25)
	invitee := User{
		Username: "affiliate-missing-inviter",
		Status:   common.UserStatusEnabled,
	}

	err := invitee.Insert(9999)

	require.ErrorContains(t, err, "lock affiliate reward inviter")
	var inviteeCount, rewardCount int64
	require.NoError(t, db.Model(&User{}).
		Where("username = ?", invitee.Username).
		Count(&inviteeCount).Error)
	require.NoError(t, db.Model(&AffiliateReward{}).Count(&rewardCount).Error)
	assert.Zero(t, inviteeCount)
	assert.Zero(t, rewardCount)
}

func TestUserInsertCanonicalizesInviterId(t *testing.T) {
	t.Run("standalone insert", func(t *testing.T) {
		db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{}, &Log{})
		setAffiliateRewardTestQuotas(t, 100, 40, 25)
		inviter := User{Username: "canonical-standalone-inviter", Status: common.UserStatusEnabled, AffCode: "canonical-standalone-code"}
		require.NoError(t, db.Create(&inviter).Error)
		invitee := User{
			Username: "canonical-standalone-invitee", Status: common.UserStatusEnabled,
			InviterId: inviter.Id + 1000,
		}

		require.NoError(t, invitee.Insert(inviter.Id))

		var persisted User
		require.NoError(t, db.First(&persisted, invitee.Id).Error)
		assert.Equal(t, inviter.Id, persisted.InviterId)
		var reward AffiliateReward
		require.NoError(t, db.First(&reward, "invitee_id = ?", invitee.Id).Error)
		assert.Equal(t, inviter.Id, reward.InviterId)
	})

	t.Run("transactional OAuth-style insert", func(t *testing.T) {
		db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{})
		setAffiliateRewardTestQuotas(t, 100, 40, 25)
		inviter := User{Username: "canonical-oauth-inviter", Status: common.UserStatusEnabled, AffCode: "canonical-oauth-code"}
		require.NoError(t, db.Create(&inviter).Error)
		invitee := User{Username: "canonical-oauth-invitee", Status: common.UserStatusEnabled}

		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return invitee.InsertWithTx(tx, inviter.Id)
		}))

		var persisted User
		require.NoError(t, db.First(&persisted, invitee.Id).Error)
		assert.Equal(t, inviter.Id, persisted.InviterId)
		var reward AffiliateReward
		require.NoError(t, db.First(&reward, "invitee_id = ?", invitee.Id).Error)
		assert.Equal(t, inviter.Id, reward.InviterId)
		assert.Equal(t, affiliateRewardStatusPending, reward.Status)
	})
}

func TestAffiliateRewardUsesCreationTimeQuotaSnapshot(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{}, &Log{})
	setAffiliateRewardTestQuotas(t, 100, 40, 25)
	inviter := User{Username: "affiliate-snapshot-inviter", Status: common.UserStatusEnabled, AffCode: "snapshot-code"}
	require.NoError(t, db.Create(&inviter).Error)
	invitee := User{Username: "affiliate-snapshot-invitee", Status: common.UserStatusEnabled}

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return invitee.InsertWithTx(tx, inviter.Id)
	}))
	common.QuotaForInvitee = 400
	common.QuotaForInviter = 250
	invitee.FinalizeOAuthUserCreation(inviter.Id)

	var persistedInvitee User
	require.NoError(t, db.First(&persistedInvitee, invitee.Id).Error)
	assert.Equal(t, 140, persistedInvitee.Quota)
	var persistedInviter User
	require.NoError(t, db.First(&persistedInviter, inviter.Id).Error)
	assert.Equal(t, 25, persistedInviter.AffQuota)
}

func TestAffiliateRewardQuotaSnapshotBounds(t *testing.T) {
	tests := []struct {
		name               string
		inviteeReward      int
		inviterReward      int
		wantRecord         bool
		wantInviteeReward  int
		wantInviterReward  int
		wantInviterInvites int
	}{
		{
			name:               "maximum values remain valid",
			inviteeReward:      common.MaxQuota,
			inviterReward:      common.MaxQuota,
			wantRecord:         true,
			wantInviteeReward:  common.MaxQuota,
			wantInviterReward:  common.MaxQuota,
			wantInviterInvites: 1,
		},
		{
			name:               "oversized invitee reward is disabled independently",
			inviteeReward:      common.MaxQuota + 1,
			inviterReward:      25,
			wantRecord:         true,
			wantInviterReward:  25,
			wantInviterInvites: 1,
		},
		{
			name:              "oversized inviter reward is disabled independently",
			inviteeReward:     40,
			inviterReward:     common.MaxQuota + 1,
			wantRecord:        true,
			wantInviteeReward: 40,
		},
		{
			name:          "two invalid rewards do not create permanent pending work",
			inviteeReward: common.MaxQuota + 1,
			inviterReward: common.MaxQuota + 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{}, &Log{})
			setAffiliateRewardTestQuotas(t, 0, test.inviteeReward, test.inviterReward)
			inviter := User{Username: "bounds-inviter", Status: common.UserStatusEnabled, AffCode: "bounds-code"}
			require.NoError(t, db.Create(&inviter).Error)
			invitee := User{Username: "bounds-invitee", Status: common.UserStatusEnabled}

			require.NoError(t, invitee.Insert(inviter.Id))

			var persistedInvitee User
			require.NoError(t, db.First(&persistedInvitee, invitee.Id).Error)
			assert.Equal(t, test.wantInviteeReward, persistedInvitee.Quota)
			var persistedInviter User
			require.NoError(t, db.First(&persistedInviter, inviter.Id).Error)
			assert.Equal(t, test.wantInviterReward, persistedInviter.AffQuota)
			assert.Equal(t, test.wantInviterReward, persistedInviter.AffHistoryQuota)
			assert.Equal(t, test.wantInviterInvites, persistedInviter.AffCount)

			var reward AffiliateReward
			err := db.First(&reward, "invitee_id = ?", invitee.Id).Error
			if !test.wantRecord {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantInviteeReward, reward.InviteeQuota)
			assert.Equal(t, test.wantInviterReward, reward.InviterQuota)
			assert.Equal(t, affiliateRewardStatusApplied, reward.Status)
		})
	}
}

func TestEnqueueAffiliateRewardValidatesCanonicalConflictRecord(t *testing.T) {
	tests := []struct {
		name        string
		record      AffiliateReward
		errorDetail string
	}{
		{
			name: "different inviter",
			record: AffiliateReward{
				InviteeId: 91, InviterId: 8, InviteeQuota: 40, InviterQuota: 25,
				Status: affiliateRewardStatusPending,
			},
			errorDetail: "already belongs to inviter",
		},
		{
			name: "different quota snapshot",
			record: AffiliateReward{
				InviteeId: 91, InviterId: 7, InviteeQuota: 41, InviterQuota: 25,
				Status: affiliateRewardStatusPending,
			},
			errorDetail: "different quota snapshot",
		},
		{
			name: "invalid status",
			record: AffiliateReward{
				InviteeId: 91, InviterId: 7, InviteeQuota: 40, InviterQuota: 25,
				Status: "corrupt",
			},
			errorDetail: "invalid status",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupBillingAdjustmentTestDB(t, &User{}, &AffiliateReward{})
			setAffiliateRewardTestQuotas(t, 0, 40, 25)
			require.NoError(t, db.Create(&User{
				Id:       7,
				Username: "canonical-conflict-inviter",
				Status:   common.UserStatusEnabled,
				AffCode:  "canonical-conflict-code",
			}).Error)
			test.record.CreatedAt = common.GetTimestamp()
			test.record.UpdatedAt = test.record.CreatedAt
			require.NoError(t, db.Create(&test.record).Error)

			err := db.Transaction(func(tx *gorm.DB) error {
				return enqueueAffiliateRewardWithTx(tx, 91, 7)
			})

			require.ErrorContains(t, err, test.errorDetail)
		})
	}
}
