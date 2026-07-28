package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImageOutputTokens(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		quality string
		size    string
		want    int
	}{
		{name: "gpt image 1 square low", model: "gpt-image-1", quality: "low", size: "1024x1024", want: 272},
		{name: "gpt image 1 portrait medium", model: "gpt-image-1", quality: "medium", size: "1024x1536", want: 1584},
		{name: "gpt image 1 mini square low", model: "gpt-image-1-mini", quality: "low", size: "1024x1024", want: 625},
		{name: "gpt image 1 mini portrait medium", model: "gpt-image-1-mini", quality: "medium", size: "1024x1536", want: 1875},
		{name: "gpt image 1 mini landscape high", model: "gpt-image-1-mini", quality: "high", size: "1536x1024", want: 6500},
		{name: "gpt image 2 square low", model: "gpt-image-2", quality: "low", size: "1024x1024", want: 196},
		{name: "gpt image 2 square medium", model: "gpt-image-2", quality: "medium", size: "1024x1024", want: 1756},
		{name: "gpt image 2 landscape high", model: "gpt-image-2", quality: "high", size: "1536x1024", want: 5488},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := OpenAIImageOutputTokens(test.model, test.quality, test.size)
			require.True(t, ok)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestOpenAIImageOutputCostUsesModelTokenRates(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		{model: "gpt-image-1", want: 272 * 40.0 / 1_000_000},
		{model: "gpt-image-1-mini", want: 0.005},
		{model: "gpt-image-1.5", want: 272 * 32.0 / 1_000_000},
		{model: "chatgpt-image-latest", want: 272 * 32.0 / 1_000_000},
		{model: "gpt-image-2", want: 196 * 30.0 / 1_000_000},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			got, ok := OpenAIImageOutputCostUSD(test.model, "low", "1024x1024")
			require.True(t, ok)
			assert.InDelta(t, test.want, got, 1e-12)
		})
	}

	partial, ok := OpenAIImagePartialOutputCostUSD("gpt-image-2", 3)
	require.True(t, ok)
	assert.InDelta(t, 300*30.0/1_000_000, partial, 1e-12)
}

func TestOpenAIImageInputTokenReservation(t *testing.T) {
	tests := []struct {
		model    string
		fidelity string
		want     int
	}{
		{model: "gpt-image-1", fidelity: "", want: OpenAIImageLowFidelityInputTokens},
		{model: "gpt-image-1.5", fidelity: "high", want: OpenAIImageHighFidelityInputTokens},
		{model: "gpt-image-1-mini", fidelity: "", want: OpenAIImageLowFidelityInputTokens},
		{model: "gpt-image-2", fidelity: "", want: OpenAIImageHighFidelityInputTokens},
	}
	for _, test := range tests {
		t.Run(test.model+"/"+test.fidelity, func(t *testing.T) {
			got, ok := OpenAIImageInputTokens(test.model, test.fidelity)
			require.True(t, ok)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestImageTokenReservationIncludesCountAndPartials(t *testing.T) {
	count := uint(2)
	request := ImageRequest{
		Model:         "gpt-image-1",
		Prompt:        "cat",
		N:             &count,
		Quality:       "low",
		Size:          "1024x1024",
		PartialImages: []byte("3"),
	}
	assert.Equal(t, 2*(272+3*OpenAIImagePartialOutputTokens), request.GetTokenCountMeta().MaxTokens)
}

func TestImageEditTokenReservationIncludesInputImages(t *testing.T) {
	request := ImageRequest{
		Model:           "gpt-image-1.5",
		Prompt:          "edit",
		Quality:         "low",
		Size:            "1024x1024",
		InputFidelity:   []byte(`"high"`),
		InputImageCount: 2,
	}
	meta := request.GetTokenCountMeta()
	assert.Equal(t, 272, meta.ImageOutputTokens)
	assert.Equal(t, 2*OpenAIImageHighFidelityInputTokens, meta.ImageInputTokens)
}

func TestValidOpenAIGPTImage2Size(t *testing.T) {
	assert.True(t, ValidOpenAIGPTImage2Size("1024x1024"))
	assert.True(t, ValidOpenAIGPTImage2Size("3840x2160"))
	assert.False(t, ValidOpenAIGPTImage2Size("3840x1264"))
	assert.False(t, ValidOpenAIGPTImage2Size("1000x1000"))
	assert.False(t, ValidOpenAIGPTImage2Size("3840x3840"))
}
