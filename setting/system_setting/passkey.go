package system_setting

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type PasskeySettings struct {
	Enabled              bool   `json:"enabled"`
	RPDisplayName        string `json:"rp_display_name"`
	RPID                 string `json:"rp_id"`
	Origins              string `json:"origins"`
	AllowInsecureOrigin  bool   `json:"allow_insecure_origin"`
	UserVerification     string `json:"user_verification"`
	AttachmentPreference string `json:"attachment_preference"`
}

var defaultPasskeySettings = PasskeySettings{
	Enabled:              false,
	RPDisplayName:        common.SystemName,
	RPID:                 "",
	Origins:              "",
	AllowInsecureOrigin:  false,
	UserVerification:     "preferred",
	AttachmentPreference: "",
}

func (s PasskeySettings) Validate() error {
	switch strings.TrimSpace(s.UserVerification) {
	case "", "required", "preferred", "discouraged":
	default:
		return fmt.Errorf("passkey user verification must be required, preferred, or discouraged")
	}
	switch strings.TrimSpace(s.AttachmentPreference) {
	case "", "platform", "cross-platform":
	default:
		return fmt.Errorf("passkey attachment preference must be platform or cross-platform")
	}
	if rpID := strings.TrimSpace(s.RPID); rpID != "" {
		if strings.Contains(rpID, "://") || strings.ContainsAny(rpID, "/?#@") {
			return fmt.Errorf("passkey RP ID must be a host name without a scheme, path, or credentials")
		}
	}
	origins := strings.TrimSpace(s.Origins)
	if origins == "" || origins == "[]" {
		return nil
	}
	for _, origin := range strings.Split(origins, ",") {
		parsed, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Path != "" && parsed.Path != "/") {
			return fmt.Errorf("passkey origin %q must contain only an absolute scheme and host", origin)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "https":
		case "http":
			if !s.AllowInsecureOrigin {
				return fmt.Errorf("passkey origin %q requires allow_insecure_origin", origin)
			}
		default:
			return fmt.Errorf("passkey origin %q must use http or https", origin)
		}
	}
	return nil
}

func init() {
	config.GlobalConfig.Register("passkey", &defaultPasskeySettings)
}

func GetPasskeySettings() *PasskeySettings {
	setting := *config.Snapshot[PasskeySettings]("passkey")
	serverAddress := common.GetLegacyOptionString("ServerAddress", &ServerAddress)

	if setting.RPID == "" && serverAddress != "" {
		// 从ServerAddress提取域名作为RPID
		// ServerAddress可能是 "https://newapi.pro" 这种格式
		serverAddr := strings.TrimSpace(serverAddress)
		if parsed, err := url.Parse(serverAddr); err == nil && parsed.Host != "" {
			setting.RPID = parsed.Host
		} else {
			setting.RPID = serverAddr
		}
	}
	if setting.Origins == "" || setting.Origins == "[]" {
		setting.Origins = serverAddress
	}
	return &setting
}
