package relay

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackedTaskResponseBody struct {
	io.Reader
	closed bool
}

func (b *trackedTaskResponseBody) Close() error {
	b.closed = true
	return nil
}

type acceptedResponseFailureAdaptor struct {
	channel.TaskAdaptor
	responseCalls int
}

func (a *acceptedResponseFailureAdaptor) DoResponse(
	_ *gin.Context,
	_ *http.Response,
	_ *relaycommon.RelayInfo,
) (string, []byte, *dto.TaskError) {
	a.responseCalls++
	return "", nil, service.TaskErrorWrapper(assert.AnError, "invalid_response", http.StatusInternalServerError)
}

type acceptedResponseBlankIDAdaptor struct {
	channel.TaskAdaptor
}

func (a *acceptedResponseBlankIDAdaptor) DoResponse(
	_ *gin.Context,
	_ *http.Response,
	_ *relaycommon.RelayInfo,
) (string, []byte, *dto.TaskError) {
	return " \t\n", []byte(`{"status":"accepted"}`), nil
}

func TestRelayTaskFetchRejectsUnknownModeWithoutCallingBuilder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	taskErr := RelayTaskFetch(ctx, relayconstant.RelayModeUnknown)

	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_relay_mode", taskErr.Code)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestValidateTaskSubmitUpstreamResponseRejectsNilResponse(t *testing.T) {
	taskErr := validateTaskSubmitUpstreamResponse(nil)

	require.NotNil(t, taskErr)
	assert.Equal(t, "empty_upstream_response", taskErr.Code)
	assert.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
}

func TestValidateTaskSubmitUpstreamResponseDoesNotRetryAcceptedNilBody(t *testing.T) {
	taskErr := validateTaskSubmitUpstreamResponse(&http.Response{
		StatusCode: http.StatusAccepted,
	})

	require.NotNil(t, taskErr)
	assert.Equal(t, "empty_upstream_response", taskErr.Code)
	assert.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
	assert.True(t, taskErr.SkipRetry)
}

func TestValidateTaskSubmitUpstreamResponseClosesRejectedResponse(t *testing.T) {
	body := &trackedTaskResponseBody{Reader: bytes.NewBufferString(`{"error":"rate limited"}`)}
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       body,
	}

	taskErr := validateTaskSubmitUpstreamResponse(resp)

	require.NotNil(t, taskErr)
	assert.Equal(t, "fail_to_fetch_task", taskErr.Code)
	assert.Equal(t, http.StatusTooManyRequests, taskErr.StatusCode)
	assert.True(t, body.closed)
}

func TestValidateTaskSubmitUpstreamResponseAcceptsAnySuccessStatus(t *testing.T) {
	for _, statusCode := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			body := &trackedTaskResponseBody{Reader: http.NoBody}
			resp := &http.Response{
				StatusCode: statusCode,
				Body:       body,
			}

			taskErr := validateTaskSubmitUpstreamResponse(resp)

			assert.Nil(t, taskErr)
			assert.False(t, body.closed, "the caller retains successful-response ownership")
		})
	}
}

func TestValidateTaskSubmitUpstreamResponseRejectsRedirects(t *testing.T) {
	for _, statusCode := range []int{http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			body := &trackedTaskResponseBody{Reader: http.NoBody}
			taskErr := validateTaskSubmitUpstreamResponse(&http.Response{
				StatusCode: statusCode,
				Body:       body,
			})

			require.NotNil(t, taskErr)
			assert.Equal(t, "fail_to_fetch_task", taskErr.Code)
			assert.Equal(t, statusCode, taskErr.StatusCode)
			assert.False(t, taskErr.SkipRetry, "a redirect is not proof that the task was accepted")
			assert.True(t, body.closed)
		})
	}
}

func TestValidateTaskSubmitUpstreamResponseNormalizesInvalidStatus(t *testing.T) {
	body := &trackedTaskResponseBody{Reader: http.NoBody}
	resp := &http.Response{
		StatusCode: 0,
		Body:       body,
	}

	taskErr := validateTaskSubmitUpstreamResponse(resp)

	require.NotNil(t, taskErr)
	assert.Equal(t, "fail_to_fetch_task", taskErr.Code)
	assert.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
	assert.Equal(t, http.StatusText(http.StatusBadGateway), taskErr.Message)
	assert.True(t, body.closed)
}

func TestValidateTaskSubmitUpstreamResponseBoundsErrorBody(t *testing.T) {
	upstreamBody := strings.NewReader(strings.Repeat("x", int(maxTaskSubmitErrorBodyBytes)+64))
	body := &trackedTaskResponseBody{Reader: upstreamBody}
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       body,
	}

	taskErr := validateTaskSubmitUpstreamResponse(resp)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
	assert.Equal(t, int(maxTaskSubmitErrorBodyBytes)+len(taskSubmitErrorTruncated), len(taskErr.Message))
	assert.True(t, strings.HasSuffix(taskErr.Message, taskSubmitErrorTruncated))
	assert.Equal(t, 63, upstreamBody.Len(), "only the limit plus one truncation byte is consumed")
	assert.True(t, body.closed)
}

func TestAcceptedTaskResponseErrorsAreNonRetryable(t *testing.T) {
	adaptor := &acceptedResponseFailureAdaptor{}

	_, _, taskErr := parseAcceptedTaskSubmitResponse(
		&gin.Context{},
		adaptor,
		&http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody},
		&relaycommon.RelayInfo{},
	)

	require.NotNil(t, taskErr)
	assert.True(t, taskErr.SkipRetry)
	assert.Equal(t, 1, adaptor.responseCalls)
}

func TestAcceptedTaskResponseRejectsBlankTaskIDWithoutRetry(t *testing.T) {
	_, _, taskErr := parseAcceptedTaskSubmitResponse(
		&gin.Context{},
		&acceptedResponseBlankIDAdaptor{},
		&http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody},
		&relaycommon.RelayInfo{},
	)

	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_response", taskErr.Code)
	assert.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
	assert.True(t, taskErr.SkipRetry)
}

func TestTaskSubmitRequestErrorClosesReturnedResponse(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		skipRetry bool
	}{
		{name: "upstream error", status: http.StatusBadGateway, skipRetry: false},
		{name: "accepted response with transport error", status: http.StatusAccepted, skipRetry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedTaskResponseBody{Reader: http.NoBody}

			taskErr := taskSubmitRequestError(&http.Response{
				StatusCode: test.status,
				Body:       body,
			}, assert.AnError)

			require.NotNil(t, taskErr)
			assert.Equal(t, "do_request_failed", taskErr.Code)
			assert.Equal(t, test.skipRetry, taskErr.SkipRetry)
			assert.True(t, body.closed)
		})
	}
}

func TestTaskBillingPreservesInheritedRatiosWhenCurrentEstimateIsEmpty(t *testing.T) {
	priceData := types.PriceData{Quota: 100}
	inherited := map[string]float64{
		"seconds": 8,
		"size":    1.5,
	}

	mergeTaskBillingRatios(&priceData, inherited, nil)
	quota, clamp := common.QuotaFromFloatChecked(priceData.ApplyOtherRatiosToFloat(float64(priceData.Quota)))

	require.Nil(t, clamp)
	assert.Equal(t, 1200, quota)
	assert.Equal(t, inherited, priceData.OtherRatios())
}

func TestTaskBillingRetryRebuildsOnlyFromOriginSnapshot(t *testing.T) {
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action:                             constant.TaskActionRemix,
			OriginTaskBillingRatios:            map[string]float64{"seconds": 8},
			OriginTaskBillingRatiosInitialized: true,
		},
	}

	require.True(t, beginTaskBillingAttempt(info, types.PriceData{Quota: 100}))
	mergeTaskBillingRatios(&info.PriceData, nil, map[string]float64{"first_attempt_size": 2})
	assert.Equal(t, map[string]float64{
		"seconds":            8,
		"first_attempt_size": 2,
	}, info.PriceData.OtherRatios())

	require.True(t, beginTaskBillingAttempt(info, types.PriceData{Quota: 200}))
	mergeTaskBillingRatios(&info.PriceData, nil, map[string]float64{"second_attempt_quality": 1.5})

	assert.Equal(t, 200, info.PriceData.Quota)
	assert.Equal(t, map[string]float64{
		"seconds":                8,
		"second_attempt_quality": 1.5,
	}, info.PriceData.OtherRatios())
	assert.False(t, info.PriceData.HasOtherRatio("first_attempt_size"))
}

func TestTaskBillingRejectsInvalidInheritedRemixMultiplier(t *testing.T) {
	for _, ratio := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		info := &relaycommon.RelayInfo{
			TaskRelayInfo: &relaycommon.TaskRelayInfo{
				Action:                             constant.TaskActionRemix,
				OriginTaskBillingRatios:            map[string]float64{"seconds": ratio},
				OriginTaskBillingRatiosInitialized: true,
			},
		}

		ok := beginTaskBillingAttempt(info, types.PriceData{Quota: 100})

		assert.False(t, ok)
		assert.Nil(t, info.PriceData.OtherRatios())
	}
}

func TestTaskBillingRejectsInvalidEstimatedMultiplier(t *testing.T) {
	for _, ratio := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		priceData := types.PriceData{Quota: 100}
		ok := mergeTaskBillingRatios(
			&priceData,
			nil,
			map[string]float64{"seconds": ratio},
		)

		assert.False(t, ok)
		assert.Nil(t, priceData.OtherRatios())
	}
}

func TestTaskBillingEstimatedQuotaReportsSaturation(t *testing.T) {
	priceData := types.PriceData{Quota: common.MaxQuota}
	priceData.AddOtherRatio("seconds", 2)

	quota, clamp := calculateTaskQuotaWithRatios(float64(priceData.Quota), &priceData)

	require.NotNil(t, clamp)
	assert.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	assert.Equal(t, common.MaxQuota, quota)
}

func TestTaskBillingPostSubmitAdjustmentRejectsSaturation(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	info.PriceData.Quota = 100

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{"seconds": 1e20})

	assert.False(t, ok)
	assert.Zero(t, quota)
	assert.Equal(t, 100, info.PriceData.Quota)
	require.NotNil(t, info.QuotaClamp)
	assert.Equal(t, common.QuotaClampOverflow, info.QuotaClamp.Kind)
}
