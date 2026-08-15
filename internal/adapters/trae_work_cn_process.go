package adapters

import (
	"os"
	"path/filepath"
	"strings"
)

const traeWorkCNExecutableSuffix = "/TRAE SOLO CN.app/Contents/MacOS/Electron"

func matchesTraeWorkCNExecutable(executable string, configuredPaths []string) bool {
	executable = canonicalLocalPath(executable)
	if !strings.HasSuffix(filepath.ToSlash(executable), traeWorkCNExecutableSuffix) {
		return false
	}
	if len(configuredPaths) == 0 {
		return true
	}
	for _, configuredPath := range configuredPaths {
		configuredPath = strings.TrimSpace(expandLocalHome(configuredPath))
		if configuredPath == "" {
			continue
		}
		if strings.HasSuffix(strings.ToLower(configuredPath), ".app") {
			configuredPath = filepath.Join(configuredPath, "Contents", "MacOS", "Electron")
		}
		if executable == canonicalLocalPath(configuredPath) {
			return true
		}
	}
	return false
}

func canonicalLocalPath(path string) string {
	cleaned := filepath.Clean(path)
	if absolute, err := filepath.Abs(cleaned); err == nil {
		cleaned = absolute
	}
	if evaluated, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = evaluated
	}
	return filepath.Clean(cleaned)
}

func expandLocalHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
