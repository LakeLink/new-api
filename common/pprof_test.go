package common

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPruneCPUProfilesRetainsNewestBoundedSet(t *testing.T) {
	directory := t.TempDir()
	for index := 0; index < 4; index++ {
		path := filepath.Join(directory, fmt.Sprintf("cpu-%d.pprof", index))
		require.NoError(t, os.WriteFile(path, []byte("profile"), 0600))
		modTime := time.Unix(int64(index+1), 0)
		require.NoError(t, os.Chtimes(path, modTime, modTime))
	}
	unrelated := filepath.Join(directory, "keep.txt")
	require.NoError(t, os.WriteFile(unrelated, []byte("unrelated"), 0600))

	require.NoError(t, pruneCPUProfiles(directory, 2))

	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	assert.ElementsMatch(t, []string{
		"cpu-2.pprof",
		"cpu-3.pprof",
		"keep.txt",
	}, names)
}

func TestPruneCPUProfilesZeroRemovesOnlyProfiles(t *testing.T) {
	directory := t.TempDir()
	profile := filepath.Join(directory, "cpu-old.pprof")
	require.NoError(t, os.WriteFile(profile, []byte("profile"), 0600))
	unrelated := filepath.Join(directory, "heap-old.pprof")
	require.NoError(t, os.WriteFile(unrelated, []byte("heap"), 0600))

	require.NoError(t, pruneCPUProfiles(directory, 0))

	assert.NoFileExists(t, profile)
	assert.FileExists(t, unrelated)
}
