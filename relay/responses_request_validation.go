package relay

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
)

// refreshFinalResponsesRequest validates and records the exact JSON payload
// that will be sent to a Responses endpoint. Channel field filtering and
// parameter overrides happen after the original request was validated, so
// billing metadata must be rebuilt from this final representation.
func refreshFinalResponsesRequest(info *relaycommon.RelayInfo, jsonData []byte) error {
	var request dto.OpenAIResponsesRequest
	if err := common.Unmarshal(jsonData, &request); err != nil {
		return fmt.Errorf("decode final Responses request: %w", err)
	}
	if err := helper.ValidateResponsesRequest(&request); err != nil {
		return fmt.Errorf("final Responses request is invalid: %w", err)
	}
	if info != nil {
		info.ResponsesUsageInfo = relaycommon.NewResponsesUsageInfo(&request)
		if original, ok := info.Request.(*dto.OpenAIResponsesRequest); ok {
			original.MaxToolCalls = cloneUint(request.MaxToolCalls)
		}
	}
	return nil
}
