package common

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/cpu"
)

const (
	cpuProfileDirectory = "./pprof"
	maxCPUProfileFiles  = 20
)

// Monitor captures a short CPU profile when sustained process CPU usage is
// high. It is best-effort diagnostics and must never crash the gateway.
func Monitor(ctx context.Context) {
	for {
		percent, err := cpu.PercentWithContext(ctx, time.Second, false)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			SysError("failed to read CPU usage: " + err.Error())
		} else if len(percent) == 0 {
			SysError("failed to read CPU usage: empty sample")
		} else if percent[0] > 80 {
			if err := os.MkdirAll(cpuProfileDirectory, 0700); err != nil {
				SysError("failed to create pprof directory: " + err.Error())
			} else {
				fileName := fmt.Sprintf(
					"cpu-%s.pprof",
					time.Now().UTC().Format("20060102T150405.000000000Z"),
				)
				path := filepath.Join(cpuProfileDirectory, fileName)
				profileFile, createErr := os.OpenFile(
					path,
					os.O_CREATE|os.O_EXCL|os.O_WRONLY,
					0600,
				)
				if createErr != nil {
					SysError("failed to create CPU profile: " + createErr.Error())
				} else if startErr := pprof.StartCPUProfile(profileFile); startErr != nil {
					_ = profileFile.Close()
					_ = os.Remove(path)
					SysError("failed to start CPU profile: " + startErr.Error())
				} else {
					timer := time.NewTimer(10 * time.Second)
					select {
					case <-ctx.Done():
						if !timer.Stop() {
							select {
							case <-timer.C:
							default:
							}
						}
					case <-timer.C:
					}
					pprof.StopCPUProfile()
					if closeErr := profileFile.Close(); closeErr != nil {
						SysError("failed to close CPU profile: " + closeErr.Error())
					}
					if pruneErr := pruneCPUProfiles(
						cpuProfileDirectory,
						maxCPUProfileFiles,
					); pruneErr != nil {
						SysError("failed to prune CPU profiles: " + pruneErr.Error())
					}
					if ctx.Err() != nil {
						return
					}
				}
			}
		}

		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func pruneCPUProfiles(directory string, keep int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	type profileEntry struct {
		name    string
		modTime time.Time
	}
	profiles := make([]profileEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasPrefix(entry.Name(), "cpu-") ||
			!strings.HasSuffix(entry.Name(), ".pprof") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		profiles = append(profiles, profileEntry{
			name:    entry.Name(),
			modTime: info.ModTime(),
		})
	}
	if keep < 0 {
		keep = 0
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].modTime.Equal(profiles[j].modTime) {
			return profiles[i].name < profiles[j].name
		}
		return profiles[i].modTime.Before(profiles[j].modTime)
	})
	for len(profiles) > keep {
		if err := os.Remove(filepath.Join(directory, profiles[0].name)); err != nil {
			return err
		}
		profiles = profiles[1:]
	}
	return nil
}
