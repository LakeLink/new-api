package common

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnsureDiskCacheDirUsesOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix directory permission bits")
	}

	originalConfig := GetDiskCacheConfig()
	t.Cleanup(func() {
		SetDiskCacheConfig(originalConfig)
	})

	SetDiskCacheConfig(DiskCacheConfig{Path: t.TempDir()})
	dir := GetDiskCacheDir()
	require.NoError(t, os.MkdirAll(dir, 0755))

	require.NoError(t, EnsureDiskCacheDir())

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), info.Mode().Perm())
}
