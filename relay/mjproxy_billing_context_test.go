package relay

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetMidjourneyBillingContextPersistsCanonicalSettlementTaskID(t *testing.T) {
	originalNodeName := common.NodeName
	common.NodeName = "midjourney-origin-node"
	t.Cleanup(func() { common.NodeName = originalNodeName })

	relayInfo := &relaycommon.RelayInfo{
		RequestId:      "midjourney-billing-context-request",
		UserId:         71,
		TokenId:        72,
		BillingSource:  service.BillingSourceSubscription,
		SubscriptionId: 73,
		IsPlayground:   true,
	}
	const purpose = "midjourney-submit:IMAGINE"
	task := &model.Midjourney{}
	require.NoError(t, setMidjourneyBillingContext(task, relayInfo, purpose))
	expectedTaskID, err := service.DurableQuotaAdjustmentTaskID(relayInfo, purpose)
	require.NoError(t, err)

	assert.Equal(t, relayInfo.RequestId, task.BillingRequestId)
	assert.Equal(t, purpose, task.BillingPurpose)
	assert.Equal(t, service.BillingSourceSubscription, task.BillingSource)
	assert.Equal(t, relayInfo.SubscriptionId, task.BillingSubscriptionId)
	assert.Equal(t, expectedTaskID, task.BillingTaskId)
	assert.Equal(t, relayInfo.TokenId, task.BillingTokenId)
	assert.True(t, task.BillingIsPlayground)
	assert.Equal(t, "midjourney-origin-node", task.BillingNodeName)
}

func TestSetMidjourneyBillingContextLeavesUnbilledTaskWithoutSettlementID(t *testing.T) {
	task := &model.Midjourney{BillingTaskId: "stale-task-id"}
	relayInfo := &relaycommon.RelayInfo{RequestId: "midjourney-unbilled-context", UserId: 74}

	require.NoError(t, setMidjourneyBillingContext(task, relayInfo, ""))
	assert.Empty(t, task.BillingTaskId)
	assert.Empty(t, task.BillingPurpose)
}

func TestAcceptedMidjourneySubmitCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   int
		accepted   bool
	}{
		{name: "created", statusCode: http.StatusOK, response: 1, accepted: true},
		{name: "existing", statusCode: http.StatusOK, response: 21, accepted: true},
		{name: "queued", statusCode: http.StatusOK, response: 22, accepted: true},
		{name: "provider rejection", statusCode: http.StatusOK, response: 4, accepted: false},
		{name: "http failure", statusCode: http.StatusBadGateway, response: 1, accepted: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.accepted, isAcceptedMidjourneySubmit(test.statusCode, test.response))
		})
	}
}
