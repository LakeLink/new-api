package channel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type apiRequestURLAdaptor struct {
	Adaptor
	requestURL string
	requestErr error
}

func (a apiRequestURLAdaptor) GetRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.requestURL, a.requestErr
}

type taskRequestBuilder struct {
	TaskAdaptor
	requestURL string
	requestErr error
}

func (a taskRequestBuilder) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	if a.requestErr != nil {
		return "", a.requestErr
	}
	if a.requestURL != "" {
		return a.requestURL, nil
	}
	return "https://example.com/v1/tasks", nil
}

func (taskRequestBuilder) BuildRequestHeader(
	_ *gin.Context,
	req *http.Request,
	_ *relaycommon.RelayInfo,
) error {
	req.Header.Set("Content-Type", "application/json")
	return nil
}

func TestDoRequestRebindsManualRequestToRelayCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	relayCtx, cancelRelay := context.WithCancel(context.Background())
	info := &relaycommon.RelayInfo{RelayCancelCtx: relayCtx}
	manualRequest, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	require.NoError(t, err)

	boundRequest, err := bindRelayContext(c, manualRequest, info)
	require.NoError(t, err)
	cancelRelay()
	require.ErrorIs(t, boundRequest.Context().Err(), context.Canceled)
	require.NoError(t, manualRequest.Context().Err())
}

func TestGetRelayCtxUsesRelayCancellationAndRequestFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestCtx)

	relayCtx, cancelRelay := context.WithCancel(context.Background())
	cancelRelay()
	got := getRelayCtx(ctx, &relaycommon.RelayInfo{RelayCancelCtx: relayCtx})
	require.ErrorIs(t, got.Err(), context.Canceled)
	require.NoError(t, requestCtx.Err())

	cancelRequest()
	got = getRelayCtx(ctx, &relaycommon.RelayInfo{})
	require.ErrorIs(t, got.Err(), context.Canceled)

	got = getRelayCtx(nil, nil)
	require.NoError(t, got.Err())
}

func TestRequestConstructionErrorsDoNotLeakUpstreamSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{}
	secretURL := "https://user:password-secret@example.com/v1?api_key=query-secret%zz"
	malformedSecretURL := "https://user:password-secret@example.com/v1?api_key=query-secret\n"

	for _, testCase := range []struct {
		name string
		call func() error
	}{
		{
			name: "api request URL error",
			call: func() error {
				_, err := DoApiRequest(
					apiRequestURLAdaptor{requestErr: errors.New("failed for " + secretURL)},
					ctx,
					info,
					nil,
				)
				return err
			},
		},
		{
			name: "form request URL error",
			call: func() error {
				_, err := DoFormRequest(
					apiRequestURLAdaptor{requestErr: errors.New("failed for " + secretURL)},
					ctx,
					info,
					nil,
				)
				return err
			},
		},
		{
			name: "websocket request URL error",
			call: func() error {
				_, err := DoWssRequest(
					apiRequestURLAdaptor{requestErr: errors.New("failed for " + secretURL)},
					ctx,
					info,
					nil,
				)
				return err
			},
		},
		{
			name: "malformed request URL",
			call: func() error {
				_, err := DoApiRequest(
					apiRequestURLAdaptor{requestURL: malformedSecretURL},
					ctx,
					info,
					nil,
				)
				return err
			},
		},
		{
			name: "task request URL error",
			call: func() error {
				_, err := buildTaskAPIRequest(
					taskRequestBuilder{requestErr: errors.New("failed for " + secretURL)},
					ctx,
					info,
					nil,
				)
				return err
			},
		},
		{
			name: "malformed task request URL",
			call: func() error {
				_, err := buildTaskAPIRequest(
					taskRequestBuilder{requestURL: malformedSecretURL},
					ctx,
					info,
					nil,
				)
				return err
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.call()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "password-secret")
			assert.NotContains(t, err.Error(), "query-secret")
			assert.NotContains(t, err.Error(), "example.com")
		})
	}
}

func TestLimitUpstreamResponseBodyBoundsBufferedAndErrorResponses(t *testing.T) {
	oldLimit := constant.MaxUpstreamResponseBodyMB
	constant.MaxUpstreamResponseBodyMB = 1
	t.Cleanup(func() { constant.MaxUpstreamResponseBodyMB = oldLimit })
	tooLargeBody := strings.Repeat("x", (1<<20)+1)

	for _, tt := range []struct {
		name       string
		isStream   bool
		statusCode int
		wantError  bool
	}{
		{name: "buffered success", statusCode: http.StatusOK, wantError: true},
		{name: "stream error", isStream: true, statusCode: http.StatusBadRequest, wantError: true},
		{name: "stream success remains incremental", isStream: true, statusCode: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(strings.NewReader(tooLargeBody)),
			}
			limitUpstreamResponseBody(resp, &relaycommon.RelayInfo{IsStream: tt.isStream})
			body, err := io.ReadAll(resp.Body)
			if tt.wantError {
				var maxBytesError *http.MaxBytesError
				require.Error(t, err)
				require.True(t, errors.As(err, &maxBytesError))
				require.Len(t, body, 1<<20)
			} else {
				require.NoError(t, err)
				require.Len(t, body, len(tooLargeBody))
			}
		})
	}
}

func TestBuildTaskAPIRequestUsesSafeRedirectReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	payload := []byte(`{"model":"video-model","prompt":"hello"}`)

	req, err := buildTaskAPIRequest(
		taskRequestBuilder{},
		ctx,
		&relaycommon.RelayInfo{},
		bytes.NewReader(payload),
	)
	require.NoError(t, err)
	require.NotNil(t, req.GetBody, "bytes.Reader has an independent net/http replay snapshot")

	sentBody, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, payload, sentBody)

	replayedBody, err := req.GetBody()
	require.NoError(t, err)
	defer replayedBody.Close()
	replayed, err := io.ReadAll(replayedBody)
	require.NoError(t, err)
	require.Equal(t, payload, replayed, "a 307/308 replay must not reuse the exhausted original reader")
}

func TestBuildTaskAPIRequestDoesNotInventReplayForOpaqueBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	opaqueBody := struct{ io.Reader }{Reader: strings.NewReader("one-shot")}

	req, err := buildTaskAPIRequest(
		taskRequestBuilder{},
		ctx,
		&relaycommon.RelayInfo{},
		opaqueBody,
	)
	require.NoError(t, err)
	require.Nil(t, req.GetBody, "net/http must return 307/308 instead of replaying an exhausted unknown reader")
}

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")
	ctx.Request.Header.Set("X-Forwarded-For", "127.0.0.1")
	ctx.Request.Header.Set("Forwarded", "for=127.0.0.1")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
	_, hasXForwardedFor := headers["x-forwarded-for"]
	require.False(t, hasXForwardedFor)
	_, hasForwarded := headers["forwarded"]
	require.False(t, hasForwarded)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}
