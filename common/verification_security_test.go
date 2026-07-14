package common

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useMemoryVerificationStore(t *testing.T) {
	t.Helper()
	oldRedisEnabled := RedisEnabled
	oldRDB := RDB
	RedisEnabled = false
	RDB = nil
	verificationMutex.Lock()
	verificationMap = make(map[string]verificationValue)
	verificationMutex.Unlock()
	t.Cleanup(func() {
		RedisEnabled = oldRedisEnabled
		RDB = oldRDB
		verificationMutex.Lock()
		verificationMap = make(map[string]verificationValue)
		verificationMutex.Unlock()
	})
}

func TestConsumeVerificationCodeIsSingleUse(t *testing.T) {
	useMemoryVerificationStore(t)
	require.NoError(t, RegisterVerificationCodeWithKey("user@example.com", "123456", PasswordResetPurpose))
	valid, err := ConsumeVerificationCodeWithKey("user@example.com", "wrong", PasswordResetPurpose)
	require.NoError(t, err)
	assert.False(t, valid)

	valid, err = ConsumeVerificationCodeWithKey("user@example.com", "123456", PasswordResetPurpose)
	require.NoError(t, err)
	assert.True(t, valid)

	valid, err = ConsumeVerificationCodeWithKey("user@example.com", "123456", PasswordResetPurpose)
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestExpiredMemoryVerificationCodeIsRejected(t *testing.T) {
	useMemoryVerificationStore(t)
	require.NoError(t, RegisterVerificationCodeWithKey("user@example.com", "123456", EmailVerificationPurpose))
	storageKey := verificationStorageKey("user@example.com", EmailVerificationPurpose)
	verificationMutex.Lock()
	value := verificationMap[storageKey]
	value.expiresAt = time.Now().Add(-time.Second)
	verificationMap[storageKey] = value
	verificationMutex.Unlock()

	valid, err := VerifyCodeWithKeyE("user@example.com", "123456", EmailVerificationPurpose)
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestMatchTOTPTimeStepReturnsReplayKey(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	at := time.Unix(1_800_000_000, 0)
	code, err := totp.GenerateCode(secret, at)
	require.NoError(t, err)

	step, valid := MatchTOTPTimeStep(secret, code, at)
	assert.True(t, valid)
	assert.Equal(t, at.Unix()/30, step)

	step, valid = MatchTOTPTimeStep(secret, code, at.Add(30*time.Second))
	require.True(t, valid)
	assert.Equal(t, at.Unix()/30, step)

	_, valid = MatchTOTPTimeStep(secret, code, at.Add(60*time.Second))
	assert.False(t, valid)
}
