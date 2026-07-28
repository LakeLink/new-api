package helper

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiThinkingBudgetValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, test := range []struct {
		name    string
		budget  int
		wantErr bool
	}{
		{name: "dynamic", budget: -1},
		{name: "disabled", budget: 0},
		{name: "maximum", budget: dto.MaxGeminiThinkingBudget},
		{name: "below dynamic sentinel", budget: -2, wantErr: true},
		{name: "above maximum", budget: dto.MaxGeminiThinkingBudget + 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := fmt.Sprintf(
				`{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":%d}}}`,
				test.budget,
			)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:generateContent", bytes.NewBufferString(body))
			c.Request.Header.Set("Content-Type", "application/json")

			request, err := GetAndValidateGeminiRequest(c)
			if test.wantErr {
				require.ErrorContains(t, err, "thinkingBudget")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, request.GenerationConfig.ThinkingConfig)
			require.NotNil(t, request.GenerationConfig.ThinkingConfig.ThinkingBudget)
			assert.Equal(t, test.budget, *request.GenerationConfig.ThinkingConfig.ThinkingBudget)
		})
	}
}
