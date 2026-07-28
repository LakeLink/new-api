package model

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthStateCanBeConsumedExactlyOnce(t *testing.T) {
	setupBillingAdjustmentTestDB(t, &OAuthState{})
	state := "authoritative-one-time-state"
	require.NoError(t, RotateOAuthState("", state, time.Now().Add(time.Minute).Unix()))

	start := make(chan struct{})
	results := make(chan bool, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			claimed, err := ConsumeOAuthState(state, time.Now().Unix())
			results <- claimed
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)

	var claimed int
	for result := range results {
		if result {
			claimed++
		}
	}
	for err := range errors {
		require.NoError(t, err)
	}
	assert.Equal(t, 1, claimed)
}

func TestOAuthStateRotationRevokesPreviousStateAndHonorsExpiry(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &OAuthState{})
	now := time.Now().Unix()
	require.NoError(t, RotateOAuthState("", "old-state", now+60))
	require.NoError(t, RotateOAuthState("old-state", "new-state", now+60))

	claimed, err := ConsumeOAuthState("old-state", now)
	require.NoError(t, err)
	assert.False(t, claimed)
	claimed, err = ConsumeOAuthState("new-state", now)
	require.NoError(t, err)
	assert.True(t, claimed)

	require.NoError(t, RotateOAuthState("", "expired-state", now-1))
	claimed, err = ConsumeOAuthState("expired-state", now)
	require.NoError(t, err)
	assert.False(t, claimed)
	var count int64
	require.NoError(t, db.Model(&OAuthState{}).Count(&count).Error)
	assert.Zero(t, count)
}
