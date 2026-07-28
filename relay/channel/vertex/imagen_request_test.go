package vertex

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imagenReservationStub struct {
	reserved int
}

func (s *imagenReservationStub) Settle(int) error              { return nil }
func (s *imagenReservationStub) Refund(*gin.Context)           {}
func (s *imagenReservationStub) NeedsRefund() bool             { return false }
func (s *imagenReservationStub) GetPreConsumedQuota() int      { return s.reserved }
func (s *imagenReservationStub) Reserve(targetQuota int) error { s.reserved = targetQuota; return nil }

func TestOpenAICompatibleImagenExtraBodyCountIsStrictlyValidated(t *testing.T) {
	tests := []struct {
		name      string
		extraBody string
		wantN     int
		wantErr   bool
	}{
		{name: "integer count", extraBody: `{"n":4}`, wantN: 4},
		{name: "fractional count", extraBody: `{"n":1.5}`, wantErr: true},
		{name: "count above provider maximum", extraBody: `{"n":5}`, wantErr: true},
		{name: "string count", extraBody: `{"n":"4"}`, wantErr: true},
		{name: "malformed extra body", extraBody: `{"n":`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"},
				PriceData:   types.PriceData{UsePrice: true},
			}
			converted, err := (&Adaptor{RequestMode: RequestModeGemini}).ConvertOpenAIRequest(ctx, info, &dto.GeneralOpenAIRequest{
				Model:     "imagen-4.0-generate-001",
				Prompt:    "cat",
				ExtraBody: []byte(tt.extraBody),
			})
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, converted)
				return
			}

			require.NoError(t, err)
			request, ok := converted.(dto.GeminiImageRequest)
			require.True(t, ok)
			assert.Equal(t, tt.wantN, request.Parameters.SampleCount)
			assert.Equal(t, float64(tt.wantN), info.PriceData.OtherRatios()["n"])
		})
	}
}

func TestOpenAICompatibleImagenExplicitCountIsStrictlyValidated(t *testing.T) {
	for _, count := range []int{0, relaycommon.MaxImagenImageCount + 1} {
		t.Run(fmt.Sprintf("n=%d", count), func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"},
				PriceData:   types.PriceData{UsePrice: true},
			}

			converted, err := (&Adaptor{RequestMode: RequestModeGemini}).ConvertOpenAIRequest(ctx, info, &dto.GeneralOpenAIRequest{
				Model:  "imagen-4.0-generate-001",
				Prompt: "cat",
				N:      &count,
			})

			require.Error(t, err)
			assert.Nil(t, converted)
			assert.Contains(t, err.Error(), "integer between 1 and")
		})
	}
}

func TestMappedImagenAliasExtendsReservationBeforeRequest(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	baseQuota := common.QuotaFromFloat(0.04 * common.QuotaPerUnit)
	billing := &imagenReservationStub{reserved: baseQuota}
	info := &relaycommon.RelayInfo{
		OriginModelName: "customer-image-alias",
		ChannelMeta: &relaycommon.ChannelMeta{
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

	converted, err := (&Adaptor{RequestMode: RequestModeGemini}).ConvertOpenAIRequest(ctx, info, &dto.GeneralOpenAIRequest{
		Model:     "imagen-4.0-generate-001",
		Prompt:    "cat",
		ExtraBody: []byte(`{"n":4}`),
	})

	require.NoError(t, err)
	request, ok := converted.(dto.GeminiImageRequest)
	require.True(t, ok)
	assert.Equal(t, 4, request.Parameters.SampleCount)
	assert.Equal(t, baseQuota*4, billing.reserved)
	assert.Equal(t, baseQuota*4, info.PriceData.QuotaToPreConsume)
}

func TestMappedImagenAliasRatioPricingReservesDeterministicUsageWithoutMultiplier(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	billing := &imagenReservationStub{reserved: 100}
	info := &relaycommon.RelayInfo{
		OriginModelName: "customer-image-alias",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "imagen-4.0-generate-001",
		},
		Billing: billing,
		PriceData: types.PriceData{
			ModelRatio:        2,
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	converted, err := (&Adaptor{RequestMode: RequestModeGemini}).ConvertOpenAIRequest(ctx, info, &dto.GeneralOpenAIRequest{
		Model:     "customer-image-alias",
		Prompt:    "cat",
		ExtraBody: []byte(`{"n":4}`),
	})

	require.NoError(t, err)
	request, ok := converted.(dto.GeminiImageRequest)
	require.True(t, ok)
	assert.Equal(t, 4, request.Parameters.SampleCount)
	assert.False(t, info.PriceData.HasOtherRatio("n"))
	expectedQuota := common.QuotaRound(float64(relaycommon.ImagenTokensPerImage*4) * 2)
	assert.Equal(t, expectedQuota, billing.reserved)
	assert.Equal(t, expectedQuota, info.PriceData.QuotaToPreConsume)
}
