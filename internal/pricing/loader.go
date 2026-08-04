package pricing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	defaultprofiles "github.com/BlueSkyXN/AgentLedger/pricing"
)

func LoadDefaultProfile() (*Profile, error) {
	data, err := defaultprofiles.DefaultProfileBytes()
	if err != nil {
		return nil, err
	}
	return DecodeProfile(data)
}

func LoadProfileFile(path string) (*Profile, error) {
	path = expandHome(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read pricing profile: %w", err)
	}
	return DecodeProfile(data)
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func DecodeProfile(data []byte) (*Profile, error) {
	var profile Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("failed to parse pricing profile: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	sort.SliceStable(profile.Rules, func(i, j int) bool {
		return profile.Rules[i].Priority > profile.Rules[j].Priority
	})
	return &profile, nil
}
