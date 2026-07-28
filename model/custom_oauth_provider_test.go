package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomOAuthProviderEnabledUsesApplicationValueWithoutSchemaDefault(
	t *testing.T,
) {
	db := setupBillingAdjustmentTestDB(t, &CustomOAuthProvider{})
	require.NoError(t, db.AutoMigrate(&CustomOAuthProvider{}))

	columnTypes, err := db.Migrator().ColumnTypes(&CustomOAuthProvider{})
	require.NoError(t, err)
	var enabledDefault string
	var enabledHasDefault bool
	for _, columnType := range columnTypes {
		if columnType.Name() == "enabled" {
			enabledDefault, enabledHasDefault = columnType.DefaultValue()
			break
		}
	}
	assert.False(t, enabledHasDefault, enabledDefault)

	provider := &CustomOAuthProvider{
		Name:                  "No DB Boolean Default",
		Slug:                  "no-db-boolean-default",
		ClientId:              "client-id",
		AuthorizationEndpoint: "https://id.example/authorize",
		TokenEndpoint:         "https://id.example/token",
		UserInfoEndpoint:      "https://id.example/userinfo",
	}
	require.NoError(t, CreateCustomOAuthProvider(provider))

	stored, err := GetCustomOAuthProviderById(provider.Id)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)

	stored.Enabled = true
	require.NoError(t, UpdateCustomOAuthProvider(stored))
	reloaded, err := GetCustomOAuthProviderById(provider.Id)
	require.NoError(t, err)
	assert.True(t, reloaded.Enabled)

	reloaded.Enabled = false
	require.NoError(t, UpdateCustomOAuthProvider(reloaded))
	reloaded, err = GetCustomOAuthProviderById(provider.Id)
	require.NoError(t, err)
	assert.False(t, reloaded.Enabled)
}
