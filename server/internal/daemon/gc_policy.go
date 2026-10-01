package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The machine policy overrides inherited environment snapshots for every
// profile. It is read once at startup; changing it never interrupts active work.
func applyGCPolicyFile(cfg *Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("GC policy home: %w", err)
	}
	path := filepath.Join(home, ".multica", "gc-policy.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read GC policy: %w", err)
	}
	var policy struct {
		ArtifactsOnly  *bool   `json:"artifacts_only"`
		Interval       *string `json:"interval"`
		ArtifactTTL    *string `json:"artifact_ttl"`
		MinFreePercent *int    `json:"min_free_percent"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&policy); err != nil {
		return fmt.Errorf("invalid GC policy: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid GC policy: expected one JSON object")
	}
	next := *cfg
	if policy.ArtifactsOnly != nil {
		next.GCArtifactsOnly = *policy.ArtifactsOnly
	}
	if policy.Interval != nil {
		value, err := time.ParseDuration(*policy.Interval)
		if err != nil || value <= 0 {
			return fmt.Errorf("GC policy interval must be a positive duration")
		}
		next.GCInterval = value
	}
	if policy.ArtifactTTL != nil {
		value, err := time.ParseDuration(*policy.ArtifactTTL)
		if err != nil || value < 0 {
			return fmt.Errorf("GC policy artifact_ttl must be a nonnegative duration")
		}
		next.GCArtifactTTL = value
	}
	if policy.MinFreePercent != nil {
		if *policy.MinFreePercent < 0 || *policy.MinFreePercent > 99 {
			return fmt.Errorf("GC policy min_free_percent must be between 0 and 99")
		}
		next.GCMinFreePercent = *policy.MinFreePercent
	}
	*cfg = next
	return nil
}
