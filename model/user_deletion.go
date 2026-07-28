package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

var ErrUserDeletionBlocked = errors.New("user deletion is blocked by unresolved account activity")

const userDeletionGuardBatchSize = 100

// ensureUserDeletionSafeTx prevents removing the account record needed by
// durable billing work and asynchronous task settlement. Direct user
// references use indexed existence probes; JSON outboxes are inspected in
// bounded primary-key batches to remain portable across all supported
// databases.
func ensureUserDeletionSafeTx(tx *gorm.DB, userID int) error {
	if tx == nil {
		return errors.New("user deletion transaction is nil")
	}
	if userID <= 0 {
		return errors.New("user id is invalid")
	}

	// Serialize deletion with balance changes that lock the user row. This
	// prevents an already-enqueued wallet adjustment from crossing the guard
	// between the existence probes below and the account deletion.
	var user User
	if err := lockForUpdate(tx.Unscoped()).
		Select("id").
		Where("id = ?", userID).
		First(&user).Error; err != nil {
		return err
	}

	var reservation BillingReservation
	reservationQuery := tx.Select("id").
		Where(
			"user_id = ? AND (status IS NULL OR status NOT IN ?)",
			userID,
			[]string{billingReservationStatusSettled, billingReservationStatusRefunded},
		).
		Limit(1).
		Find(&reservation)
	if reservationQuery.Error != nil {
		return reservationQuery.Error
	}
	if reservationQuery.RowsAffected != 0 {
		return fmt.Errorf("%w: unresolved billing reservation %d", ErrUserDeletionBlocked, reservation.ID)
	}

	var preConsume SubscriptionPreConsumeRecord
	preConsumeQuery := tx.Select("id", "request_id").
		Where(
			"user_id = ? AND (status IS NULL OR status NOT IN ?)",
			userID,
			[]string{"settled", "refunded"},
		).
		Limit(1).
		Find(&preConsume)
	if preConsumeQuery.Error != nil {
		return preConsumeQuery.Error
	}
	if preConsumeQuery.RowsAffected != 0 {
		return fmt.Errorf("%w: unresolved subscription reservation %s", ErrUserDeletionBlocked, preConsume.RequestId)
	}

	var inviteeReward AffiliateReward
	inviteeRewardQuery := tx.Select("invitee_id").
		Where("invitee_id = ? AND (status IS NULL OR status <> ?)", userID, affiliateRewardStatusApplied).
		Limit(1).
		Find(&inviteeReward)
	if inviteeRewardQuery.Error != nil {
		return inviteeRewardQuery.Error
	}
	if inviteeRewardQuery.RowsAffected != 0 {
		return fmt.Errorf("%w: pending affiliate reward for invitee %d", ErrUserDeletionBlocked, userID)
	}

	var inviterReward AffiliateReward
	inviterRewardQuery := tx.Select("invitee_id").
		Where("inviter_id = ? AND (status IS NULL OR status <> ?)", userID, affiliateRewardStatusApplied).
		Limit(1).
		Find(&inviterReward)
	if inviterRewardQuery.Error != nil {
		return inviterRewardQuery.Error
	}
	if inviterRewardQuery.RowsAffected != 0 {
		return fmt.Errorf(
			"%w: pending affiliate reward for inviter %d from invitee %d",
			ErrUserDeletionBlocked,
			userID,
			inviterReward.InviteeId,
		)
	}

	var task Task
	taskQuery := tx.Select("id", "task_id").
		Where(
			"user_id = ? AND (status IS NULL OR status NOT IN ?)",
			userID,
			[]TaskStatus{TaskStatusSuccess, TaskStatusFailure},
		).
		Limit(1).
		Find(&task)
	if taskQuery.Error != nil {
		return taskQuery.Error
	}
	if taskQuery.RowsAffected != 0 {
		return fmt.Errorf("%w: nonterminal async task %s", ErrUserDeletionBlocked, task.TaskID)
	}

	var midjourneyTask Midjourney
	midjourneyQuery := tx.Select("id").
		Where(
			`user_id = ? AND (
				progress IS NULL OR progress <> ? OR
				(billing_purpose <> ? AND (
					billing_finalized IS NULL OR billing_finalized = ? OR
					((status IS NULL OR status <> ?) AND
						(billing_refunded IS NULL OR billing_refunded = ?))
				))
			)`,
			userID,
			"100%",
			"",
			false,
			"SUCCESS",
			false,
		).
		Limit(1).
		Find(&midjourneyTask)
	if midjourneyQuery.Error != nil {
		return midjourneyQuery.Error
	}
	if midjourneyQuery.RowsAffected != 0 {
		return fmt.Errorf("%w: nonterminal Midjourney task %d", ErrUserDeletionBlocked, midjourneyTask.Id)
	}

	var lastSystemTaskID int64
	for {
		var billingTasks []SystemTask
		if err := tx.Select("id", "task_id", "payload").
			Where(
				"id > ? AND type = ? AND (status IS NULL OR status <> ?)",
				lastSystemTaskID,
				SystemTaskTypeBillingAdjustment,
				SystemTaskStatusSucceeded,
			).
			Order("id asc").
			Limit(userDeletionGuardBatchSize).
			Find(&billingTasks).Error; err != nil {
			return err
		}
		for _, billingTask := range billingTasks {
			var adjustment BillingAdjustment
			if err := common.UnmarshalJsonStr(billingTask.Payload, &adjustment); err != nil {
				return fmt.Errorf("inspect pending billing adjustment %s: %w", billingTask.TaskID, err)
			}
			if adjustment.UserID == userID {
				return fmt.Errorf("%w: pending billing adjustment %s", ErrUserDeletionBlocked, billingTask.TaskID)
			}
		}
		if len(billingTasks) < userDeletionGuardBatchSize {
			break
		}
		lastSystemTaskID = billingTasks[len(billingTasks)-1].ID
	}

	var lastFinalizationID int64
	for {
		var finalizations []TaskBillingFinalization
		if err := tx.Select("id", "finalization_id", "payload").
			Where(
				"id > ? AND (main_status IS NULL OR main_status <> ? OR log_status IS NULL OR log_status <> ?)",
				lastFinalizationID,
				taskBillingFinalizationSucceeded,
				taskBillingFinalizationSucceeded,
			).
			Order("id asc").
			Limit(userDeletionGuardBatchSize).
			Find(&finalizations).Error; err != nil {
			return err
		}
		for _, finalization := range finalizations {
			var payload TaskBillingFinalizationPayload
			if err := common.UnmarshalJsonStr(finalization.Payload, &payload); err != nil {
				return fmt.Errorf("inspect pending billing finalization %s: %w", finalization.FinalizationID, err)
			}
			if payload.Adjustment.UserID == userID || payload.Log.UserID == userID {
				return fmt.Errorf("%w: pending billing finalization %s", ErrUserDeletionBlocked, finalization.FinalizationID)
			}
		}
		if len(finalizations) < userDeletionGuardBatchSize {
			break
		}
		lastFinalizationID = finalizations[len(finalizations)-1].ID
	}

	return nil
}
