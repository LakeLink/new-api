package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
)

const maxCodexWhamResponseBytes int64 = 4 << 20

func readCodexWhamResponse(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("empty Codex usage response")
	}
	body, err := ReadResponseBodyWithLimit(resp.Body, maxCodexWhamResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("Codex usage %w", err)
	}
	return body, nil
}

func FetchCodexWhamUsage(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	accountID string,
) (statusCode int, body []byte, err error) {
	if client == nil {
		return 0, nil, fmt.Errorf("nil http client")
	}
	bu := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if bu == "" {
		return 0, nil, fmt.Errorf("empty baseURL")
	}
	at := strings.TrimSpace(accessToken)
	aid := strings.TrimSpace(accountID)
	if at == "" {
		return 0, nil, fmt.Errorf("empty accessToken")
	}
	if aid == "" {
		return 0, nil, fmt.Errorf("empty accountID")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bu+"/backend-api/wham/usage", nil)
	if err != nil {
		return 0, nil, fmt.Errorf("create Codex usage request: %s", common.MaskSensitiveInfo(err.Error()))
	}
	setCodexWhamRequestHeaders(req, at, aid)

	resp, err := DoUpstreamRequest(client, req)
	if err != nil {
		return 0, nil, fmt.Errorf("Codex usage request failed: %s", common.MaskSensitiveInfo(err.Error()))
	}
	defer resp.Body.Close()

	body, err = readCodexWhamResponse(resp)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func FetchCodexWhamRateLimitResetCredits(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	accountID string,
) (statusCode int, body []byte, err error) {
	if client == nil {
		return 0, nil, fmt.Errorf("nil http client")
	}
	bu := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if bu == "" {
		return 0, nil, fmt.Errorf("empty baseURL")
	}
	at := strings.TrimSpace(accessToken)
	aid := strings.TrimSpace(accountID)
	if at == "" {
		return 0, nil, fmt.Errorf("empty accessToken")
	}
	if aid == "" {
		return 0, nil, fmt.Errorf("empty accountID")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bu+"/backend-api/wham/rate-limit-reset-credits", nil)
	if err != nil {
		return 0, nil, fmt.Errorf("create Codex credits request: %s", common.MaskSensitiveInfo(err.Error()))
	}
	setCodexWhamRequestHeaders(req, at, aid)

	resp, err := DoUpstreamRequest(client, req)
	if err != nil {
		return 0, nil, fmt.Errorf("Codex credits request failed: %s", common.MaskSensitiveInfo(err.Error()))
	}
	defer resp.Body.Close()

	body, err = readCodexWhamResponse(resp)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func ConsumeCodexWhamRateLimitResetCredit(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	accountID string,
) (statusCode int, body []byte, err error) {
	if client == nil {
		return 0, nil, fmt.Errorf("nil http client")
	}
	bu := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if bu == "" {
		return 0, nil, fmt.Errorf("empty baseURL")
	}
	at := strings.TrimSpace(accessToken)
	aid := strings.TrimSpace(accountID)
	if at == "" {
		return 0, nil, fmt.Errorf("empty accessToken")
	}
	if aid == "" {
		return 0, nil, fmt.Errorf("empty accountID")
	}

	requestBody, err := common.Marshal(map[string]string{
		"redeem_request_id": uuid.NewString(),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("marshal Codex credit consumption request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		bu+"/backend-api/wham/rate-limit-reset-credits/consume",
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return 0, nil, fmt.Errorf("create Codex credit consumption request: %s", common.MaskSensitiveInfo(err.Error()))
	}
	setCodexWhamRequestHeaders(req, at, aid)
	req.Header.Set("Content-Type", "application/json")

	resp, err := DoUpstreamRequest(client, req)
	if err != nil {
		return 0, nil, fmt.Errorf("Codex credit consumption request failed: %s", common.MaskSensitiveInfo(err.Error()))
	}
	defer resp.Body.Close()

	body, err = readCodexWhamResponse(resp)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func setCodexWhamRequestHeaders(req *http.Request, accessToken string, accountID string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("Accept", "application/json")
	if req.Header.Get("originator") == "" {
		req.Header.Set("originator", "codex_cli_rs")
	}
}
