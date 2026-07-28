package model

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPasskeyAssertionTest(t *testing.T) (*User, *webauthn.Credential) {
	t.Helper()
	db := setupBillingAdjustmentTestDB(t, &User{}, &PasskeyCredential{})
	user := &User{
		Username: "passkey-assertion-user",
		AffCode:  "passkey-assertion-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)
	credential := &webauthn.Credential{
		ID:              []byte("passkey-credential-id"),
		PublicKey:       []byte("passkey-public-key"),
		AttestationType: "none",
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: true,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:     []byte("passkey-aaguid"),
			SignCount:  4,
			Attachment: protocol.Platform,
		},
	}
	record := NewPasskeyCredentialFromWebAuthn(user.Id, credential)
	require.NotNil(t, record)
	require.NoError(t, UpsertPasskeyCredential(record))
	return user, credential
}

func TestPasskeyAssertionPersistsValidatedCounterAndFlags(t *testing.T) {
	user, credential := setupPasskeyAssertionTest(t)
	validated := *credential
	validated.Authenticator.SignCount = 5
	validated.Flags.BackupState = true
	usedAt := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, UpdatePasskeyCredentialAfterAssertion(
		user.Id,
		&validated,
		usedAt,
	))

	persisted, err := GetPasskeyByUserID(user.Id)
	require.NoError(t, err)
	assert.Equal(t, uint32(5), persisted.SignCount)
	assert.True(t, persisted.BackupState)
	require.NotNil(t, persisted.LastUsedAt)
	assert.WithinDuration(t, usedAt, *persisted.LastUsedAt, time.Millisecond)
}

func TestPasskeyAssertionRejectsStaleConcurrentCounter(t *testing.T) {
	user, credential := setupPasskeyAssertionTest(t)
	first := *credential
	first.Authenticator.SignCount = 5
	require.NoError(t, UpdatePasskeyCredentialAfterAssertion(
		user.Id,
		&first,
		time.Now(),
	))

	stale := *credential
	stale.Authenticator.SignCount = 5
	err := UpdatePasskeyCredentialAfterAssertion(
		user.Id,
		&stale,
		time.Now().Add(time.Second),
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPasskeyCounterConflict))

	persisted, getErr := GetPasskeyByUserID(user.Id)
	require.NoError(t, getErr)
	assert.Equal(t, uint32(5), persisted.SignCount)
	assert.True(t, persisted.CloneWarning)
}

func TestPasskeyAssertionAllowsAuthenticatorsWithoutCounters(t *testing.T) {
	user, credential := setupPasskeyAssertionTest(t)
	db := DB
	require.NoError(t, db.Model(&PasskeyCredential{}).
		Where("user_id = ?", user.Id).
		Update("sign_count", 0).Error)
	credential.Authenticator.SignCount = 0

	require.NoError(t, UpdatePasskeyCredentialAfterAssertion(
		user.Id,
		credential,
		time.Now(),
	))
	require.NoError(t, UpdatePasskeyCredentialAfterAssertion(
		user.Id,
		credential,
		time.Now().Add(time.Second),
	))

	persisted, err := GetPasskeyByUserID(user.Id)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), persisted.SignCount)
	assert.False(t, persisted.CloneWarning)
}
