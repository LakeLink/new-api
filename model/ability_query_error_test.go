package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAbilityListQueriesPropagateDatabaseErrors(t *testing.T) {
	callbackName := "inject_ability_list_query_failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			tx.AddError(errors.New("injected ability query failure"))
		},
	))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(callbackName))
	})

	_, err := GetGroupEnabledModels("default")
	require.ErrorContains(t, err, "injected ability query failure")

	_, err = GetEnabledModels()
	require.ErrorContains(t, err, "injected ability query failure")

	_, err = GetAllEnableAbilities()
	require.ErrorContains(t, err, "injected ability query failure")

	_, err = filterAbilitiesByRequestPathAndModel(
		[]Ability{{ChannelId: 1}},
		"/v1/chat/completions",
		"test-model",
	)
	require.ErrorContains(t, err, "injected ability query failure")
}
