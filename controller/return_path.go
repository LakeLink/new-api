package controller

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

func paymentReturnPath(suffix string) string {
	base := strings.TrimRight(system_setting.GetServerAddress(), "/")
	return base + common.ThemeAwarePath(suffix)
}

func parsePaymentCallbackURL(rawURL string) (*url.URL, error) {
	parsed, err := common.ParseAbsoluteHTTPURL(strings.TrimSpace(rawURL))
	if err != nil || parsed.RawQuery != "" {
		return nil, fmt.Errorf("payment callback URL must be an absolute HTTP(S) URL")
	}
	return parsed, nil
}
