package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func signedTelegramParams(token string, authDate int64) map[string][]string {
	params := map[string][]string{
		"id":         {"12345"},
		"auth_date":  {strconv.FormatInt(authDate, 10)},
		"first_name": {"Test"},
	}
	dataCheckString := "auth_date=" + params["auth_date"][0] + "\nfirst_name=Test\nid=12345"
	secret := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, secret[:])
	_, _ = mac.Write([]byte(dataCheckString))
	params["hash"] = []string{hex.EncodeToString(mac.Sum(nil))}
	return params
}

func TestCheckTelegramAuthorizationRejectsReplayAndMalformedValues(t *testing.T) {
	const token = "telegram-bot-token"
	now := time.Now().Unix()

	assert.True(t, checkTelegramAuthorization(signedTelegramParams(token, now), token))
	assert.False(t, checkTelegramAuthorization(signedTelegramParams(token, now-int64((6*time.Minute)/time.Second)), token))
	assert.False(t, checkTelegramAuthorization(signedTelegramParams(token, now+60), token))

	malformed := signedTelegramParams(token, now)
	malformed["hash"] = []string{strings.Repeat("z", 64)}
	assert.False(t, checkTelegramAuthorization(malformed, token))

	ambiguous := signedTelegramParams(token, now)
	ambiguous["id"] = []string{"12345", "67890"}
	assert.False(t, checkTelegramAuthorization(ambiguous, token))
}
