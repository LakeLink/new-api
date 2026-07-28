package relay

import (
	"fmt"
	"net/http"
	"reflect"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
)

// validateConvertedRequest rejects an adaptor's nil or typed-nil conversion
// result before it can be marshalled as JSON null or dispatched as an empty
// request body. A nil result with no error is an adaptor contract violation,
// not a valid upstream request.
func validateConvertedRequest(value any) error {
	if value == nil {
		return fmt.Errorf("adaptor returned an empty converted request")
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if reflected.IsNil() {
			return fmt.Errorf("adaptor returned an empty converted request of type %T", value)
		}
	}
	return nil
}

// adaptorHTTPResponse validates the intentionally generic Adaptor.DoRequest
// result at the relay boundary. Some SDK-backed adaptors return nil and keep
// their response state on the adaptor, but every non-nil HTTP result must have
// the documented type.
func adaptorHTTPResponse(value any) (*http.Response, *types.NewAPIError) {
	if value == nil {
		return nil, nil
	}
	response, ok := value.(*http.Response)
	if !ok || response == nil {
		err := fmt.Errorf("invalid adaptor response type: %T", value)
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	return response, nil
}

// adaptorTextUsage normalizes the generic Adaptor.DoResponse result. Missing
// or invalid usage is deliberately returned as nil: quota finalization treats
// nil as untrusted upstream billing data and settles against the pre-consumed
// reservation instead of allowing an undercharge or a type-assertion panic.
func adaptorTextUsage(value any) *dto.Usage {
	switch usage := value.(type) {
	case *dto.Usage:
		return usage
	case dto.Usage:
		return &usage
	default:
		return nil
	}
}

func adaptorRealtimeUsage(value any) *dto.RealtimeUsage {
	switch usage := value.(type) {
	case *dto.RealtimeUsage:
		return usage
	case dto.RealtimeUsage:
		return &usage
	default:
		return nil
	}
}
