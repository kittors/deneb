package proto

import (
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

// DeviceMetrics is a host's latest sample. It is visible only to its owner,
// never on a peer's network map. Missing measurements are unavailable, not zero.
type DeviceMetrics struct {
	At            time.Time   `json:"at"`
	OS            string      `json:"os"`
	Arch          string      `json:"arch"`
	OSVersion     string      `json:"osVersion,omitempty"`
	CPUModel      string      `json:"cpuModel,omitempty"`
	CPUs          int         `json:"cpus"`
	CPU           *float64    `json:"cpu,omitempty"` // fraction of all logical CPUs, two samples
	Memory        *HostMemory `json:"memory,omitempty"`
	Disks         []HostDisk  `json:"disks,omitempty"`
	Uptime        *uint64     `json:"uptime,omitempty"` // seconds
	EngineVersion string      `json:"engineVersion,omitempty"`
	Errors        []string    `json:"errors,omitempty"`
}
type HostMemory struct {
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"` // OS free/reclaimable estimate
}
type HostDisk struct {
	Name      string `json:"name"` // mount/drive, no volume serials or user directory names
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"` // bytes available to the engine's user
}
type DeviceMetricsView struct {
	Devices map[DeviceID]DeviceMetrics `json:"devices"`
}

// Validate bounds untrusted self-reports before accepting them into memory.
func (m DeviceMetrics) Validate() error {
	if m.CPUs < 1 || m.CPUs > 65536 {
		return fmt.Errorf("invalid CPU count")
	}
	if m.CPU != nil && (math.IsNaN(*m.CPU) || math.IsInf(*m.CPU, 0) || *m.CPU < 0 || *m.CPU > 1) {
		return fmt.Errorf("invalid CPU usage")
	}
	if m.Memory != nil && (m.Memory.Total == 0 || m.Memory.Available > m.Memory.Total) {
		return fmt.Errorf("invalid memory")
	}
	if len(m.Disks) > 32 || len(m.Errors) > 8 {
		return fmt.Errorf("too many measurements")
	}
	for _, s := range append([]string{m.OS, m.Arch, m.OSVersion, m.CPUModel, m.EngineVersion}, m.Errors...) {
		if len(s) > 256 || !utf8.ValidString(s) {
			return fmt.Errorf("invalid metric text")
		}
		for _, r := range s {
			if r < 32 || r == 127 {
				return fmt.Errorf("invalid metric text")
			}
		}
	}
	seen := map[string]bool{}
	for _, d := range m.Disks {
		if d.Name == "" || len(d.Name) > 128 || !utf8.ValidString(d.Name) || seen[d.Name] || d.Total == 0 || d.Available > d.Total {
			return fmt.Errorf("invalid disk")
		}
		for _, r := range d.Name {
			if r < 32 || r == 127 {
				return fmt.Errorf("invalid disk name")
			}
		}
		seen[d.Name] = true
	}
	return nil
}
