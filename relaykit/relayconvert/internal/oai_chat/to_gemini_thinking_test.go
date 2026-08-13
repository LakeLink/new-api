package oaichat

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatToGeminiValidatesNativeThinkingBudget(t *testing.T) {
	for _, test := range []struct {
		name    string
		budget  string
		want    int
		wantErr bool
	}{
		{name: "dynamic", budget: "-1", want: -1},
		{name: "disabled", budget: "0", want: 0},
		{name: "maximum", budget: fmt.Sprintf("%d", dto.MaxGeminiThinkingBudget), want: dto.MaxGeminiThinkingBudget},
		{name: "fractional", budget: "1.5", wantErr: true},
		{name: "below dynamic sentinel", budget: "-2", wantErr: true},
		{name: "above maximum", budget: fmt.Sprintf("%d", dto.MaxGeminiThinkingBudget+1), wantErr: true},
		{name: "overflowing float", budget: "1e300", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := OpenAIChatRequestToGeminiGenerateContent(nil, dto.GeneralOpenAIRequest{
				Model: "gemini-2.5-flash",
				ExtraBody: []byte(fmt.Sprintf(
					`{"google":{"thinking_config":{"thinking_budget":%s}}}`,
					test.budget,
				)),
			}, nil)
			if test.wantErr {
				require.ErrorContains(t, err, "thinking_budget must be an integer between")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, request.GenerationConfig.ThinkingConfig)
			require.NotNil(t, request.GenerationConfig.ThinkingConfig.ThinkingBudget)
			assert.Equal(t, test.want, *request.GenerationConfig.ThinkingConfig.ThinkingBudget)
		})
	}
}
