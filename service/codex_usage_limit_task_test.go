package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexWeeklyUsageRemainingPercent(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		remaining float64
		ok        bool
	}{
		{
			name:      "weekly primary window",
			body:      `{"rate_limit":{"primary_window":{"used_percent":72.5,"limit_window_seconds":604800}}}`,
			remaining: 27.5,
			ok:        true,
		},
		{
			name:      "weekly secondary window when primary is daily",
			body:      `{"rate_limit":{"primary_window":{"used_percent":99,"limit_window_seconds":3600},"secondary_window":{"used_percent":40,"limit_window_seconds":604800}}}`,
			remaining: 60,
			ok:        true,
		},
		{
			name: "no weekly window",
			body: `{"rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":3600}}}`,
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remaining, ok := codexWeeklyUsageRemainingPercent([]byte(tt.body))
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.InDelta(t, tt.remaining, remaining, 0.0001)
			}
		})
	}
}
