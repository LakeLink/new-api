package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestChatResponsesChannelSelection(t *testing.T) {
	enabled, disabled := true, false
	tests := []struct {
		name                   string
		master, all            bool
		channelID, channelType int
		patterns               []string
		override               *bool
		want                   bool
	}{
		{"legacy channel ID", true, false, 79, 57, []string{"^gpt-6.*$"}, nil, true},
		{"channel type", true, false, 80, 1, []string{"^gpt-6.*$"}, nil, true},
		{"unselected channel", true, false, 80, 57, []string{"^gpt-6.*$"}, nil, false},
		{"channel enabled outside global selection", true, false, 80, 57, []string{"^gpt-6.*$"}, &enabled, true},
		{"channel disabled overrides legacy ID", true, false, 79, 57, []string{"^gpt-6.*$"}, &disabled, false},
		{"channel disabled overrides channel type", true, false, 80, 1, []string{"^gpt-6.*$"}, &disabled, false},
		{"channel disabled overrides all channels", true, true, 80, 57, []string{"^gpt-6.*$"}, &disabled, false},
		{"inherits all channels", true, true, 80, 57, []string{"^gpt-6.*$"}, nil, true},
		{"master disabled overrides channel enable", false, true, 79, 57, []string{"^gpt-6.*$"}, &enabled, false},
		{"model mismatch overrides channel enable", true, true, 79, 57, []string{"^gpt-5.*$"}, &enabled, false},
		{"empty model list", true, true, 79, 57, nil, &enabled, false},
		{"Go regexp with inline flags", true, false, 79, 57, []string{"(?i)^GPT-[56]\\.[0-9]{1,3}-sol$"}, nil, true},
		{"invalid historical patterns do not match", true, true, 79, 57, []string{"["}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := model_setting.ChatCompletionsToResponsesPolicy{
				Enabled: tc.master, AllChannels: tc.all,
				ChannelIDs: []int{79}, ChannelTypes: []int{1}, ModelPatterns: tc.patterns,
			}
			assert.Equal(t, tc.want, ShouldChatCompletionsUseResponsesPolicy(policy, tc.channelID, tc.channelType, "gpt-6.1-sol", tc.override))
		})
	}
}

func TestChatResponsesPolicyValidation(t *testing.T) {
	for _, value := range []string{
		`{}`, `{"enabled":true,"channel_ids":[79],"channel_types":[1],"model_patterns":["(?i)^gpt-[56].{1,3}$"]}`,
	} {
		require.NoError(t, model_setting.ValidateChatCompletionsToResponsesPolicy(value))
	}
	for _, value := range []string{
		``, `null`, `[]`, `{"enabled":"yes"}`, `{"model_patterns":["["]}`, `{"model_patterns":[1]}`,
	} {
		require.Error(t, model_setting.ValidateChatCompletionsToResponsesPolicy(value), value)
	}
}

func TestChatResponsesChannelDatabaseRoundTrip(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "conversion.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			table := db.Table("chat_responses_test_channels").Session(&gorm.Session{})
			require.NoError(t, table.AutoMigrate(&model.Channel{}))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable("chat_responses_test_channels")) })
			var version string
			query := "select version()"
			if dialect == "sqlite" {
				query = "select sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("%s version: %s", dialect, version)
			enabled, disabled := true, false
			channel := model.Channel{Name: "Codex", Type: 57, Key: "unused", Status: common.ChannelStatusEnabled}
			// Start with a legacy row, then enable, exclude, and return to inherited selection.
			for _, override := range []*bool{nil, &enabled, &disabled, nil} {
				channel.SetSetting(dto.ChannelSettings{ChatCompletionsToResponses: override, NonStreamUpstreamStream: true, Proxy: "socks5://localhost:1080"})
				require.NoError(t, table.Save(&channel).Error)
				var loaded model.Channel
				require.NoError(t, table.First(&loaded, channel.Id).Error)
				settings := loaded.GetSetting()
				assert.Equal(t, override, settings.ChatCompletionsToResponses)
				assert.True(t, settings.NonStreamUpstreamStream)
				assert.Equal(t, "socks5://localhost:1080", settings.Proxy)
				policy := model_setting.ChatCompletionsToResponsesPolicy{Enabled: true, ChannelIDs: []int{channel.Id}, ModelPatterns: []string{"^gpt-6.*$"}}
				assert.Equal(t, override == nil || *override, ShouldChatCompletionsUseResponsesPolicy(policy, channel.Id, channel.Type, "gpt-6.1-sol", settings.ChatCompletionsToResponses))
				var stored map[string]any
				require.NoError(t, common.UnmarshalJsonStr(*loaded.Setting, &stored))
				if override == nil {
					assert.NotContains(t, stored, "chat_completions_to_responses")
				} else {
					assert.Equal(t, *override, stored["chat_completions_to_responses"])
				}
			}
		})
	}
}
