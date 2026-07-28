package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransferAffQuotaRejectsWalletOverflow(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	user := &User{
		Id: 554, Username: "affiliate_transfer_user", Status: common.UserStatusEnabled,
		Quota: common.MaxQuota - 5, AffQuota: 20,
	}
	require.NoError(t, DB.Create(user).Error)
	require.Error(t, user.TransferAffQuotaToQuota(10))

	var persisted User
	require.NoError(t, DB.First(&persisted, user.Id).Error)
	assert.Equal(t, common.MaxQuota-5, persisted.Quota)
	assert.Equal(t, 20, persisted.AffQuota)
}
