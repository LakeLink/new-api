package console_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateURLRejectsUnsafeAbsoluteURLForms(t *testing.T) {
	require.NoError(t, validateURL("https://status.example.com:8443/base", 1, "分组"))

	for _, rawURL := range []string{
		"https://status.example.com:99999/base",
		"https://user:password@status.example.com/base",
		"https://status.example.com/base#fragment",
	} {
		require.Error(t, validateURL(rawURL, 1, "分组"), rawURL)
	}
}
