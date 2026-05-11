package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldSendWalletQuotaNotify(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		userQuota         int
		consumeQuota      int
		threshold         int
		expectedNotify    bool
		expectedRemaining int
	}{
		{
			name:              "crosses from above threshold to below threshold",
			userQuota:         1500,
			consumeQuota:      600,
			threshold:         1000,
			expectedNotify:    true,
			expectedRemaining: 900,
		},
		{
			name:              "already below threshold does not notify again",
			userQuota:         900,
			consumeQuota:      100,
			threshold:         1000,
			expectedNotify:    false,
			expectedRemaining: 800,
		},
		{
			name:              "remaining quota at threshold does not notify",
			userQuota:         1500,
			consumeQuota:      500,
			threshold:         1000,
			expectedNotify:    false,
			expectedRemaining: 1000,
		},
		{
			name:              "equal threshold before consume notifies when crossing below",
			userQuota:         1000,
			consumeQuota:      1,
			threshold:         1000,
			expectedNotify:    true,
			expectedRemaining: 999,
		},
		{
			name:              "refund does not notify",
			userQuota:         900,
			consumeQuota:      -200,
			threshold:         1000,
			expectedNotify:    false,
			expectedRemaining: 1100,
		},
		{
			name:              "zero consume does not notify",
			userQuota:         900,
			consumeQuota:      0,
			threshold:         1000,
			expectedNotify:    false,
			expectedRemaining: 900,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			shouldNotify, remainingQuota := shouldSendWalletQuotaNotify(tc.userQuota, tc.consumeQuota, tc.threshold)

			require.Equal(t, tc.expectedNotify, shouldNotify)
			require.Equal(t, tc.expectedRemaining, remainingQuota)
		})
	}
}
