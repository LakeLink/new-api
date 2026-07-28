package system_setting

import (
	"fmt"
	"net"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type FetchSetting struct {
	EnableSSRFProtection   bool     `json:"enable_ssrf_protection"` // 是否启用SSRF防护
	AllowPrivateIp         bool     `json:"allow_private_ip"`
	DomainFilterMode       bool     `json:"domain_filter_mode"`         // 域名过滤模式，true: 白名单模式，false: 黑名单模式
	IpFilterMode           bool     `json:"ip_filter_mode"`             // IP过滤模式，true: 白名单模式，false: 黑名单模式
	DomainList             []string `json:"domain_list"`                // domain format, e.g. example.com, *.example.com
	IpList                 []string `json:"ip_list"`                    // CIDR format
	AllowedPorts           []string `json:"allowed_ports"`              // port range format, e.g. 80, 443, 8000-9000
	ApplyIPFilterForDomain bool     `json:"apply_ip_filter_for_domain"` // 对域名启用IP过滤（实验性）
}

var defaultFetchSetting = FetchSetting{
	EnableSSRFProtection:   true, // 默认开启SSRF防护
	AllowPrivateIp:         false,
	DomainFilterMode:       false,
	IpFilterMode:           false,
	DomainList:             []string{},
	IpList:                 []string{},
	AllowedPorts:           []string{"80", "443", "8080", "8443"},
	ApplyIPFilterForDomain: true,
}

func (s FetchSetting) Validate() error {
	if len(s.DomainList) > 4_096 || len(s.IpList) > 4_096 || len(s.AllowedPorts) > 4_096 {
		return fmt.Errorf("fetch protection lists cannot contain more than 4096 entries")
	}
	for _, pattern := range s.DomainList {
		if !validFetchDomainPattern(pattern) {
			return fmt.Errorf("invalid fetch domain pattern %q", pattern)
		}
	}
	for _, entry := range s.IpList {
		if entry == "" || entry != strings.TrimSpace(entry) {
			return fmt.Errorf("invalid fetch IP or CIDR %q", entry)
		}
		if net.ParseIP(entry) == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return fmt.Errorf("invalid fetch IP or CIDR %q", entry)
			}
		}
	}
	if _, err := common.NewSSRFProtectionFromFetchSetting(
		s.AllowPrivateIp,
		s.DomainFilterMode,
		s.IpFilterMode,
		s.DomainList,
		s.IpList,
		s.AllowedPorts,
		s.ApplyIPFilterForDomain,
	); err != nil {
		return err
	}
	return nil
}

func validFetchDomainPattern(pattern string) bool {
	if pattern == "" || pattern != strings.TrimSpace(pattern) {
		return false
	}
	if strings.HasPrefix(pattern, "*.") {
		pattern = strings.TrimPrefix(pattern, "*.")
	}
	if len(pattern) == 0 || len(pattern) > 253 || strings.HasSuffix(pattern, ".") || net.ParseIP(pattern) != nil {
		return false
	}
	for _, label := range strings.Split(pattern, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
				(char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("fetch_setting", &defaultFetchSetting)
}

func GetFetchSetting() *FetchSetting {
	return config.Snapshot[FetchSetting]("fetch_setting")
}
