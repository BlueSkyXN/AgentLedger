package model

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/oklog/ulid/v2"
)

type Device struct {
	DeviceID     string
	DeviceName   string
	Hostname     string
	OS           string
	Arch         string
	AppVersion   string
	CreatedAtMs  int64
	LastSeenAtMs int64
}

func CurrentDevice() (*Device, error) {
	hostname, _ := os.Hostname()
	now := time.Now().UnixMilli()
	deviceID, err := loadOrCreateDeviceID()
	if err != nil {
		return nil, fmt.Errorf("failed to get device ID: %w", err)
	}

	return &Device{
		DeviceID:     deviceID,
		DeviceName:   hostname,
		Hostname:     hostname,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		AppVersion:   "0.1.0",
		CreatedAtMs:  now,
		LastSeenAtMs: now,
	}, nil
}

func deviceIDPath() string {
	return filepath.Join(config.DataDir(), "device_id")
}

func loadOrCreateDeviceID() (string, error) {
	path := deviceIDPath()

	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}

	entropy := ulid.Monotonic(rand.Reader, 0)
	id := ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(id), 0644); err != nil {
		return "", fmt.Errorf("failed to write device_id: %w", err)
	}

	return id, nil
}
