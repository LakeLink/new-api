package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthenticationUserLookupsSurfaceDatabaseErrors(t *testing.T) {
	setupBillingAdjustmentTestDB(t)

	tests := []struct {
		name string
		user User
		load func(*User) error
	}{
		{
			name: "primary id",
			user: User{Id: 1},
			load: func(user *User) error { return user.FillUserById() },
		},
		{
			name: "email",
			user: User{Email: "user@example.com"},
			load: func(user *User) error { return user.FillUserByEmail() },
		},
		{
			name: "GitHub",
			user: User{GitHubId: "github-user"},
			load: func(user *User) error { return user.FillUserByGitHubId() },
		},
		{
			name: "Discord",
			user: User{DiscordId: "discord-user"},
			load: func(user *User) error { return user.FillUserByDiscordId() },
		},
		{
			name: "OIDC",
			user: User{OidcId: "oidc-user"},
			load: func(user *User) error { return user.FillUserByOidcId() },
		},
		{
			name: "WeChat",
			user: User{WeChatId: "wechat-user"},
			load: func(user *User) error { return user.FillUserByWeChatId() },
		},
		{
			name: "Telegram",
			user: User{TelegramId: "telegram-user"},
			load: func(user *User) error { return user.FillUserByTelegramId() },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user := test.user
			require.ErrorContains(t, test.load(&user), "no such table")
		})
	}
}
