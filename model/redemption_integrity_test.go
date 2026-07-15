package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRedemptionQuotaMustBePositiveAndBounded(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	})

	for _, quota := range []int{0, -1, common.MaxQuota + 1} {
		redemption := &Redemption{Key: common.GetUUID(), Quota: quota}
		require.Error(t, redemption.Insert())
	}
}

func TestRedeemRejectsWalletOverflowWithoutConsumingCode(t *testing.T) {
	userID, key := setupRedeemFixture(t, 10)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userID).Update("quota", common.MaxQuota-5).Error)

	_, err := Redeem(key, userID)
	require.Error(t, err)
	assert.Equal(t, common.MaxQuota-5, getUserQuotaForPaymentGuardTest(t, userID))

	var persisted Redemption
	require.NoError(t, DB.First(&persisted, "key = ?", key).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, persisted.Status)
}
