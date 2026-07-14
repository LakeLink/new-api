package common

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

type verificationValue struct {
	code      string
	expiresAt time.Time
}

const (
	EmailVerificationPurpose = "v"
	PasswordResetPurpose     = "r"
)

var (
	verificationMutex        sync.Mutex
	verificationMap          = make(map[string]verificationValue)
	verificationMapMaxSize   = 10_000
	VerificationValidMinutes = 10
)

func GenerateVerificationCode(length int) string {
	code := strings.ReplaceAll(uuid.New().String(), "-", "")
	if length == 0 {
		return code
	}
	return code[:length]
}

func verificationStorageKey(key string, purpose string) string {
	digest := sha256.Sum256([]byte(purpose + "\x00" + strings.TrimSpace(key)))
	return "verification:" + purpose + ":" + hex.EncodeToString(digest[:])
}

// RegisterVerificationCodeWithKey stores codes in Redis when configured so all
// application instances observe the same TTL and value. The bounded in-memory
// store is used only for deliberate single-node deployments without Redis; a
// Redis outage fails closed instead of creating node-local codes that cannot be
// verified reliably.
func RegisterVerificationCodeWithKey(key string, code string, purpose string) error {
	storageKey := verificationStorageKey(key, purpose)
	expiration := time.Duration(VerificationValidMinutes) * time.Minute
	if RedisEnabled && RDB != nil {
		return RDB.Set(context.Background(), storageKey, code, expiration).Err()
	}

	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap[storageKey] = verificationValue{code: code, expiresAt: time.Now().Add(expiration)}
	if len(verificationMap) > verificationMapMaxSize {
		removeExpiredPairs(time.Now())
		for len(verificationMap) > verificationMapMaxSize {
			var oldestKey string
			var oldestExpiry time.Time
			for candidateKey, value := range verificationMap {
				if oldestKey == "" || value.expiresAt.Before(oldestExpiry) {
					oldestKey = candidateKey
					oldestExpiry = value.expiresAt
				}
			}
			delete(verificationMap, oldestKey)
		}
	}
	return nil
}

func VerifyCodeWithKey(key string, code string, purpose string) bool {
	valid, err := VerifyCodeWithKeyE(key, code, purpose)
	if err != nil {
		SysError(fmt.Sprintf("verification code lookup failed: %v", err))
		return false
	}
	return valid
}

func VerifyCodeWithKeyE(key string, code string, purpose string) (bool, error) {
	storageKey := verificationStorageKey(key, purpose)
	if RedisEnabled && RDB != nil {
		stored, err := RDB.Get(context.Background(), storageKey).Result()
		if err != nil {
			if err == redis.Nil {
				return false, nil
			}
			return false, err
		}
		return secureCodeEqual(stored, code), nil
	}

	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	value, ok := verificationMap[storageKey]
	if !ok || !time.Now().Before(value.expiresAt) {
		delete(verificationMap, storageKey)
		return false, nil
	}
	return secureCodeEqual(value.code, code), nil
}

// ConsumeVerificationCodeWithKey atomically verifies and removes a code. It is
// intended for recovery credentials, which must never be replayable even when
// two reset requests arrive concurrently.
func ConsumeVerificationCodeWithKey(key string, code string, purpose string) (bool, error) {
	storageKey := verificationStorageKey(key, purpose)
	if RedisEnabled && RDB != nil {
		const compareAndDelete = `local value = redis.call('GET', KEYS[1]); if value and value == ARGV[1] then redis.call('DEL', KEYS[1]); return 1; end; return 0`
		result, err := RDB.Eval(context.Background(), compareAndDelete, []string{storageKey}, code).Int()
		if err != nil {
			return false, err
		}
		return result == 1, nil
	}

	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	value, ok := verificationMap[storageKey]
	if !ok || !time.Now().Before(value.expiresAt) {
		delete(verificationMap, storageKey)
		return false, nil
	}
	if !secureCodeEqual(value.code, code) {
		return false, nil
	}
	delete(verificationMap, storageKey)
	return true, nil
}

func DeleteKey(key string, purpose string) error {
	storageKey := verificationStorageKey(key, purpose)
	if RedisEnabled && RDB != nil {
		return RDB.Del(context.Background(), storageKey).Err()
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	delete(verificationMap, storageKey)
	return nil
}

func secureCodeEqual(expected string, actual string) bool {
	return hmac.Equal([]byte(expected), []byte(actual))
}

// removeExpiredPairs requires verificationMutex to be held by the caller.
func removeExpiredPairs(now time.Time) {
	for key, value := range verificationMap {
		if !now.Before(value.expiresAt) {
			delete(verificationMap, key)
		}
	}
}
