package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"bot_astrosferum/internal/config"
)

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func Doctor(cfg config.Config) []Check {
	checks := []Check{
		checkDirectory("data directory", cfg.Paths.Data, true),
		checkDirectory("temporary directory", cfg.Paths.Temp, true),
		checkExecutable("grib_get", "-V"),
		checkExecutable("grib_ls", "-V"),
		checkExecutable("gdalinfo", "--version"),
	}
	checks = append(checks, checkFreeSpace(cfg.Paths.Data, int64(cfg.Sync.MinFreeSpace)))
	if cfg.Platforms.Telegram.Enabled {
		checks = append(checks, checkSecret("telegram token", cfg.Platforms.Telegram.TokenFile))
	}
	if cfg.Platforms.VK.Enabled {
		checks = append(checks, checkSecret("VK token", cfg.Platforms.VK.TokenFile))
	}
	return checks
}

func AllChecksPass(checks []Check) bool {
	for _, check := range checks {
		if !check.OK {
			return false
		}
	}
	return true
}

func checkDirectory(name, path string, writable bool) Check {
	info, err := os.Stat(path)
	if err != nil {
		return Check{Name: name, Detail: err.Error()}
	}
	if !info.IsDir() {
		return Check{Name: name, Detail: "not a directory"}
	}
	if writable {
		file, err := os.CreateTemp(path, ".doctor-*")
		if err != nil {
			return Check{Name: name, Detail: "not writable: " + err.Error()}
		}
		temporary := file.Name()
		if err := file.Close(); err != nil {
			return Check{Name: name, Detail: "close write probe: " + err.Error()}
		}
		if err := os.Remove(temporary); err != nil {
			return Check{Name: name, Detail: "remove write probe: " + err.Error()}
		}
	}
	return Check{Name: name, OK: true, Detail: filepath.Clean(path)}
}

func checkExecutable(name string, versionArgs ...string) Check {
	path, err := exec.LookPath(name)
	if err != nil {
		return Check{Name: name, Detail: "not found in PATH"}
	}
	detail := path
	if len(versionArgs) > 0 {
		output, versionErr := exec.Command(path, versionArgs...).CombinedOutput()
		if versionErr != nil {
			return Check{Name: name, Detail: "version check failed: " + versionErr.Error()}
		}
		for _, line := range strings.Split(string(output), "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				detail += " · " + trimmed
				break
			}
		}
	}
	return Check{Name: name, OK: true, Detail: detail}
}

func checkSecret(name, path string) Check {
	info, err := os.Stat(path)
	if err != nil {
		return Check{Name: name, Detail: err.Error()}
	}
	if !info.Mode().IsRegular() {
		return Check{Name: name, Detail: "not a regular file"}
	}
	if info.Size() == 0 {
		return Check{Name: name, Detail: "file is empty"}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Check{Name: name, Detail: fmt.Sprintf("permissions %04o are too broad", info.Mode().Perm())}
	}
	return Check{Name: name, OK: true, Detail: "present with restricted permissions"}
}

func checkFreeSpace(path string, minimum int64) Check {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return Check{Name: "free disk space", Detail: err.Error()}
	}
	available := int64(stats.Bavail) * int64(stats.Bsize)
	detail := fmt.Sprintf("available=%s minimum=%s", formatBytes(available), formatBytes(minimum))
	return Check{Name: "free disk space", OK: available >= minimum, Detail: detail}
}

func formatBytes(value int64) string {
	const gib = int64(1 << 30)
	if value >= gib {
		return fmt.Sprintf("%.1fGiB", float64(value)/float64(gib))
	}
	return strings.TrimSpace(fmt.Sprintf("%dB", value))
}
