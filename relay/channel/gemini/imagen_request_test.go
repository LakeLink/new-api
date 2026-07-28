package gemini

import (
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

type imagenBillingReservationStub struct {
	reserved int
}

func (s *imagenBillingReservationStub) Settle(int) error         { return nil }
func (s *imagenBillingReservationStub) Refund(*gin.Context)      {}
func (s *imagenBillingReservationStub) NeedsRefund() bool        { return false }
func (s *imagenBillingReservationStub) GetPreConsumedQuota() int { return s.reserved }
func (s *imagenBillingReservationStub) Reserve(targetQuota int) error {
	s.reserved = targetQuota
	return nil
}

func TestConvertImagenRequestBoundsAndBillsOutputCount(t *testing.T) {
	tests := []struct {
		name    string
		n       uint
		wantErr bool
	}{
		{name: "minimum", n: 1},
		{name: "documented maximum", n: 4},
		{name: "zero", n: 0, wantErr: true},
		{name: "above documented maximum", n: 5, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"},
				PriceData:   types.PriceData{UsePrice: true},
			}
			converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, dto.ImageRequest{
				Prompt: "cat",
				N:      &tt.n,
			})
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, converted)
				assert.False(t, info.PriceData.HasOtherRatio("n"))
				return
			}

			require.NoError(t, err)
			request, ok := converted.(dto.GeminiImageRequest)
			require.True(t, ok)
			assert.Equal(t, int(tt.n), request.Parameters.SampleCount)
			assert.Equal(t, float64(tt.n), info.PriceData.OtherRatios()["n"])
		})
	}
}

func TestConvertImagenRequestNormalizesDocumentedAspectRatios(t *testing.T) {
	tests := []struct {
		name       string
		size       string
		wantAspect string
		wantErr    bool
	}{
		{name: "native landscape", size: "4:3", wantAspect: "4:3"},
		{name: "OpenAI landscape", size: "1536x1024", wantAspect: "4:3"},
		{name: "OpenAI portrait", size: "1024x1536", wantAspect: "3:4"},
		{name: "widescreen", size: "1792x1024", wantAspect: "16:9"},
		{name: "unsupported ratio", size: "3:2", wantErr: true},
		{name: "unsupported dimensions", size: "800x600", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"}}
			converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, dto.ImageRequest{Prompt: "cat", Size: tt.size})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			request := converted.(dto.GeminiImageRequest)
			assert.Equal(t, tt.wantAspect, request.Parameters.AspectRatio)
		})
	}
}

func TestConvertImagenRequestRejectsFast2KOutput(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-fast-generate-001"}}

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, dto.ImageRequest{Prompt: "cat", Quality: "2K"})

	require.Error(t, err)
	assert.Nil(t, converted)
}

func TestRatioPricedImagenReservesReportedImageTokensWithoutNMultiplier(t *testing.T) {
	billing := &imagenBillingReservationStub{reserved: 100}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"},
		Billing:     billing,
		PriceData: types.PriceData{
			ModelRatio:        2,
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1.5},
		},
	}
	info.PriceData.AddOtherRatio("n", 9)
	info.PriceData.AddOtherRatio("regional", 1.25)
	imageCount := uint(3)

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, dto.ImageRequest{
		Prompt: "cat",
		N:      &imageCount,
	})

	require.NoError(t, err)
	request, ok := converted.(dto.GeminiImageRequest)
	require.True(t, ok)
	assert.Equal(t, 3, request.Parameters.SampleCount)
	assert.False(t, info.PriceData.HasOtherRatio("n"))
	assert.Equal(t, 1.25, info.PriceData.OtherRatios()["regional"])
	expectedQuota := common.QuotaRound(float64(relaycommon.ImagenTokensPerImage*3) * 2 * 1.5 * 1.25)
	assert.Equal(t, expectedQuota, billing.reserved)
	assert.Equal(t, expectedQuota, info.PriceData.QuotaToPreConsume)
}

func TestOpenAIChatMappedToImagenUsesPredictPayloadAndReservesEveryImage(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	baseQuota := common.QuotaFromFloat(0.04 * common.QuotaPerUnit)
	billing := &imagenBillingReservationStub{reserved: baseQuota}
	info := &relaycommon.RelayInfo{
		OriginModelName: "customer-image-alias",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
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

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(ctx, info, &dto.GeneralOpenAIRequest{
		Model: "customer-image-alias",
		Messages: []dto.Message{
			{Role: "user", Content: "a cat in a spacesuit"},
		},
		ExtraBody: []byte(`{"n":3,"aspectRatio":"4:3"}`),
	})

	require.NoError(t, err)
	request, ok := converted.(dto.GeminiImageRequest)
	require.True(t, ok)
	require.Len(t, request.Instances, 1)
	assert.Equal(t, "a cat in a spacesuit", request.Instances[0].Prompt)
	assert.Equal(t, 3, request.Parameters.SampleCount)
	assert.Equal(t, "4:3", request.Parameters.AspectRatio)
	assert.Equal(t, baseQuota*3, billing.reserved)
	assert.Equal(t, baseQuota*3, info.PriceData.QuotaToPreConsume)

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Contains(t, requestURL, "/models/imagen-4.0-generate-001:predict")
}

func TestOpenAIChatMappedToImagenRejectsInvalidCountBeforeBuildingPayload(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"},
		PriceData:   types.PriceData{UsePrice: true},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(ctx, info, &dto.GeneralOpenAIRequest{
		Model:     "imagen-4.0-generate-001",
		Prompt:    "cat",
		ExtraBody: []byte(`{"n":1.5}`),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "integer between 1 and 4")
	assert.Nil(t, converted)
}
