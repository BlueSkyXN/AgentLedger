//go:build !darwin || !cgo

package adapters

import "fmt"

func traeWorkCNPlatformSupported() bool { return false }

func findTraeWorkCNProcesses(paths []string) ([]int, error) {
	return nil, nil
}

func signalTraeWorkCNInspector(pid int) error {
	return fmt.Errorf("unsupported platform")
}
