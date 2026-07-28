package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTextOtherInfoRecordsCohereSearchUnitBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/rerank", nil)
	searchUnits := float64(3)
	start := time.Unix(1_700_000_000, 0)
	info := &relaycommon.RelayInfo{
		StartTime:         start,
		FirstResponseTime: start.Add(time.Second),
		ChannelMeta:       &relaycommon.ChannelMeta{},
		RerankerInfo: &relaycommon.RerankerInfo{
			CohereSearchUnits: &searchUnits,
		},
	}

	other := GenerateTextOtherInfo(ctx, info, 0, 1, 0, 0, 0, 2.5/1000, -1)
	require.NotNil(t, other)
	assert.Equal(t, "search", other["billing_unit"])
	assert.Equal(t, float64(3), other["search_units"])
	assert.Equal(t, 2.5/1000, other["search_unit_price"])
}

func TestCalculateTextQuotaSummaryBillsCohereProviderSearchUnits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	searchUnits := float64(3)
	info := &relaycommon.RelayInfo{
		OriginModelName: "rerank-v4.0-pro",
		StartTime:       time.Now(),
		RerankerInfo: &relaycommon.RerankerInfo{
			CohereSearchUnits: &searchUnits,
		},
		PriceData: types.PriceData{
			UsePrice:   true,
			ModelPrice: 2.5 / 1000,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 2,
			},
		},
	}
	info.PriceData.AddOtherRatio(relaycommon.CohereRerankSearchUnitsRatioKey, searchUnits)

	// Cohere Rerank reports search units rather than tokens. A disabled local
	// tokenizer can therefore legitimately leave token usage at zero without
	// making the provider's search-unit charge disappear.
	summary := calculateTextQuotaSummary(ctx, info, &dto.Usage{})
	assert.Equal(t, 7500, summary.Quota)
	assert.Nil(t, info.QuotaClamp)
}
