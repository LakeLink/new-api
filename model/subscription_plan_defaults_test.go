package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionPlanJSONEnabledDefault(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{name: "omitted defaults enabled", json: `{}`, want: true},
		{name: "explicit enabled", json: `{"enabled":true}`, want: true},
		{name: "explicit disabled", json: `{"enabled":false}`, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var plan SubscriptionPlan
			require.NoError(t, common.Unmarshal([]byte(test.json), &plan))
			assert.Equal(t, test.want, plan.Enabled)
		})
	}
}

func TestSubscriptionPlanCreatePreservesExplicitDisabledState(t *testing.T) {
	require.NoError(t, DB.Exec("DELETE FROM subscription_plans").Error)
	t.Cleanup(func() {
		_ = DB.Exec("DELETE FROM subscription_plans").Error
	})

	plan := SubscriptionPlan{
		Title:         "Disabled plan",
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       false,
	}
	require.NoError(t, DB.Create(&plan).Error)

	var persisted SubscriptionPlan
	require.NoError(t, DB.Where("id = ?", plan.Id).First(&persisted).Error)
	assert.False(t, persisted.Enabled)
}
