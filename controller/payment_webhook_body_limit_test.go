package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPaymentWebhooksRejectOversizedBodies(t *testing.T) {
	originalStripeSecret := setting.StripeWebhookSecret
	originalCreemSecret := setting.CreemWebhookSecret
	originalWaffoSandbox := setting.WaffoSandbox
	originalWaffoMerchantID := setting.WaffoMerchantId
	originalWaffoAPIKey := setting.WaffoApiKey
	originalWaffoPrivateKey := setting.WaffoPrivateKey
	originalWaffoPublicCert := setting.WaffoPublicCert
	t.Cleanup(func() {
		setting.StripeWebhookSecret = originalStripeSecret
		setting.CreemWebhookSecret = originalCreemSecret
		setting.WaffoSandbox = originalWaffoSandbox
		setting.WaffoMerchantId = originalWaffoMerchantID
		setting.WaffoApiKey = originalWaffoAPIKey
		setting.WaffoPrivateKey = originalWaffoPrivateKey
		setting.WaffoPublicCert = originalWaffoPublicCert
	})

	setting.StripeWebhookSecret = "whsec_test"
	setting.CreemWebhookSecret = "creem_secret"
	setting.WaffoSandbox = false
	setting.WaffoMerchantId = "merchant"
	setting.WaffoApiKey = "api"
	setting.WaffoPrivateKey = "private"
	setting.WaffoPublicCert = "public"

	tests := []struct {
		name    string
		handler gin.HandlerFunc
		params  gin.Params
	}{
		{name: "Stripe", handler: StripeWebhook},
		{name: "Creem", handler: CreemWebhook},
		{name: "Waffo", handler: WaffoWebhook},
		{
			name:    "Waffo Pancake",
			handler: WaffoPancakeWebhook,
			params:  gin.Params{{Key: "env", Value: "test"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = test.params
			c.Request = httptest.NewRequest(
				http.MethodPost,
				"/webhook",
				strings.NewReader(strings.Repeat("x", int(paymentWebhookMaxBodyBytes)+1)),
			)

			test.handler(c)

			require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
		})
	}
}
