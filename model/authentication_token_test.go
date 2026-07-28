package model

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisteredAuthenticationTokenCanBeConsumedExactlyOnce(t *testing.T) {
	setupBillingAdjustmentTestDB(t, &AuthenticationToken{})
	now := time.Now().UnixMilli()
	require.NoError(t, RotateAuthenticationToken(
		"passkey_login_session",
		"",
		"challenge-a",
		now+60_000,
		now,
	))

	var successfulClaims atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := ConsumeAuthenticationToken(
				"passkey_login_session",
				"challenge-a",
				now,
			)
			errs <- err
			if claimed {
				successfulClaims.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.EqualValues(t, 1, successfulClaims.Load())
}

func TestAuthenticationTokenRotationAndExpiryFailClosed(t *testing.T) {
	setupBillingAdjustmentTestDB(t, &AuthenticationToken{})
	now := time.Now().UnixMilli()
	require.NoError(t, RotateAuthenticationToken(
		"passkey_verify_session",
		"",
		"old-challenge",
		now+60_000,
		now,
	))
	require.NoError(t, RotateAuthenticationToken(
		"passkey_verify_session",
		"old-challenge",
		"new-challenge",
		now+60_000,
		now,
	))

	claimed, err := ConsumeAuthenticationToken(
		"passkey_verify_session",
		"old-challenge",
		now,
	)
	require.NoError(t, err)
	assert.False(t, claimed)

	claimed, err = ConsumeAuthenticationToken(
		"passkey_verify_session",
		"new-challenge",
		now+60_000,
	)
	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestExternalAuthenticationAssertionReplayIsRejected(t *testing.T) {
	setupBillingAdjustmentTestDB(t, &AuthenticationToken{})
	now := time.Now().UnixMilli()

	claimed, err := ClaimAuthenticationToken(
		"telegram",
		"signed-assertion",
		now+60_000,
		now,
	)
	require.NoError(t, err)
	assert.True(t, claimed)

	claimed, err = ClaimAuthenticationToken(
		"telegram",
		"signed-assertion",
		now+60_000,
		now,
	)
	require.NoError(t, err)
	assert.False(t, claimed)
}
