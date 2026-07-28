package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionUserLookupsFailForMissingOwner(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)

	_, err := getUserGroupByIdTx(db, 991)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	err = updateUserGroupTx(db, 991, "paid")
	require.ErrorContains(t, err, "does not exist")
}

func TestSubscriptionAdminMutationsRejectMissingOwner(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(int) (string, error)
	}{
		{name: "invalidate", mutate: AdminInvalidateUserSubscription},
		{name: "delete", mutate: AdminDeleteUserSubscription},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupSubscriptionDeleteGuardTest(t)
			subscription := UserSubscription{
				UserId:      992,
				AmountTotal: 100,
				AmountUsed:  25,
				Status:      "active",
			}
			require.NoError(t, db.Create(&subscription).Error)

			_, err := test.mutate(subscription.Id)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)

			var persisted UserSubscription
			require.NoError(t, db.First(&persisted, subscription.Id).Error)
			assert.Equal(t, "active", persisted.Status)
			assert.Equal(t, int64(25), persisted.AmountUsed)
		})
	}
}
