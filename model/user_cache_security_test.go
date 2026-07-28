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

func TestGetUserCacheRefreshesAuthoritativeAccountState(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{})
	user := &User{
		Username: "db-name",
		Email:    "new@example.com",
		Status:   common.UserStatusDisabled,
		Quota:    42,
		Group:    "authoritative-group",
		Setting:  `{"billing_preference":"subscription_first"}`,
	}
	require.NoError(t, db.Create(user).Error)

	serverConn, clientConn := net.Pipe()
	serverResult := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		reader := bufio.NewReader(serverConn)
		lines := make([]string, 5)
		for i := range lines {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverResult <- err
				return
			}
			lines[i] = strings.TrimSpace(line)
		}
		if len(lines) != 5 || !strings.EqualFold(lines[2], "hgetall") || lines[4] != fmt.Sprintf("user:%d", user.Id) {
			serverResult <- fmt.Errorf("unexpected Redis command: %v", lines)
			return
		}

		idText := fmt.Sprintf("%d", user.Id+1000)
		response := fmt.Sprintf(
			"*14\r\n$2\r\nId\r\n$%d\r\n%s\r\n$5\r\nGroup\r\n$7\r\ndefault\r\n$5\r\nEmail\r\n$15\r\nold@example.com\r\n$5\r\nQuota\r\n$3\r\n999\r\n$6\r\nStatus\r\n$1\r\n1\r\n$8\r\nUsername\r\n$11\r\ncached-name\r\n$7\r\nSetting\r\n$0\r\n\r\n",
			len(idText), idText,
		)
		_, err := io.WriteString(serverConn, response)
		serverResult <- err
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

	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	require.NoError(t, <-serverResult)
	assert.Equal(t, user.Id, cached.Id)
	assert.Equal(t, "db-name", cached.Username)
	assert.Equal(t, "new@example.com", cached.Email)
	assert.Equal(t, 42, cached.Quota)
	assert.Equal(t, common.UserStatusDisabled, cached.Status)
	assert.Equal(t, "authoritative-group", cached.Group)
	assert.Equal(t, `{"billing_preference":"subscription_first"}`, cached.Setting)
}
