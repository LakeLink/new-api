package oauth

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

const maxOAuthResponseBodyBytes int64 = 1 << 20

func protectedOAuthHTTPClient(timeout time.Duration) *http.Client {
	baseClient := service.GetSSRFProtectedHTTPClient()
	return &http.Client{
		Transport:     baseClient.Transport,
		CheckRedirect: baseClient.CheckRedirect,
		Jar:           baseClient.Jar,
		Timeout:       timeout,
	}
}

// readOAuthResponseBody bounds identity-provider responses before they are
// decoded. OAuth token and user-info payloads are small; accepting an
// unbounded body lets a misconfigured or compromised provider exhaust gateway
// memory during a login.
func readOAuthResponseBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, errors.New("OAuth provider returned an empty response")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxOAuthResponseBodyBytes {
		return nil, fmt.Errorf("OAuth provider response exceeds %d bytes", maxOAuthResponseBodyBytes)
	}
	return body, nil
}

func decodeOAuthJSONResponse(response *http.Response, destination any) error {
	body, err := readOAuthResponseBody(response)
	if err != nil {
		return err
	}
	return common.Unmarshal(body, destination)
}

func requireOAuthSuccessStatus(response *http.Response) error {
	if response == nil {
		return errors.New("OAuth provider returned an empty response")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("OAuth provider returned status %d", response.StatusCode)
	}
	return nil
}
