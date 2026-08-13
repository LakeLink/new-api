package relay

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// refreshFinalImagenRequest validates and accounts for the exact Imagen
// predict payload after disabled-field filtering and parameter overrides.
// It returns zero for non-Imagen requests.
func refreshFinalImagenRequest(info *relaycommon.RelayInfo, jsonData []byte) (int, error) {
	if !isFinalImagenRequest(info) {
		return 0, nil
	}

	var request struct {
		Instances  []dto.GeminiImageInstance `json:"instances"`
		Parameters struct {
			SampleCount *int `json:"sampleCount,omitempty"`
		} `json:"parameters"`
	}
	if err := common.Unmarshal(jsonData, &request); err != nil {
		return 0, fmt.Errorf("decode final Imagen request: %w", err)
	}
	if len(request.Instances) != 1 || strings.TrimSpace(request.Instances[0].Prompt) == "" {
		return 0, errors.New("final Imagen instances must contain exactly one non-empty prompt")
	}
	if request.Parameters.SampleCount == nil {
		return 0, fmt.Errorf("final Imagen parameters.sampleCount must be an integer between 1 and %d", relaycommon.MaxImagenImageCount)
	}
	imageCount := *request.Parameters.SampleCount
	if imageCount < 1 || imageCount > relaycommon.MaxImagenImageCount {
		return 0, fmt.Errorf("final Imagen parameters.sampleCount must be an integer between 1 and %d", relaycommon.MaxImagenImageCount)
	}
	if info.TieredBillingSnapshot != nil {
		initialCount, ok := initialImageRequestCount(info)
		if !ok {
			return 0, errors.New("cannot verify final Imagen sampleCount for tiered billing")
		}
		if uint(imageCount) != initialCount {
			return 0, errors.New("final Imagen sampleCount changed after tiered pre-consume and cannot be safely repriced")
		}
		return imageCount, nil
	}
	if err := relaycommon.SyncImagenBilling(info, imageCount); err != nil {
		return 0, err
	}
	return imageCount, nil
}

func isFinalImagenRequest(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil || !strings.HasPrefix(info.UpstreamModelName, "imagen") {
		return false
	}
	return info.ApiType == constant.APITypeGemini || info.ApiType == constant.APITypeVertexAi
}

func initialImageRequestCount(info *relaycommon.RelayInfo) (uint, bool) {
	if info == nil {
		return 0, false
	}
	request, ok := info.Request.(*dto.ImageRequest)
	if !ok || request == nil {
		return 0, false
	}
	if request.N == nil {
		return 1, true
	}
	if *request.N < 1 || *request.N > dto.MaxImageN {
		return 0, false
	}
	return *request.N, true
}
