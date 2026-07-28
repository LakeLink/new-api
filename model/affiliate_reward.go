package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	affiliateRewardStatusPending = "pending"
	affiliateRewardStatusApplied = "applied"
)

// AffiliateReward is the durable, per-invitee idempotency record for referral
// rewards. Reward amounts are snapshotted when the user is created so a later
// settings change cannot alter or repeat an already accepted registration.
type AffiliateReward struct {
	InviteeId    int    `json:"invitee_id" gorm:"primaryKey;autoIncrement:false"`
	InviterId    int    `json:"inviter_id" gorm:"not null;index"`
	InviteeQuota int    `json:"invitee_quota" gorm:"not null"`
	InviterQuota int    `json:"inviter_quota" gorm:"not null"`
	Status       string `json:"status" gorm:"type:varchar(16);not null;index"`
	CreatedAt    int64  `json:"created_at" gorm:"type:bigint;not null"`
	UpdatedAt    int64  `json:"updated_at" gorm:"type:bigint;not null"`
	AppliedAt    int64  `json:"applied_at" gorm:"type:bigint;not null"`
}

// enqueueAffiliateRewardWithTx writes the pending reward in the same
// transaction as the new user. A process crash after user creation therefore
// leaves durable work for ProcessPendingAffiliateRewards instead of losing the
// referral award.
func enqueueAffiliateRewardWithTx(tx *gorm.DB, inviteeId int, inviterId int) error {
	if tx == nil {
		return errors.New("affiliate reward transaction is nil")
	}
	if inviteeId <= 0 || inviterId <= 0 {
		return nil
	}
	inviteeQuota := snapshotAffiliateRewardQuota("invitee", common.GetLegacyOptionInt("QuotaForInvitee", &common.QuotaForInvitee))
	inviterQuota := snapshotAffiliateRewardQuota("inviter", common.GetLegacyOptionInt("QuotaForInviter", &common.QuotaForInviter))
	if inviteeQuota == 0 && inviterQuota == 0 {
		return nil
	}

	// Serialize the durable reward with inviter deletion. If deletion wins, the
	// registration transaction rolls back instead of committing a reward that
	// can never credit its inviter. If registration wins, deletion observes the
	// pending reward and fails closed until it is applied.
	var inviter User
	if err := lockForUpdate(tx).
		Select("id").
		Where("id = ?", inviterId).
		First(&inviter).Error; err != nil {
		return fmt.Errorf("lock affiliate reward inviter %d: %w", inviterId, err)
	}

	now := common.GetTimestamp()
	reward := AffiliateReward{
		InviteeId:    inviteeId,
		InviterId:    inviterId,
		InviteeQuota: inviteeQuota,
		InviterQuota: inviterQuota,
		Status:       affiliateRewardStatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "invitee_id"}},
		DoNothing: true,
	}).Create(&reward)
	if result.Error != nil {
		return result.Error
	}

	var existing AffiliateReward
	if err := lockForUpdate(tx).Where("invitee_id = ?", inviteeId).First(&existing).Error; err != nil {
		return err
	}
	if existing.InviterId != inviterId {
		return fmt.Errorf("affiliate reward for invitee %d already belongs to inviter %d", inviteeId, existing.InviterId)
	}
	if existing.InviteeQuota != inviteeQuota || existing.InviterQuota != inviterQuota {
		return fmt.Errorf("affiliate reward for invitee %d already has a different quota snapshot", inviteeId)
	}
	if existing.Status != affiliateRewardStatusPending && existing.Status != affiliateRewardStatusApplied {
		return fmt.Errorf("affiliate reward for invitee %d has invalid status %q", inviteeId, existing.Status)
	}
	return nil
}

func snapshotAffiliateRewardQuota(kind string, quota int) int {
	if quota <= 0 {
		return 0
	}
	if quota > common.MaxQuota {
		common.SysError(fmt.Sprintf("%s affiliate reward quota exceeds the supported range and was ignored: %d", kind, quota))
		return 0
	}
	return quota
}

// applyAffiliateReward atomically credits both sides and marks the durable
// record applied. Any failure rolls back both user updates and leaves the
// record pending for a later retry.
func applyAffiliateReward(inviteeId int, expectedInviterId int) (AffiliateReward, bool, error) {
	var reward AffiliateReward
	applied := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("invitee_id = ?", inviteeId).First(&reward).Error; err != nil {
			return err
		}
		if expectedInviterId > 0 && reward.InviterId != expectedInviterId {
			return fmt.Errorf("affiliate reward inviter mismatch for invitee %d: got %d, expected %d", inviteeId, reward.InviterId, expectedInviterId)
		}
		if reward.Status == affiliateRewardStatusApplied {
			return nil
		}
		if reward.Status != affiliateRewardStatusPending {
			return fmt.Errorf("affiliate reward for invitee %d has invalid status %q", inviteeId, reward.Status)
		}
		if reward.InviteeQuota < 0 || reward.InviteeQuota > common.MaxQuota {
			return fmt.Errorf("invitee reward quota is outside the supported range: %d", reward.InviteeQuota)
		}
		if reward.InviterQuota < 0 || reward.InviterQuota > common.MaxQuota {
			return fmt.Errorf("inviter reward quota is outside the supported range: %d", reward.InviterQuota)
		}

		if reward.InviteeQuota > 0 {
			result := tx.Model(&User{}).
				Where("id = ? AND quota <= ?", reward.InviteeId, common.MaxQuota-reward.InviteeQuota).
				Update("quota", gorm.Expr("quota + ?", reward.InviteeQuota))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("invitee reward would overflow or user %d does not exist", reward.InviteeId)
			}
		}

		if reward.InviterQuota > 0 {
			result := tx.Model(&User{}).
				Where("id = ? AND aff_count < ? AND aff_quota <= ? AND aff_history <= ?",
					reward.InviterId, common.MaxQuota, common.MaxQuota-reward.InviterQuota, common.MaxQuota-reward.InviterQuota).
				Updates(map[string]interface{}{
					"aff_count":   gorm.Expr("aff_count + 1"),
					"aff_quota":   gorm.Expr("aff_quota + ?", reward.InviterQuota),
					"aff_history": gorm.Expr("aff_history + ?", reward.InviterQuota),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("inviter reward would overflow or user %d does not exist", reward.InviterId)
			}
		}

		now := common.GetTimestamp()
		result := tx.Model(&AffiliateReward{}).
			Where("invitee_id = ? AND status = ?", reward.InviteeId, affiliateRewardStatusPending).
			Updates(map[string]interface{}{
				"status":     affiliateRewardStatusApplied,
				"updated_at": now,
				"applied_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("affiliate reward for invitee %d was concurrently modified", reward.InviteeId)
		}
		reward.Status = affiliateRewardStatusApplied
		reward.UpdatedAt = now
		reward.AppliedAt = now
		applied = true
		return nil
	})
	if err != nil {
		return AffiliateReward{}, false, err
	}

	if err := invalidateUserCache(reward.InviteeId); err != nil {
		common.SysLog("failed to invalidate invitee cache after affiliate reward: " + err.Error())
	}
	if err := invalidateUserCache(reward.InviterId); err != nil {
		common.SysLog("failed to invalidate inviter cache after affiliate reward: " + err.Error())
	}
	if applied {
		if reward.InviteeQuota > 0 {
			RecordLog(reward.InviteeId, LogTypeSystem, fmt.Sprintf("使用邀请码赠送 %s", logger.LogQuota(reward.InviteeQuota)))
		}
		if reward.InviterQuota > 0 {
			RecordLog(reward.InviterId, LogTypeSystem, fmt.Sprintf("邀请用户赠送 %s", logger.LogQuota(reward.InviterQuota)))
		}
	}
	return reward, applied, nil
}

func finalizeAffiliateReward(inviteeId int, inviterId int) error {
	if inviteeId <= 0 || inviterId <= 0 {
		return nil
	}
	_, _, err := applyAffiliateReward(inviteeId, inviterId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// No record means affiliate rewards were disabled when the user was
		// created. Do not apply a later configuration retroactively.
		return nil
	}
	return err
}

// ProcessPendingAffiliateRewards retries durable awards left behind by a
// transient database failure or a process shutdown between user commit and
// post-registration finalization.
func ProcessPendingAffiliateRewards(limit int) error {
	if limit <= 0 {
		limit = 100
	}
	var rewards []AffiliateReward
	if err := DB.Where("status = ?", affiliateRewardStatusPending).
		Order("updated_at asc, invitee_id asc").Limit(limit).Find(&rewards).Error; err != nil {
		return err
	}
	var processErrors []error
	for _, reward := range rewards {
		if _, _, err := applyAffiliateReward(reward.InviteeId, reward.InviterId); err != nil {
			processErrors = append(processErrors, fmt.Errorf("invitee %d: %w", reward.InviteeId, err))
			// Move a temporarily blocked record behind older untried work so a
			// permanently unavailable inviter cannot starve the whole queue.
			if rotateErr := DB.Model(&AffiliateReward{}).
				Where("invitee_id = ? AND status = ?", reward.InviteeId, affiliateRewardStatusPending).
				Update("updated_at", common.GetTimestamp()).Error; rotateErr != nil {
				processErrors = append(processErrors, fmt.Errorf("invitee %d retry rotation: %w", reward.InviteeId, rotateErr))
			}
		}
	}
	return errors.Join(processErrors...)
}
