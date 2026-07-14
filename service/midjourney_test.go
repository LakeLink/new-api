package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestCoverPlusActionRejectsMalformedCustomIDs(t *testing.T) {
	for _, customID := range []string{"invalid", "MJ::JOB", "MJ::JOB::upsample", "MJ::JOB::variation", "MJ::JOB::upsample::9"} {
		t.Run(customID, func(t *testing.T) {
			request := &dto.MidjourneyRequest{CustomId: customID}
			require.NotNil(t, CoverPlusActionToNormalAction(request))
		})
	}
}

func TestCoverPlusActionParsesValidatedIndex(t *testing.T) {
	request := &dto.MidjourneyRequest{CustomId: "MJ::JOB::upsample::3::task-id"}
	require.Nil(t, CoverPlusActionToNormalAction(request))
	require.Equal(t, constant.MjActionUpscale, request.Action)
	require.Equal(t, 3, request.Index)
}

func TestConvertSimpleChangeParamsRejectsShortOrAmbiguousActions(t *testing.T) {
	for _, content := range []string{"task ", "task u", "task u10", "task v9", "task unknown"} {
		require.Nil(t, ConvertSimpleChangeParams(content), content)
	}

	reroll := ConvertSimpleChangeParams("task r")
	require.NotNil(t, reroll)
	require.Equal(t, "REROLL", reroll.Action)
}
