package relay

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateConvertedRequestRejectsNilAndTypedNil(t *testing.T) {
	var typedNilBuffer *bytes.Buffer
	var typedNilMap map[string]any
	tests := []struct {
		name    string
		value   any
		wantErr bool
	}{
		{name: "nil", value: nil, wantErr: true},
		{name: "typed nil pointer", value: typedNilBuffer, wantErr: true},
		{name: "typed nil map", value: typedNilMap, wantErr: true},
		{name: "empty but present map", value: map[string]any{}},
		{name: "request pointer", value: &dto.EmbeddingRequest{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateConvertedRequest(test.value)
			if test.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "empty converted request")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRerankHelperRejectsAdaptorNilRequestBeforeDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeDify)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "rerank-model")

	info := &relaycommon.RelayInfo{
		Request:         &dto.RerankRequest{Model: "rerank-model"},
		OriginModelName: "rerank-model",
	}
	apiErr := RerankHelper(c, info)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeConvertRequestFailed, apiErr.GetErrorCode())
	assert.Contains(t, apiErr.Error(), "empty converted request")
	assert.Equal(t, 0, recorder.Body.Len())
}

func TestAdaptorHTTPResponseRejectsUnexpectedTypesWithoutPanicking(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "wrong type", value: "not an HTTP response"},
		{name: "typed nil", value: (*http.Response)(nil)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response *http.Response
			var apiErr *types.NewAPIError
			require.NotPanics(t, func() {
				response, apiErr = adaptorHTTPResponse(test.value)
			})
			assert.Nil(t, response)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
		})
	}
}

func TestAdaptorUsageNormalizesMissingAndValueResults(t *testing.T) {
	textValue := dto.Usage{PromptTokens: 7}
	assert.Equal(t, &textValue, adaptorTextUsage(textValue))
	assert.Nil(t, adaptorTextUsage(nil))
	assert.Nil(t, adaptorTextUsage("invalid"))

	realtimeValue := dto.RealtimeUsage{InputTokens: 9}
	assert.Equal(t, &realtimeValue, adaptorRealtimeUsage(realtimeValue))
	assert.Nil(t, adaptorRealtimeUsage(nil))
	assert.Nil(t, adaptorRealtimeUsage("invalid"))
}
