package controller

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
)

func TestStripeInvoicePaymentIdentifierSupportsCurrentPaymentsPayload(t *testing.T) {
	raw := []byte(`{
		"id":"in_current",
		"amount_paid":999,
		"payments":{
			"data":[
				{
					"amount_paid":400,
					"is_default":false,
					"status":"paid",
					"payment":{"payment_intent":"pi_secondary"}
				},
				{
					"amount_paid":599,
					"is_default":true,
					"status":"paid",
					"payment":{"payment_intent":{"id":"pi_default"}}
				}
			]
		}
	}`)
	var event stripe.Event
	require.NoError(t, common.Unmarshal([]byte(`{
		"id":"evt_invoice_paid",
		"type":"invoice.paid",
		"data":{"object":`+string(raw)+`}
	}`), &event))
	var invoice stripe.Invoice
	require.NoError(t, common.Unmarshal(raw, &invoice))

	assert.Equal(t, "pi_default", stripeInvoicePaymentIdentifier(event, &invoice))
	identifiers, hasMore := stripeInvoicePaymentIdentifiers(event, &invoice)
	assert.False(t, hasMore)
	assert.Equal(t, []string{"pi_default", "pi_secondary"}, identifiers)
}

func TestStripeInvoicePaymentIdentifierFallsBackToPaidCharge(t *testing.T) {
	raw := []byte(`{
		"id":"in_charge",
		"amount_paid":999,
		"payments":{
			"data":[{
				"amount_paid":999,
				"is_default":true,
				"status":"paid",
				"payment":{"charge":"ch_paid"}
			}]
		}
	}`)
	var event stripe.Event
	require.NoError(t, common.Unmarshal([]byte(`{
		"id":"evt_invoice_paid",
		"type":"invoice.paid",
		"data":{"object":`+string(raw)+`}
	}`), &event))
	var invoice stripe.Invoice
	require.NoError(t, common.Unmarshal(raw, &invoice))

	assert.Equal(t, "ch_paid", stripeInvoicePaymentIdentifier(event, &invoice))
}

func TestStripeInvoicePaymentIdentifiersSupportPaymentRecordsAndPaginationSignal(t *testing.T) {
	raw := []byte(`{
		"id":"in_payment_record",
		"amount_paid":999,
		"payments":{
			"has_more":true,
			"data":[{
				"amount_paid":999,
				"is_default":true,
				"status":"paid",
				"payment":{"payment_record":{"id":"pr_paid"}}
			}]
		}
	}`)
	var event stripe.Event
	require.NoError(t, common.Unmarshal([]byte(`{
		"id":"evt_invoice_paid",
		"type":"invoice.paid",
		"data":{"object":`+string(raw)+`}
	}`), &event))
	var invoice stripe.Invoice
	require.NoError(t, common.Unmarshal(raw, &invoice))

	identifiers, hasMore := stripeInvoicePaymentIdentifiers(event, &invoice)
	assert.True(t, hasMore)
	assert.Equal(t, []string{"pr_paid"}, identifiers)
}

func TestStripeInvoicePaidRejectsUntraceablePayment(t *testing.T) {
	var event stripe.Event
	require.NoError(t, common.Unmarshal([]byte(`{
		"id":"evt_untraceable_invoice",
		"type":"invoice.paid",
		"created":100,
		"data":{"object":{
			"id":"in_untraceable",
			"amount_paid":999,
			"currency":"usd",
			"billing_reason":"subscription_cycle",
			"parent":{"subscription_details":{"subscription":"sub_untraceable"}}
		}}
	}`), &event))

	err := handleStripeInvoicePaid(event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no traceable payment reference")
}

func TestStripeFullyRefundedPartialChargesAccumulateAtInvoiceScope(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.SubscriptionPlan{},
		&model.SubscriptionOrder{},
		&model.SubscriptionProviderPayment{},
		&model.UserSubscription{},
		&model.TopUp{},
	))
	user := &model.User{
		Id:       731,
		Username: "stripe-multi-refund",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)
	allowBalance := false
	plan := &model.SubscriptionPlan{
		Title:           "Stripe multi-payment plan",
		PriceAmount:     10,
		Enabled:         true,
		DurationUnit:    model.SubscriptionDurationMonth,
		DurationValue:   1,
		AllowBalancePay: &allowBalance,
	}
	require.NoError(t, db.Create(plan).Error)
	order := &model.SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		Money:           10,
		TradeNo:         "stripe-multi-refund-order",
		PaymentMethod:   model.PaymentMethodStripe,
		PaymentProvider: model.PaymentProviderStripe,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())
	require.NoError(t, model.SetSubscriptionOrderProviderIdentifiersAt(
		order.TradeNo,
		model.PaymentProviderStripe,
		"",
		"sub_multi_refund",
		"pi_multi_refund",
		"active",
		100,
		"",
	))
	require.NoError(t, model.CompleteSubscriptionOrder(
		order.TradeNo,
		"",
		model.PaymentProviderStripe,
		"",
	))
	require.NoError(t, model.BindSubscriptionProviderPayments(
		model.PaymentProviderStripe,
		"sub_multi_refund",
		"",
		[]string{"ch_multi_refund_a", "ch_multi_refund_b"},
	))

	buildRefundEvent := func(eventID string, chargeID string, created int64) stripe.Event {
		var event stripe.Event
		require.NoError(t, common.Unmarshal([]byte(`{
			"id":"`+eventID+`",
			"type":"charge.refunded",
			"created":`+fmt.Sprintf("%d", created)+`,
			"data":{"object":{
				"id":"`+chargeID+`",
				"amount":500,
				"amount_refunded":500,
				"refunded":true
			}}
		}`), &event))
		return event
	}

	require.NoError(t, handleStripeChargeRefunded(
		buildRefundEvent("evt_multi_refund_a", "ch_multi_refund_a", 200),
	))
	refundedOrder := model.GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, refundedOrder)
	assert.Equal(t, int64(500), refundedOrder.ProviderRefundedAmountMinor)
	var subscription model.UserSubscription
	require.NoError(t, db.Where("subscription_order_id = ?", order.Id).First(&subscription).Error)
	assert.Equal(t, "active", subscription.Status)

	require.NoError(t, handleStripeChargeRefunded(
		buildRefundEvent("evt_multi_refund_b", "ch_multi_refund_b", 300),
	))
	refundedOrder = model.GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, refundedOrder)
	assert.Equal(t, int64(1000), refundedOrder.ProviderRefundedAmountMinor)
	require.NoError(t, db.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
}

func TestCreemSubscriptionPaidAmountPrefersActualTransactionTotal(t *testing.T) {
	var event CreemWebhookEvent
	event.Object.Product.Price = 1000
	event.Object.LastTransaction.AmountPaid = 1210

	amount, err := creemSubscriptionPaidAmount(&event)
	require.NoError(t, err)
	assert.Equal(t, 12.10, amount)

	event.Object.LastTransaction.AmountPaid = 0
	amount, err = creemSubscriptionPaidAmount(&event)
	require.NoError(t, err)
	assert.Equal(t, 10.0, amount)

	event.Object.Product.Price = 0
	_, err = creemSubscriptionPaidAmount(&event)
	require.Error(t, err)
}

func TestCreemSubscriptionProductBindingSupportsNonUSDCurrency(t *testing.T) {
	plan := &model.SubscriptionPlan{CreemProductId: "prod_eur"}
	order := &model.SubscriptionOrder{
		Money:           10,
		PaymentProvider: model.PaymentProviderCreem,
	}
	var event CreemWebhookEvent
	event.Object.Product.Id = "prod_eur"
	event.Object.Product.Currency = "EUR"
	event.Object.Order.Type = "recurring"
	event.Object.Order.Transaction = "tran_eur"
	event.Object.Order.Amount = 1000
	event.Object.Order.Currency = "EUR"

	require.NoError(t, validateCreemSubscriptionCheckout(order, plan, &event))

	event.Object.Order.Currency = "USD"
	require.Error(t, validateCreemSubscriptionCheckout(order, plan, &event))
	event.Object.Order.Currency = "EUR"
	event.Object.Product.Id = "prod_other"
	require.Error(t, validateCreemSubscriptionCheckout(order, plan, &event))
}

func TestCreemSubscriptionRenewalBindsExpandedTransactionCurrency(t *testing.T) {
	plan := &model.SubscriptionPlan{CreemProductId: "prod_eur"}
	var event CreemWebhookEvent
	event.Object.Product.Id = "prod_eur"
	event.Object.Product.Currency = "EUR"
	event.Object.LastTransactionId = "tran_eur"
	event.Object.LastTransaction.Id = "tran_eur"
	event.Object.LastTransaction.Currency = "EUR"
	event.Object.LastTransaction.Status = "paid"

	require.NoError(t, validateCreemSubscriptionProduct(plan, &event))
	event.Object.LastTransaction.Currency = "USD"
	require.Error(t, validateCreemSubscriptionProduct(plan, &event))
}
