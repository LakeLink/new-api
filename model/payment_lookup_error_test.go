package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPaymentOrderLookupsDistinguishMissingRowsFromDatabaseFailures(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &TopUp{}, &SubscriptionOrder{})

	_, err := FindTopUpByTradeNo("missing-topup")
	require.ErrorIs(t, err, ErrTopUpNotFound)
	_, err = FindSubscriptionOrderByTradeNo("missing-subscription")
	require.ErrorIs(t, err, ErrSubscriptionOrderNotFound)
	_, err = FindSubscriptionOrderByProviderSubscription("creem", "missing-provider-subscription")
	require.ErrorIs(t, err, ErrSubscriptionOrderNotFound)

	injectedErr := errors.New("injected payment lookup failure")
	const callbackName = "test:fail_payment_order_lookup"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema == nil {
				return
			}
			switch tx.Statement.Schema.Table {
			case "top_ups", "subscription_orders":
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(callbackName))
	})

	_, err = FindTopUpByTradeNo("transient-topup")
	require.ErrorIs(t, err, injectedErr)
	require.NotErrorIs(t, err, ErrTopUpNotFound)
	_, err = FindSubscriptionOrderByTradeNo("transient-subscription")
	require.ErrorIs(t, err, injectedErr)
	require.NotErrorIs(t, err, ErrSubscriptionOrderNotFound)
	_, err = FindSubscriptionOrderByProviderSubscription("creem", "transient-provider-subscription")
	require.ErrorIs(t, err, injectedErr)
	require.NotErrorIs(t, err, ErrSubscriptionOrderNotFound)
}
