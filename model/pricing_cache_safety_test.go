package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPricingCacheGettersReturnCallerOwnedSnapshots(t *testing.T) {
	ratio := 0.5
	updatePricingLock.Lock()
	previousPricing := pricingMap
	previousVendors := vendorsList
	previousEndpoints := supportedEndpointMap
	previousUpdatedAt := lastGetPricingTime
	pricingMap = []Pricing{{
		ModelName:              "snapshot-model",
		EnableGroup:            []string{"default"},
		SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI},
		CacheRatio:             &ratio,
	}}
	vendorsList = []PricingVendor{{ID: 1, Name: "snapshot-vendor"}}
	supportedEndpointMap = map[string]common.EndpointInfo{
		"chat": {Path: "/v1/chat/completions", Method: "POST"},
	}
	lastGetPricingTime = time.Now()
	updatePricingLock.Unlock()

	modelSupportEndpointsLock.Lock()
	previousModelEndpoints := modelSupportEndpointTypes
	modelSupportEndpointTypes = map[string][]constant.EndpointType{
		"snapshot-model": {constant.EndpointTypeOpenAI},
	}
	modelSupportEndpointsLock.Unlock()

	modelEnableGroupsLock.Lock()
	previousEnableGroups := modelEnableGroups
	modelEnableGroups = map[string][]string{"snapshot-model": {"default"}}
	modelEnableGroupsLock.Unlock()

	t.Cleanup(func() {
		updatePricingLock.Lock()
		pricingMap = previousPricing
		vendorsList = previousVendors
		supportedEndpointMap = previousEndpoints
		lastGetPricingTime = previousUpdatedAt
		updatePricingLock.Unlock()

		modelSupportEndpointsLock.Lock()
		modelSupportEndpointTypes = previousModelEndpoints
		modelSupportEndpointsLock.Unlock()

		modelEnableGroupsLock.Lock()
		modelEnableGroups = previousEnableGroups
		modelEnableGroupsLock.Unlock()
	})

	firstPricing := GetPricing()
	require.Len(t, firstPricing, 1)
	firstPricing[0].EnableGroup[0] = "mutated"
	firstPricing[0].SupportedEndpointTypes[0] = constant.EndpointTypeGemini
	*firstPricing[0].CacheRatio = 9
	assert.Equal(t, "default", GetPricing()[0].EnableGroup[0])
	assert.Equal(t, constant.EndpointTypeOpenAI, GetPricing()[0].SupportedEndpointTypes[0])
	assert.Equal(t, 0.5, *GetPricing()[0].CacheRatio)

	firstVendors := GetVendors()
	require.Len(t, firstVendors, 1)
	firstVendors[0].Name = "mutated"
	assert.Equal(t, "snapshot-vendor", GetVendors()[0].Name)

	firstEndpointMap := GetSupportedEndpointMap()
	firstEndpointMap["chat"] = common.EndpointInfo{Path: "/mutated"}
	assert.Equal(t, "/v1/chat/completions", GetSupportedEndpointMap()["chat"].Path)

	firstModelEndpoints := GetModelSupportEndpointTypes("snapshot-model")
	require.Len(t, firstModelEndpoints, 1)
	firstModelEndpoints[0] = constant.EndpointTypeGemini
	assert.Equal(t, constant.EndpointTypeOpenAI, GetModelSupportEndpointTypes("snapshot-model")[0])

	firstGroups := GetModelEnableGroups("snapshot-model")
	require.Len(t, firstGroups, 1)
	firstGroups[0] = "mutated"
	assert.Equal(t, "default", GetModelEnableGroups("snapshot-model")[0])
}
