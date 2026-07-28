package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTextRequestPreservesProviderDefaultSearchContext(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "sonar",
		Messages: []dto.Message{{
			Role:    "user",
			Content: "search",
		}},
		WebSearchOptions: &dto.WebSearchOptions{},
	}

	require.NoError(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions))
	assert.Empty(t, request.WebSearchOptions.SearchContextSize)
}
