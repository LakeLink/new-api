package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopUpLockEntryIsReleasedAfterConcurrentAttempts(t *testing.T) {
	const userID = 987654321
	topUpLocks.Delete(userID)
	t.Cleanup(func() {
		topUpLocks.Delete(userID)
	})

	holder := getTopUpLock(userID)
	require.True(t, holder.TryLock())
	contender := getTopUpLock(userID)
	require.Same(t, holder, contender)
	assert.False(t, contender.TryLock())

	releaseTopUpLock(userID, contender, false)
	_, exists := topUpLocks.Load(userID)
	assert.True(t, exists, "the active holder must keep the lock entry alive")

	releaseTopUpLock(userID, holder, true)
	_, exists = topUpLocks.Load(userID)
	assert.False(t, exists, "an idle per-user lock must not leak forever")
}
