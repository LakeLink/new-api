package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestDoWorkerRequestValidatesTargetBeforeDispatch(t *testing.T) {
	originalWorkerURL := system_setting.WorkerUrl
	originalAllowHTTP := system_setting.WorkerAllowHttpImageRequestEnabled
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() {
		system_setting.WorkerUrl = originalWorkerURL
		system_setting.WorkerAllowHttpImageRequestEnabled = originalAllowHTTP
		*fetchSetting = originalFetchSetting
	})

	system_setting.WorkerUrl = "https://worker.example"
	system_setting.WorkerAllowHttpImageRequestEnabled = true
	fetchSetting.EnableSSRFProtection = false

	_, err := DoWorkerRequest(nil)
	require.ErrorContains(t, err, "worker request is nil")

	for _, target := range []string{
		"",
		"httpsx://example.com/file",
		"gopher://example.com/file",
		"/relative/file",
		"https://user:secret@example.com/file",
		"https://example.com/file#fragment",
	} {
		t.Run(target, func(t *testing.T) {
			_, err := DoWorkerRequest(&WorkerRequest{URL: target})
			require.Error(t, err)
		})
	}
}

func TestDoWorkerRequestAppliesSSRFPolicyBeforeWorkerDispatch(t *testing.T) {
	originalWorkerURL := system_setting.WorkerUrl
	originalAllowHTTP := system_setting.WorkerAllowHttpImageRequestEnabled
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() {
		system_setting.WorkerUrl = originalWorkerURL
		system_setting.WorkerAllowHttpImageRequestEnabled = originalAllowHTTP
		*fetchSetting = originalFetchSetting
	})

	system_setting.WorkerUrl = "https://worker.example"
	system_setting.WorkerAllowHttpImageRequestEnabled = true
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = nil
	fetchSetting.ApplyIPFilterForDomain = true

	_, err := DoWorkerRequest(&WorkerRequest{URL: "http://127.0.0.1/internal"})

	require.ErrorContains(t, err, "private IP address not allowed")
}
