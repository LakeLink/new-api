package siliconflow

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertImageRequestValidatesAndAccountsForNativeBatchSize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, test := range []struct {
		name      string
		batchSize string
		wantErr   bool
		wantBatch uint
	}{
		{name: "provider maximum", batchSize: `4`, wantBatch: 4},
		{name: "explicit zero", batchSize: `0`, wantErr: true},
		{name: "above provider maximum", batchSize: `5`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{}
			info.PriceData.UsePrice = true
			info.PriceData.AddOtherRatio("n", 1)
			request := dto.ImageRequest{
				Model:  "Kwai-Kolors/Kolors",
				Prompt: "a cat",
				N:      common.GetPointer(uint(1)),
				Extra:  map[string]json.RawMessage{"batch_size": []byte(test.batchSize)},
			}

			convertedValue, err := (&Adaptor{}).ConvertImageRequest(c, info, request)
			if test.wantErr {
				require.ErrorContains(t, err, "batch_size must be an integer between 1 and 4")
				return
			}
			require.NoError(t, err)
			converted, ok := convertedValue.(*SFImageRequest)
			require.True(t, ok)
			require.NotNil(t, converted.BatchSize)
			require.Equal(t, test.wantBatch, *converted.BatchSize)
			require.Equal(t, float64(test.wantBatch), info.PriceData.OtherRatios()["n"])
		})
	}
}

func TestConvertImageRequestPreservesExplicitZeroProviderScalars(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := dto.ImageRequest{
		Model:  "Kwai-Kolors/Kolors",
		Prompt: "a cat",
		N:      common.GetPointer(uint(1)),
		Extra: map[string]json.RawMessage{
			"seed":           []byte(`0`),
			"guidance_scale": []byte(`0`),
			"cfg":            []byte(`0`),
		},
	}

	convertedValue, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{}, request)
	require.NoError(t, err)
	converted, ok := convertedValue.(*SFImageRequest)
	require.True(t, ok)
	require.NotNil(t, converted.Seed)
	require.Zero(t, *converted.Seed)
	require.NotNil(t, converted.GuidanceScale)
	require.Zero(t, *converted.GuidanceScale)
	require.NotNil(t, converted.Cfg)
	require.Zero(t, *converted.Cfg)

	body, err := common.Marshal(converted)
	require.NoError(t, err)
	require.Contains(t, string(body), `"seed":0`)
	require.Contains(t, string(body), `"guidance_scale":0`)
	require.Contains(t, string(body), `"cfg":0`)
}

func TestConvertImageRequestOnlySendsBatchSizeForSupportedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	convertedValue, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{}, dto.ImageRequest{
		Model:  "Qwen/Qwen-Image-Edit-2509",
		Prompt: "edit",
		N:      common.GetPointer(uint(1)),
	})
	require.NoError(t, err)
	converted, ok := convertedValue.(*SFImageRequest)
	require.True(t, ok)
	require.Nil(t, converted.BatchSize)

	_, err = (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{}, dto.ImageRequest{
		Model:  "Qwen/Qwen-Image-Edit-2509",
		Prompt: "edit",
		N:      common.GetPointer(uint(2)),
	})
	require.ErrorContains(t, err, "batch_size greater than 1 is only supported")
}
