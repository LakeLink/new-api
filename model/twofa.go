package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// TwoFA 用户2FA设置表
type TwoFA struct {
	Id             int            `json:"id" gorm:"primaryKey"`
	UserId         int            `json:"user_id" gorm:"unique;not null;index"`
	Secret         string         `json:"-" gorm:"type:varchar(255);not null"` // TOTP密钥，不返回给前端
	IsEnabled      bool           `json:"is_enabled"`
	FailedAttempts int            `json:"failed_attempts" gorm:"default:0"`
	LockedUntil    *time.Time     `json:"locked_until,omitempty"`
	LastUsedAt     *time.Time     `json:"last_used_at,omitempty"`
	LastUsedStep   int64          `json:"-" gorm:"column:last_used_step"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-" gorm:"index"`
}

// TwoFABackupCode 备用码使用记录表
type TwoFABackupCode struct {
	Id        int            `json:"id" gorm:"primaryKey"`
	UserId    int            `json:"user_id" gorm:"not null;index"`
	CodeHash  string         `json:"-" gorm:"type:varchar(255);not null"` // 备用码哈希
	IsUsed    bool           `json:"is_used"`
	UsedAt    *time.Time     `json:"used_at,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// GetTwoFAByUserId 根据用户ID获取2FA设置
func GetTwoFAByUserId(userId int) (*TwoFA, error) {
	if userId == 0 {
		return nil, errors.New("用户ID不能为空")
	}

	var twoFA TwoFA
	err := DB.Where("user_id = ?", userId).First(&twoFA).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // 返回nil表示未设置2FA
		}
		return nil, err
	}

	return &twoFA, nil
}

// IsTwoFAEnabled checks whether the user has enabled 2FA. Database errors are
// returned to the caller so authentication can fail closed instead of silently
// treating an unavailable 2FA record as an account without a second factor.
func IsTwoFAEnabled(userId int) (bool, error) {
	twoFA, err := GetTwoFAByUserId(userId)
	if err != nil {
		return false, err
	}
	return twoFA != nil && twoFA.IsEnabled, nil
}

// CreateTwoFA 创建2FA设置
func (t *TwoFA) Create() error {
	// 检查用户是否已存在2FA设置
	existing, err := GetTwoFAByUserId(t.UserId)
	if err != nil {
		return err
	}
	if existing != nil {
		return errors.New("用户已存在2FA设置")
	}

	// 验证用户存在
	var user User
	if err := DB.First(&user, t.UserId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("用户不存在")
		}
		return err
	}

	return DB.Create(t).Error
}

// Update 更新2FA设置
func (t *TwoFA) Update() error {
	if t.Id == 0 {
		return errors.New("2FA记录ID不能为空")
	}
	return DB.Save(t).Error
}

// Delete 删除2FA设置
func (t *TwoFA) Delete() error {
	if t.Id == 0 {
		return errors.New("2FA记录ID不能为空")
	}

	// 使用事务确保原子性
	return DB.Transaction(func(tx *gorm.DB) error {
		// 同时删除相关的备用码记录（硬删除）
		if err := tx.Unscoped().Where("user_id = ?", t.UserId).Delete(&TwoFABackupCode{}).Error; err != nil {
			return err
		}

		// 硬删除2FA记录
		return tx.Unscoped().Delete(t).Error
	})
}

// ResetFailedAttempts 重置失败尝试次数
func (t *TwoFA) ResetFailedAttempts() error {
	if err := DB.Model(&TwoFA{}).Where("id = ?", t.Id).Updates(map[string]any{
		"failed_attempts": 0,
		"locked_until":    nil,
	}).Error; err != nil {
		return err
	}
	t.FailedAttempts = 0
	t.LockedUntil = nil
	return nil
}

// IncrementFailedAttempts 增加失败尝试次数
func (t *TwoFA) IncrementFailedAttempts() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var current TwoFA
		if err := lockForUpdate(tx).Where("id = ?", t.Id).First(&current).Error; err != nil {
			return err
		}
		incrementTwoFAFailure(&current, time.Now())
		if err := tx.Model(&current).Updates(map[string]any{
			"failed_attempts": current.FailedAttempts,
			"locked_until":    current.LockedUntil,
		}).Error; err != nil {
			return err
		}
		*t = current
		return nil
	})
}

func incrementTwoFAFailure(twoFA *TwoFA, now time.Time) {
	twoFA.FailedAttempts++
	if twoFA.FailedAttempts >= common.MaxFailAttempts {
		lockUntil := now.Add(time.Duration(common.LockoutDuration) * time.Second)
		twoFA.LockedUntil = &lockUntil
	}
}

// IsLocked 检查账户是否被锁定
func (t *TwoFA) IsLocked() bool {
	if t.LockedUntil == nil {
		return false
	}
	return time.Now().Before(*t.LockedUntil)
}

// CreateBackupCodes 创建备用码
func CreateBackupCodes(userId int, codes []string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// 先删除现有的备用码
		if err := tx.Where("user_id = ?", userId).Delete(&TwoFABackupCode{}).Error; err != nil {
			return err
		}

		// 创建新的备用码记录
		for _, code := range codes {
			hashedCode, err := common.HashBackupCode(code)
			if err != nil {
				return err
			}

			backupCode := TwoFABackupCode{
				UserId:   userId,
				CodeHash: hashedCode,
				IsUsed:   false,
			}

			if err := tx.Create(&backupCode).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// ValidateBackupCode 验证并使用备用码
func ValidateBackupCode(userId int, code string) (bool, error) {
	if !common.ValidateBackupCode(code) {
		return false, errors.New("验证码或备用码不正确")
	}

	normalizedCode := common.NormalizeBackupCode(code)

	used := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var backupCodes []TwoFABackupCode
		if err := tx.Where("user_id = ? AND is_used = ?", userId, false).Find(&backupCodes).Error; err != nil {
			return err
		}
		for _, backupCode := range backupCodes {
			if !common.ValidatePasswordAndHash(normalizedCode, backupCode.CodeHash) {
				continue
			}
			now := time.Now()
			result := tx.Model(&TwoFABackupCode{}).
				Where("id = ? AND is_used = ?", backupCode.Id, false).
				Updates(map[string]any{"is_used": true, "used_at": &now})
			if result.Error != nil {
				return result.Error
			}
			used = result.RowsAffected == 1
			return nil
		}
		return nil
	})
	return used, err
}

// GetUnusedBackupCodeCount 获取未使用的备用码数量
func GetUnusedBackupCodeCount(userId int) (int, error) {
	var count int64
	err := DB.Model(&TwoFABackupCode{}).Where("user_id = ? AND is_used = ?", userId, false).Count(&count).Error
	return int(count), err
}

// DisableTwoFA 禁用用户的2FA
func DisableTwoFA(userId int) error {
	twoFA, err := GetTwoFAByUserId(userId)
	if err != nil {
		return err
	}
	if twoFA == nil {
		return ErrTwoFANotEnabled
	}

	// 删除2FA设置和备用码
	return twoFA.Delete()
}

// EnableTwoFA 启用2FA
func (t *TwoFA) Enable() error {
	t.IsEnabled = true
	t.FailedAttempts = 0
	t.LockedUntil = nil
	return t.Update()
}

// EnableWithTOTP verifies the enrollment code and enables the factor in one
// transaction. Recording the time step prevents the enrollment code from being
// replayed immediately as a separate authentication assertion.
func (t *TwoFA) EnableWithTOTP(code string) (bool, error) {
	valid := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current TwoFA
		if err := lockForUpdate(tx).Where("id = ?", t.Id).First(&current).Error; err != nil {
			return err
		}
		now := time.Now()
		step, matched := common.MatchTOTPTimeStep(current.Secret, code, now)
		if !matched || step <= current.LastUsedStep {
			return nil
		}
		if err := tx.Model(&current).Updates(map[string]any{
			"is_enabled":      true,
			"failed_attempts": 0,
			"locked_until":    nil,
			"last_used_at":    &now,
			"last_used_step":  step,
		}).Error; err != nil {
			return err
		}
		current.IsEnabled = true
		current.FailedAttempts = 0
		current.LockedUntil = nil
		current.LastUsedAt = &now
		current.LastUsedStep = step
		*t = current
		valid = true
		return nil
	})
	return valid, err
}

// ValidateTOTPAndUpdateUsage 验证TOTP并更新使用记录
func (t *TwoFA) ValidateTOTPAndUpdateUsage(code string) (bool, error) {
	return t.validateCodeAndUpdateUsage(code, true, false)
}

// ValidateBackupCodeAndUpdateUsage 验证备用码并更新使用记录
func (t *TwoFA) ValidateBackupCodeAndUpdateUsage(code string) (bool, error) {
	return t.validateCodeAndUpdateUsage(code, false, true)
}

// ValidateCodeAndUpdateUsage validates either a TOTP or a backup code while
// applying replay protection, backup-code consumption, and the failed-attempt
// counter in one database transaction. A failed input increments the counter
// exactly once regardless of which code formats it resembles.
func (t *TwoFA) ValidateCodeAndUpdateUsage(code string) (bool, error) {
	return t.validateCodeAndUpdateUsage(code, true, true)
}

func (t *TwoFA) validateCodeAndUpdateUsage(code string, allowTOTP bool, allowBackup bool) (bool, error) {
	valid := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current TwoFA
		query := lockForUpdate(tx)
		if t.Id != 0 {
			query = query.Where("id = ?", t.Id)
		} else {
			query = query.Where("user_id = ?", t.UserId)
		}
		if err := query.First(&current).Error; err != nil {
			return err
		}
		if !current.IsEnabled {
			return ErrTwoFANotEnabled
		}

		now := time.Now()
		if current.LockedUntil != nil && now.Before(*current.LockedUntil) {
			return fmt.Errorf("账户已被锁定，请在%v后重试", current.LockedUntil.Format("2006-01-02 15:04:05"))
		}

		matchedStep := int64(0)
		if allowTOTP {
			if step, matched := common.MatchTOTPTimeStep(current.Secret, code, now); matched && step > current.LastUsedStep {
				matchedStep = step
				valid = true
			}
		}

		if !valid && allowBackup && common.ValidateBackupCode(code) {
			normalizedCode := common.NormalizeBackupCode(code)
			var backupCodes []TwoFABackupCode
			if err := tx.Where("user_id = ? AND is_used = ?", current.UserId, false).Find(&backupCodes).Error; err != nil {
				return err
			}
			for _, backupCode := range backupCodes {
				if !common.ValidatePasswordAndHash(normalizedCode, backupCode.CodeHash) {
					continue
				}
				result := tx.Model(&TwoFABackupCode{}).
					Where("id = ? AND is_used = ?", backupCode.Id, false).
					Updates(map[string]any{"is_used": true, "used_at": &now})
				if result.Error != nil {
					return result.Error
				}
				valid = result.RowsAffected == 1
				break
			}
		}

		updates := map[string]any{}
		if valid {
			current.FailedAttempts = 0
			current.LockedUntil = nil
			current.LastUsedAt = &now
			if matchedStep != 0 {
				current.LastUsedStep = matchedStep
			}
			updates["failed_attempts"] = 0
			updates["locked_until"] = nil
			updates["last_used_at"] = &now
			updates["last_used_step"] = current.LastUsedStep
		} else {
			incrementTwoFAFailure(&current, now)
			updates["failed_attempts"] = current.FailedAttempts
			updates["locked_until"] = current.LockedUntil
		}
		if err := tx.Model(&current).Updates(updates).Error; err != nil {
			return err
		}
		*t = current
		return nil
	})
	return valid, err
}

// GetTwoFAStats 获取2FA统计信息（管理员使用）
func GetTwoFAStats() (map[string]interface{}, error) {
	var totalUsers, enabledUsers int64

	// 总用户数
	if err := DB.Model(&User{}).Count(&totalUsers).Error; err != nil {
		return nil, err
	}

	// 启用2FA的用户数
	if err := DB.Model(&TwoFA{}).Where("is_enabled = ?", true).Count(&enabledUsers).Error; err != nil {
		return nil, err
	}

	enabledRate := float64(0)
	if totalUsers > 0 {
		enabledRate = float64(enabledUsers) / float64(totalUsers) * 100
	}

	return map[string]interface{}{
		"total_users":   totalUsers,
		"enabled_users": enabledUsers,
		"enabled_rate":  fmt.Sprintf("%.1f%%", enabledRate),
	}, nil
}
