package model

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingReservationReplayInvalidatesCachesAfterCommitWindow(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &BillingReservation{})
	user := User{Username: "reservation-cache", Password: "password", Quota: 900, AffCode: "reservation-cache-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "reservation-cache-token", RemainQuota: 500}
	require.NoError(t, db.Create(&token).Error)
	request := BillingReservationRequest{
		RequestID:      "reservation-cache-replay",
		UserID:         user.Id,
		TokenID:        token.Id,
		TokenKey:       token.Key,
		FundingSource:  BillingAdjustmentWallet,
		ModelName:      "reservation-cache-model",
		RequestedQuota: 30,
		InitialQuota:   30,
	}
	first, err := CreateBillingReservation(request)
	require.NoError(t, err)
	assert.False(t, first.AlreadyReserved)

	serverConn, clientConn := net.Pipe()
	serverResult := make(chan error, 1)
	deletedKeys := make(chan string, 2)
	go func() {
		defer serverConn.Close()
		reader := bufio.NewReader(serverConn)
		for i := 0; i < 2; i++ {
			lines := make([]string, 5)
			for j := range lines {
				line, readErr := reader.ReadString('\n')
				if readErr != nil {
					serverResult <- readErr
					return
				}
				lines[j] = strings.TrimSpace(line)
			}
			if lines[0] != "*2" || !strings.EqualFold(lines[2], "del") {
				serverResult <- fmt.Errorf("unexpected Redis command: %v", lines)
				return
			}
			deletedKeys <- lines[4]
			if _, writeErr := io.WriteString(serverConn, ":1\r\n"); writeErr != nil {
				serverResult <- writeErr
				return
			}
		}
		serverResult <- nil
	}()

	redisClient := redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	common.RedisEnabled = true
	common.RDB = redisClient
	t.Cleanup(func() { _ = redisClient.Close() })

	replayed, err := CreateBillingReservation(request)
	require.NoError(t, err)
	assert.True(t, replayed.AlreadyReserved)
	require.NoError(t, <-serverResult)
	close(deletedKeys)
	keys := make([]string, 0, 2)
	for key := range deletedKeys {
		keys = append(keys, key)
	}
	assert.ElementsMatch(t, []string{
		fmt.Sprintf("user:%d", user.Id),
		fmt.Sprintf("token:%s", common.GenerateHMAC(token.Key)),
	}, keys)
}
