package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetUserQuotaRejectsValuesOutsideDatabaseRange(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 551, 10)

	require.NoError(t, SetUserQuota(551, -25))
	assert.Equal(t, -25, getUserQuotaForPaymentGuardTest(t, 551))

	require.Error(t, SetUserQuota(551, common.MaxQuota+1))
	require.Error(t, SetUserQuota(551, common.MinQuota-1))
	assert.Equal(t, -25, getUserQuotaForPaymentGuardTest(t, 551))
}
