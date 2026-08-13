package moonshot

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelListAdvertisesCurrentMoonshotModels(t *testing.T) {
	assert.Equal(t, []string{
		"kimi-k3",
		"kimi-k2.7-code",
		"kimi-k2.7-code-highspeed",
		"kimi-k2.6",
		"kimi-k2.5",
		"moonshot-v1-8k",
		"moonshot-v1-32k",
		"moonshot-v1-128k",
		"moonshot-v1-8k-vision-preview",
		"moonshot-v1-32k-vision-preview",
		"moonshot-v1-128k-vision-preview",
	}, (&Adaptor{}).GetModelList())

	for _, retiredModel := range []string{
		"kimi-k2-0905-preview",
		"kimi-k2-turbo-preview",
		"kimi-k2-thinking",
		"kimi-k2-thinking-turbo",
	} {
		assert.NotContains(t, ModelList, retiredModel)
	}
}

func TestConvertOpenAIRequestKimiK26AcceptsOfficialParameters(t *testing.T) {
	tests := []struct {
		name        string
		thinking    string
		temperature float64
	}{
		{
			name:        "provider default thinking",
			temperature: 1.0,
		},
		{
			name:        "thinking enabled",
			thinking:    `{"type":"enabled"}`,
			temperature: 1.0,
		},
		{
			name:        "thinking enabled with preserved history",
			thinking:    `{"type":"enabled","keep":"all"}`,
			temperature: 1.0,
		},
		{
			name:        "thinking disabled",
			thinking:    `{"type":"disabled"}`,
			temperature: 0.6,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{
				Model:            "kimi-k2.6",
				Temperature:      common.GetPointer(test.temperature),
				TopP:             common.GetPointer(0.95),
				N:                common.GetPointer(1),
				PresencePenalty:  common.GetPointer(0.0),
				FrequencyPenalty: common.GetPointer(0.0),
			}
			if test.thinking != "" {
				request.THINKING = []byte(test.thinking)
			}

			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

			require.NoError(t, err)
			assert.Same(t, request, converted)
			assert.Equal(t, test.temperature, *request.Temperature)
			assert.Equal(t, 0.95, *request.TopP)
			assert.Equal(t, 1, *request.N)
			assert.Equal(t, 0.0, *request.PresencePenalty)
			assert.Equal(t, 0.0, *request.FrequencyPenalty)
			assert.Equal(t, test.thinking, string(request.THINKING))
		})
	}
}

func TestConvertOpenAIRequestKimiK25AcceptsOfficialParameters(t *testing.T) {
	tests := []struct {
		name        string
		thinking    string
		temperature float64
	}{
		{name: "provider default thinking", temperature: 1.0},
		{name: "thinking enabled", thinking: `{"type":"enabled"}`, temperature: 1.0},
		{name: "thinking disabled", thinking: `{"type":"disabled"}`, temperature: 0.6},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{
				Model:            "kimi-k2.5",
				Temperature:      common.GetPointer(test.temperature),
				TopP:             common.GetPointer(0.95),
				N:                common.GetPointer(1),
				PresencePenalty:  common.GetPointer(0.0),
				FrequencyPenalty: common.GetPointer(0.0),
			}
			if test.thinking != "" {
				request.THINKING = []byte(test.thinking)
			}

			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

			require.NoError(t, err)
			assert.Same(t, request, converted)
		})
	}
}

func TestConvertOpenAIRequestCurrentKimiK2PreservesProviderDefaults(t *testing.T) {
	for _, model := range []string{"kimi-k2.6", "kimi-k2.5"} {
		t.Run(model, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{Model: model}

			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

			require.NoError(t, err)
			assert.Same(t, request, converted)
			assert.Empty(t, request.THINKING)
			assert.Nil(t, request.Temperature)
			assert.Nil(t, request.TopP)
			assert.Nil(t, request.N)
			assert.Nil(t, request.PresencePenalty)
			assert.Nil(t, request.FrequencyPenalty)
		})
	}
}

func TestConvertOpenAIRequestKimiK27AcceptsOnlyThinkingMode(t *testing.T) {
	for _, model := range []string{"kimi-k2.7-code", "kimi-k2.7-code-highspeed"} {
		for _, thinking := range []string{"", `{"type":"enabled"}`, `{"type":"enabled","keep":"all"}`, `{"type":"enabled","keep":null}`} {
			t.Run(model+"/"+thinking, func(t *testing.T) {
				request := &dto.GeneralOpenAIRequest{
					Model:            model,
					Temperature:      common.GetPointer(1.0),
					TopP:             common.GetPointer(0.95),
					N:                common.GetPointer(1),
					PresencePenalty:  common.GetPointer(0.0),
					FrequencyPenalty: common.GetPointer(0.0),
				}
				if thinking != "" {
					request.THINKING = []byte(thinking)
				}

				converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

				require.NoError(t, err)
				assert.Same(t, request, converted)
			})
		}
	}
}

func TestConvertOpenAIRequestKimiK3ValidatesCurrentProtocol(t *testing.T) {
	maxCompletionTokens := uint(1_048_576)
	request := &dto.GeneralOpenAIRequest{
		Model:               "kimi-k3",
		ReasoningEffort:     "max",
		MaxCompletionTokens: &maxCompletionTokens,
		Temperature:         common.GetPointer(1.0),
		TopP:                common.GetPointer(0.95),
		N:                   common.GetPointer(1),
		PresencePenalty:     common.GetPointer(0.0),
		FrequencyPenalty:    common.GetPointer(0.0),
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

	require.NoError(t, err)
	assert.Same(t, request, converted)
}

func TestConvertOpenAIRequestCurrentKimiModelsRejectProtocolViolations(t *testing.T) {
	tooManyK3Tokens := uint(1_048_577)
	tests := []struct {
		name    string
		model   string
		request dto.GeneralOpenAIRequest
		wantErr string
	}{
		{
			name:    "k2.5 rejects preserved thinking",
			model:   "kimi-k2.5",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`{"type":"enabled","keep":"all"}`)},
			wantErr: "thinking.keep is not supported",
		},
		{
			name:    "k2.6 rejects invalid preserved thinking",
			model:   "kimi-k2.6",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`{"type":"enabled","keep":"last"}`)},
			wantErr: "thinking.keep must be all or null",
		},
		{
			name:    "k2.6 rejects unknown thinking field",
			model:   "kimi-k2.6",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`{"type":"enabled","budget_tokens":1000}`)},
			wantErr: `unsupported field "budget_tokens"`,
		},
		{
			name:    "k2.7 cannot disable thinking",
			model:   "kimi-k2.7-code",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`{"type":"disabled"}`)},
			wantErr: "thinking.type must be enabled",
		},
		{
			name:    "k2.7 rejects reasoning effort",
			model:   "kimi-k2.7-code-highspeed",
			request: dto.GeneralOpenAIRequest{ReasoningEffort: "high"},
			wantErr: "does not support reasoning_effort",
		},
		{
			name:    "k3 rejects thinking field",
			model:   "kimi-k3",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`{"type":"enabled"}`)},
			wantErr: "thinking is always enabled",
		},
		{
			name:    "k3 rejects unsupported reasoning effort",
			model:   "kimi-k3",
			request: dto.GeneralOpenAIRequest{ReasoningEffort: "medium"},
			wantErr: "reasoning_effort must be low, high, or max",
		},
		{
			name:    "k3 bounds completion tokens",
			model:   "kimi-k3",
			request: dto.GeneralOpenAIRequest{MaxCompletionTokens: &tooManyK3Tokens},
			wantErr: "must not exceed 1048576",
		},
		{
			name:    "k3 enforces fixed temperature",
			model:   "kimi-k3",
			request: dto.GeneralOpenAIRequest{Temperature: common.GetPointer(0.6)},
			wantErr: "temperature must be 1.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.request.Model = test.model

			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, &test.request)

			require.ErrorContains(t, err, test.wantErr)
			assert.Nil(t, converted)
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
		})
	}
}

func TestConvertOpenAIRequestKimiThinkingRestrictsToolChoice(t *testing.T) {
	tools := []dto.ToolCallRequest{{Type: "function"}}
	request := &dto.GeneralOpenAIRequest{
		Model:      "kimi-k2.7-code",
		Tools:      tools,
		ToolChoice: "required",
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

	require.ErrorContains(t, err, "tool_choice must be auto or none")
	assert.Nil(t, converted)

	request = &dto.GeneralOpenAIRequest{
		Model:      "kimi-k2.6",
		THINKING:   []byte(`{"type":"disabled"}`),
		Tools:      tools,
		ToolChoice: "required",
	}
	converted, err = (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)
	require.NoError(t, err)
	assert.Same(t, request, converted)
}

func TestConvertOpenAIRequestCurrentKimiK2RejectsUnsupportedParameters(t *testing.T) {
	tests := []struct {
		name    string
		request dto.GeneralOpenAIRequest
		wantErr string
	}{
		{
			name:    "malformed thinking",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`{`)},
			wantErr: "thinking is invalid",
		},
		{
			name:    "thinking is not an object",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`false`)},
			wantErr: "thinking must be an object",
		},
		{
			name:    "thinking type is missing",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`{}`)},
			wantErr: "thinking.type must be enabled or disabled",
		},
		{
			name:    "thinking type is unsupported",
			request: dto.GeneralOpenAIRequest{THINKING: []byte(`{"type":"auto"}`)},
			wantErr: "thinking.type must be enabled or disabled",
		},
		{
			name: "default thinking temperature",
			request: dto.GeneralOpenAIRequest{
				Temperature: common.GetPointer(0.6),
			},
			wantErr: "temperature must be 1.0 when thinking is enabled",
		},
		{
			name: "enabled thinking temperature",
			request: dto.GeneralOpenAIRequest{
				THINKING:    []byte(`{"type":"enabled"}`),
				Temperature: common.GetPointer(0.6),
			},
			wantErr: "temperature must be 1.0 when thinking is enabled",
		},
		{
			name: "disabled thinking temperature",
			request: dto.GeneralOpenAIRequest{
				THINKING:    []byte(`{"type":"disabled"}`),
				Temperature: common.GetPointer(1.0),
			},
			wantErr: "temperature must be 0.6 when thinking is disabled",
		},
		{
			name: "top p",
			request: dto.GeneralOpenAIRequest{
				TopP: common.GetPointer(1.0),
			},
			wantErr: "top_p must be 0.95",
		},
		{
			name: "number of choices",
			request: dto.GeneralOpenAIRequest{
				N: common.GetPointer(2),
			},
			wantErr: "n must be 1",
		},
		{
			name: "presence penalty",
			request: dto.GeneralOpenAIRequest{
				PresencePenalty: common.GetPointer(0.1),
			},
			wantErr: "presence_penalty must be 0",
		},
		{
			name: "frequency penalty",
			request: dto.GeneralOpenAIRequest{
				FrequencyPenalty: common.GetPointer(-0.1),
			},
			wantErr: "frequency_penalty must be 0",
		},
	}

	for _, model := range []string{"kimi-k2.6", "kimi-k2.5"} {
		for _, test := range tests {
			t.Run(model+"/"+test.name, func(t *testing.T) {
				request := test.request
				request.Model = model

				converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, &request)

				require.ErrorContains(t, err, test.wantErr)
				assert.Nil(t, converted)
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
			})
		}
	}
}

func TestConvertOpenAIRequestUsesMappedUpstreamModelForKimiValidation(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:       "public-kimi-alias",
		THINKING:    []byte(`{"type":"disabled"}`),
		Temperature: common.GetPointer(1.0),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "kimi-k2.6",
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)

	require.ErrorContains(t, err, "temperature must be 0.6 when thinking is disabled")
	assert.Nil(t, converted)
}

func TestConvertOpenAIRequestRetiredK2ModelRemainsPassThroughCompatible(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:            "kimi-k2-thinking",
		Temperature:      common.GetPointer(0.7),
		TopP:             common.GetPointer(0.8),
		N:                common.GetPointer(2),
		PresencePenalty:  common.GetPointer(0.2),
		FrequencyPenalty: common.GetPointer(0.3),
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

	require.NoError(t, err)
	assert.Same(t, request, converted)
}
