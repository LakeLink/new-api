package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type finalImagenBillingStub struct {
	reserved int
}

func (s *finalImagenBillingStub) Settle(int) error              { return nil }
func (s *finalImagenBillingStub) Refund(*gin.Context)           {}
func (s *finalImagenBillingStub) NeedsRefund() bool             { return false }
func (s *finalImagenBillingStub) GetPreConsumedQuota() int      { return s.reserved }
func (s *finalImagenBillingStub) Reserve(targetQuota int) error { s.reserved = targetQuota; return nil }

func TestRefreshFinalImagenRequestSynchronizesOverrideCountAndReservation(t *testing.T) {
	baseQuota := common.QuotaFromFloat(0.04 * common.QuotaPerUnit)
	billing := &finalImagenBillingStub{reserved: baseQuota}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeGemini,
			UpstreamModelName: "imagen-4.0-generate-001",
		},
		Billing: billing,
		PriceData: types.PriceData{
			ModelPrice:        0.04,
			UsePrice:          true,
			QuotaToPreConsume: baseQuota,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	info.PriceData.AddOtherRatio("n", 1)

	imageCount, err := refreshFinalImagenRequest(info, []byte(`{
		"instances":[{"prompt":"cat"}],
		"parameters":{"sampleCount":4,"aspectRatio":"1:1"}
	}`))

	require.NoError(t, err)
	assert.Equal(t, 4, imageCount)
	assert.Equal(t, float64(4), info.PriceData.OtherRatios()["n"])
	assert.Equal(t, baseQuota*4, billing.reserved)
	assert.Equal(t, baseQuota*4, info.PriceData.QuotaToPreConsume)
}

func TestRefreshFinalRatioPricedImagenUsesDeterministicUsageWithoutNMultiplier(t *testing.T) {
	initialQuota := common.QuotaRound(float64(relaycommon.ImagenTokensPerImage) * 1.5)
	billing := &finalImagenBillingStub{reserved: initialQuota}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeVertexAi,
			UpstreamModelName: "imagen-4.0-generate-001",
		},
		Billing: billing,
		PriceData: types.PriceData{
			ModelRatio:        1.5,
			QuotaToPreConsume: initialQuota,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	imageCount, err := refreshFinalImagenRequest(info, []byte(`{
		"instances":[{"prompt":"cat"}],
		"parameters":{"sampleCount":4,"aspectRatio":"1:1"}
	}`))

	require.NoError(t, err)
	assert.Equal(t, 4, imageCount)
	assert.False(t, info.PriceData.HasOtherRatio("n"))
	expectedQuota := common.QuotaRound(float64(relaycommon.ImagenTokensPerImage*4) * 1.5)
	assert.Equal(t, expectedQuota, billing.reserved)
	assert.Equal(t, expectedQuota, info.PriceData.QuotaToPreConsume)
}

func TestRefreshFinalImagenRequestRejectsDeletedOrOutOfRangeCount(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "deleted", body: `{"instances":[{"prompt":"cat"}],"parameters":{}}`},
		{name: "zero", body: `{"instances":[{"prompt":"cat"}],"parameters":{"sampleCount":0}}`},
		{name: "too many", body: `{"instances":[{"prompt":"cat"}],"parameters":{"sampleCount":5}}`},
		{name: "fractional", body: `{"instances":[{"prompt":"cat"}],"parameters":{"sampleCount":1.5}}`},
		{name: "multiple instances", body: `{"instances":[{"prompt":"cat"},{"prompt":"dog"}],"parameters":{"sampleCount":1}}`},
		{name: "empty prompt", body: `{"instances":[{"prompt":""}],"parameters":{"sampleCount":1}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ApiType:           constant.APITypeVertexAi,
					UpstreamModelName: "imagen-4.0-generate-001",
				},
			}

			imageCount, err := refreshFinalImagenRequest(info, []byte(tt.body))

			require.Error(t, err)
			assert.Zero(t, imageCount)
		})
	}
}

func TestRefreshFinalTieredImagenRejectsChangedSampleCount(t *testing.T) {
	initialCount := uint(1)
	info := &relaycommon.RelayInfo{
		Request: &dto.ImageRequest{
			Model:  "imagen-4.0-generate-001",
			Prompt: "cat",
			N:      &initialCount,
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeVertexAi,
			UpstreamModelName: "imagen-4.0-generate-001",
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{},
		PriceData:             types.PriceData{QuotaToPreConsume: 100},
	}

	imageCount, err := refreshFinalImagenRequest(info, []byte(`{
		"instances":[{"prompt":"cat"}],
		"parameters":{"sampleCount":4}
	}`))

	require.ErrorContains(t, err, "sampleCount changed after tiered pre-consume")
	assert.Zero(t, imageCount)
	assert.Equal(t, 100, info.PriceData.QuotaToPreConsume)
}

func TestRefreshFinalTieredImagenPreservesUnchangedSnapshotReservation(t *testing.T) {
	initialCount := uint(4)
	billing := &finalImagenBillingStub{reserved: 100}
	info := &relaycommon.RelayInfo{
		Request: &dto.ImageRequest{
			Model:  "imagen-4.0-generate-001",
			Prompt: "cat",
			N:      &initialCount,
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeVertexAi,
			UpstreamModelName: "imagen-4.0-generate-001",
		},
		Billing:               billing,
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{},
		PriceData:             types.PriceData{QuotaToPreConsume: 100},
	}

	imageCount, err := refreshFinalImagenRequest(info, []byte(`{
		"instances":[{"prompt":"cat"}],
		"parameters":{"sampleCount":4}
	}`))

	require.NoError(t, err)
	assert.Equal(t, 4, imageCount)
	assert.Equal(t, 100, billing.reserved)
	assert.Equal(t, 100, info.PriceData.QuotaToPreConsume)
}
