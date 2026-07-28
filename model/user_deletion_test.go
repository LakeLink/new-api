package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserDeletionGuardTest(t *testing.T) (*gorm.DB, User) {
	t.Helper()
	db := setupBillingAdjustmentTestDB(
		t,
		&User{},
		&UserOAuthBinding{},
		&BillingReservation{},
		&SubscriptionPreConsumeRecord{},
		&SystemTask{},
		&TaskBillingFinalization{},
		&AffiliateReward{},
		&Task{},
		&Midjourney{},
		&BuiltInOAuthIdentity{},
		&BrowserSession{},
	)
	user := User{
		Username: "deletion-guard-user",
		Password: "SecurePassword123",
		AffCode:  "deletion-guard-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	return db, user
}

func TestUserDeletionRejectsUnresolvedAccountActivity(t *testing.T) {
	tests := []struct {
		name       string
		errorMatch string
		seed       func(t *testing.T, db *gorm.DB, user User)
	}{
		{
			name:       "pending billing reservation",
			errorMatch: "billing reservation",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&BillingReservation{
					RequestID:     "delete-pending-reservation",
					UserID:        user.Id,
					FundingSource: BillingAdjustmentWallet,
					Status:        billingReservationStatusPending,
				}).Error)
			},
		},
		{
			name:       "legacy billing reservation without status",
			errorMatch: "billing reservation",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				reservation := BillingReservation{
					RequestID:     "delete-legacy-reservation",
					UserID:        user.Id,
					FundingSource: BillingAdjustmentWallet,
					Status:        billingReservationStatusPending,
				}
				require.NoError(t, db.Create(&reservation).Error)
				require.NoError(t, db.Model(&reservation).Update("status", "").Error)
			},
		},
		{
			name:       "pending billing adjustment",
			errorMatch: "billing adjustment",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				payload, err := common.Marshal(BillingAdjustment{
					RequestID:     "delete-pending-adjustment",
					Kind:          BillingAdjustmentSettle,
					FundingSource: BillingAdjustmentWallet,
					UserID:        user.Id,
				})
				require.NoError(t, err)
				require.NoError(t, db.Create(&SystemTask{
					TaskID:  "delete-pending-adjustment",
					Type:    SystemTaskTypeBillingAdjustment,
					Status:  SystemTaskStatusPending,
					Payload: string(payload),
				}).Error)
			},
		},
		{
			name:       "failed billing adjustment",
			errorMatch: "billing adjustment",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				payload, err := common.Marshal(BillingAdjustment{
					RequestID:     "delete-failed-adjustment",
					Kind:          BillingAdjustmentSettle,
					FundingSource: BillingAdjustmentWallet,
					UserID:        user.Id,
				})
				require.NoError(t, err)
				require.NoError(t, db.Create(&SystemTask{
					TaskID:  "delete-failed-adjustment",
					Type:    SystemTaskTypeBillingAdjustment,
					Status:  SystemTaskStatusFailed,
					Payload: string(payload),
				}).Error)
			},
		},
		{
			name:       "consumed subscription reservation",
			errorMatch: "subscription reservation",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{
					RequestId:   "delete-consumed-subscription-reservation",
					UserId:      user.Id,
					PreConsumed: 10,
					Status:      "consumed",
				}).Error)
			},
		},
		{
			name:       "pending billing finalization main phase",
			errorMatch: "billing finalization",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				payload, err := common.Marshal(TaskBillingFinalizationPayload{
					Adjustment: BillingAdjustment{UserID: user.Id},
					Log:        TaskBillingFinalizationLog{UserID: user.Id},
				})
				require.NoError(t, err)
				require.NoError(t, db.Create(&TaskBillingFinalization{
					FinalizationID: "delete-pending-main-finalization",
					Payload:        string(payload),
					MainStatus:     taskBillingFinalizationPending,
					LogStatus:      taskBillingFinalizationSucceeded,
				}).Error)
			},
		},
		{
			name:       "pending billing finalization log phase",
			errorMatch: "billing finalization",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				payload, err := common.Marshal(TaskBillingFinalizationPayload{
					Adjustment: BillingAdjustment{UserID: user.Id},
					Log:        TaskBillingFinalizationLog{UserID: user.Id},
				})
				require.NoError(t, err)
				require.NoError(t, db.Create(&TaskBillingFinalization{
					FinalizationID: "delete-pending-log-finalization",
					Payload:        string(payload),
					MainStatus:     taskBillingFinalizationSucceeded,
					LogStatus:      taskBillingFinalizationProcessing,
				}).Error)
			},
		},
		{
			name:       "pending affiliate reward as invitee",
			errorMatch: "affiliate reward",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&AffiliateReward{
					InviteeId: user.Id,
					InviterId: user.Id + 1000,
					Status:    affiliateRewardStatusPending,
				}).Error)
			},
		},
		{
			name:       "pending affiliate reward as inviter",
			errorMatch: "affiliate reward",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&AffiliateReward{
					InviteeId: user.Id + 1000,
					InviterId: user.Id,
					Status:    affiliateRewardStatusPending,
				}).Error)
			},
		},
		{
			name:       "invalid affiliate reward state",
			errorMatch: "affiliate reward",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&AffiliateReward{
					InviteeId: user.Id,
					InviterId: user.Id + 1000,
					Status:    "invalid",
				}).Error)
			},
		},
		{
			name:       "nonterminal async task",
			errorMatch: "async task",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&Task{
					TaskID: "delete-nonterminal-async-task",
					UserId: user.Id,
					Status: TaskStatusInProgress,
				}).Error)
			},
		},
		{
			name:       "nonterminal Midjourney task",
			errorMatch: "Midjourney task",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&Midjourney{
					UserId:   user.Id,
					MjId:     "delete-nonterminal-midjourney-task",
					Status:   "IN_PROGRESS",
					Progress: "50%",
				}).Error)
			},
		},
		{
			name:       "unfinalized successful Midjourney billing",
			errorMatch: "Midjourney task",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&Midjourney{
					UserId:           user.Id,
					MjId:             "delete-unfinalized-successful-midjourney-task",
					Status:           "SUCCESS",
					Progress:         "100%",
					BillingPurpose:   "midjourney-submit:IMAGINE",
					BillingFinalized: false,
				}).Error)
			},
		},
		{
			name:       "legacy null Midjourney billing markers",
			errorMatch: "Midjourney task",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				task := Midjourney{
					UserId:         user.Id,
					MjId:           "delete-null-midjourney-billing-markers",
					Status:         "FAILURE",
					Progress:       "100%",
					BillingPurpose: "midjourney-submit:IMAGINE",
				}
				require.NoError(t, db.Create(&task).Error)
				require.NoError(t, db.Model(&task).Updates(map[string]interface{}{
					"billing_finalized": nil,
					"billing_refunded":  nil,
				}).Error)
			},
		},
		{
			name:       "unrecorded failed Midjourney refund",
			errorMatch: "Midjourney task",
			seed: func(t *testing.T, db *gorm.DB, user User) {
				t.Helper()
				require.NoError(t, db.Create(&Midjourney{
					UserId:           user.Id,
					MjId:             "delete-unrecorded-midjourney-refund",
					Status:           "FAILURE",
					Progress:         "100%",
					BillingPurpose:   "midjourney-submit:IMAGINE",
					BillingFinalized: true,
					BillingRefunded:  false,
				}).Error)
			},
		},
	}

	deletionModes := []struct {
		name   string
		delete func(userID int) error
	}{
		{name: "soft", delete: DeleteUserById},
		{name: "hard", delete: HardDeleteUserById},
	}

	for _, test := range tests {
		for _, deletionMode := range deletionModes {
			t.Run(test.name+"/"+deletionMode.name, func(t *testing.T) {
				db, user := setupUserDeletionGuardTest(t)
				test.seed(t, db, user)

				err := deletionMode.delete(user.Id)
				require.ErrorIs(t, err, ErrUserDeletionBlocked)
				assert.ErrorContains(t, err, test.errorMatch)

				var persisted User
				require.NoError(t, db.Unscoped().First(&persisted, user.Id).Error)
				assert.False(t, persisted.DeletedAt.Valid)
			})
		}
	}
}

func TestUserDeletionAllowsTerminalAccountActivity(t *testing.T) {
	deletionModes := []struct {
		name   string
		delete func(userID int) error
		hard   bool
	}{
		{name: "soft", delete: DeleteUserById},
		{name: "hard", delete: HardDeleteUserById, hard: true},
	}

	for _, deletionMode := range deletionModes {
		t.Run(deletionMode.name, func(t *testing.T) {
			db, user := setupUserDeletionGuardTest(t)

			require.NoError(t, db.Create(&BillingReservation{
				RequestID:     "delete-settled-reservation",
				UserID:        user.Id,
				FundingSource: BillingAdjustmentWallet,
				Status:        billingReservationStatusSettled,
			}).Error)
			require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{
				RequestId:   "delete-settled-subscription-reservation",
				UserId:      user.Id,
				PreConsumed: 10,
				Status:      "settled",
			}).Error)
			require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{
				RequestId:   "delete-refunded-subscription-reservation",
				UserId:      user.Id,
				PreConsumed: 10,
				Status:      "refunded",
			}).Error)
			require.NoError(t, db.Create(&BillingReservation{
				RequestID:     "delete-refunded-reservation",
				UserID:        user.Id,
				FundingSource: BillingAdjustmentWallet,
				Status:        billingReservationStatusRefunded,
			}).Error)

			adjustmentPayload, err := common.Marshal(BillingAdjustment{UserID: user.Id})
			require.NoError(t, err)
			require.NoError(t, db.Create(&SystemTask{
				TaskID:  "delete-succeeded-adjustment",
				Type:    SystemTaskTypeBillingAdjustment,
				Status:  SystemTaskStatusSucceeded,
				Payload: string(adjustmentPayload),
			}).Error)

			finalizationPayload, err := common.Marshal(TaskBillingFinalizationPayload{
				Adjustment: BillingAdjustment{UserID: user.Id},
				Log:        TaskBillingFinalizationLog{UserID: user.Id},
			})
			require.NoError(t, err)
			require.NoError(t, db.Create(&TaskBillingFinalization{
				FinalizationID: "delete-succeeded-finalization",
				Payload:        string(finalizationPayload),
				MainStatus:     taskBillingFinalizationSucceeded,
				LogStatus:      taskBillingFinalizationSucceeded,
			}).Error)

			require.NoError(t, db.Create(&AffiliateReward{
				InviteeId: user.Id,
				InviterId: user.Id + 1000,
				Status:    affiliateRewardStatusApplied,
			}).Error)
			require.NoError(t, db.Create(&AffiliateReward{
				InviteeId: user.Id + 1001,
				InviterId: user.Id,
				Status:    affiliateRewardStatusApplied,
			}).Error)

			require.NoError(t, db.Create(&Task{
				TaskID: "delete-successful-async-task",
				UserId: user.Id,
				Status: TaskStatusSuccess,
			}).Error)
			require.NoError(t, db.Create(&Task{
				TaskID: "delete-failed-async-task",
				UserId: user.Id,
				Status: TaskStatusFailure,
			}).Error)
			require.NoError(t, db.Create(&Midjourney{
				UserId:   user.Id,
				MjId:     "delete-successful-midjourney-task",
				Status:   "SUCCESS",
				Progress: "100%",
			}).Error)
			require.NoError(t, db.Create(&Midjourney{
				UserId:   user.Id,
				MjId:     "delete-failed-midjourney-task",
				Status:   "FAILURE",
				Progress: "100%",
			}).Error)

			require.NoError(t, deletionMode.delete(user.Id))

			var persisted User
			err = db.Unscoped().First(&persisted, user.Id).Error
			if deletionMode.hard {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			} else {
				require.NoError(t, err)
				assert.True(t, persisted.DeletedAt.Valid)
			}
		})
	}
}
