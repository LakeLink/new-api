package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const setupSingletonID uint = 1

var ErrSetupAlreadyCompleted = errors.New("system setup is already completed")

type Setup struct {
	ID            uint   `json:"id" gorm:"primaryKey"`
	Version       string `json:"version" gorm:"type:varchar(50);not null"`
	InitializedAt int64  `json:"initialized_at" gorm:"type:bigint;not null"`
	ClaimToken    string `json:"-" gorm:"type:varchar(32)"`
}

func GetSetup() (*Setup, error) {
	var setup Setup
	err := DB.Order("id asc").First(&setup).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &setup, nil
}

// InitializeSetup atomically claims the singleton setup marker, creates the
// first root account when needed, and persists the initial operating modes. A
// database uniqueness conflict makes concurrent callers deterministic losers;
// no root or option writes can escape if the transaction rolls back.
func InitializeSetup(rootUser *User, selfUseModeEnabled bool, demoSiteEnabled bool) error {
	optionValues := map[string]string{
		"SelfUseModeEnabled": strconv.FormatBool(selfUseModeEnabled),
		"DemoSiteEnabled":    strconv.FormatBool(demoSiteEnabled),
	}
	for key, value := range optionValues {
		if err := validateOptionValue(key, value); err != nil {
			return err
		}
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		setup := Setup{
			ID:            setupSingletonID,
			Version:       common.Version,
			InitializedAt: time.Now().Unix(),
			ClaimToken:    common.GetUUID(),
		}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&setup)
		if claim.Error != nil {
			return claim.Error
		}
		// MySQL can report a matched no-op upsert as one affected row when the
		// connection enables CLIENT_FOUND_ROWS. Read the claimed row under a lock
		// and compare its random token instead of trusting RowsAffected.
		var persistedSetup Setup
		if err := lockForUpdate(tx).Where("id = ?", setupSingletonID).First(&persistedSetup).Error; err != nil {
			return err
		}
		if persistedSetup.ClaimToken != setup.ClaimToken {
			return ErrSetupAlreadyCompleted
		}
		// Legacy setup rows may predate the fixed singleton ID. Detect them only
		// after the claim so SQLite's first transactional statement is a write;
		// this avoids the read-to-write lock upgrade race between initializers.
		var legacySetupCount int64
		if err := tx.Model(&Setup{}).Where("id <> ?", setupSingletonID).Count(&legacySetupCount).Error; err != nil {
			return err
		}
		if legacySetupCount != 0 {
			return ErrSetupAlreadyCompleted
		}

		rootExists, err := rootUserExists(tx)
		if err != nil {
			return err
		}
		if !rootExists {
			if rootUser == nil {
				return errors.New("root user credentials are required")
			}
			rootUser.Username = strings.TrimSpace(rootUser.Username)
			if rootUser.Username == "" {
				return errors.New("root username is required")
			}
			if err := tx.Create(rootUser).Error; err != nil {
				return fmt.Errorf("create root user: %w", err)
			}
		}

		for key, value := range optionValues {
			option := Option{Key: key}
			if err := tx.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
				return err
			}
			option.Value = value
			if err := tx.Save(&option).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for key, value := range optionValues {
		if err := updateOptionMap(key, value); err != nil {
			return err
		}
	}
	return nil
}
