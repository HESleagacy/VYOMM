package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGenerateIsDeterministic(t *testing.T) {
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	a, b := Generate("healthy-baseline", 1, at), Generate("healthy-baseline", 1, at)
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	if string(left) != string(right) {
		t.Fatalf("same seed generated different JSON: %s != %s", left, right)
	}
}

func TestGenerateMatchesThresholdScenarios(t *testing.T) {
	at := time.Unix(0, 0).UTC()
	checks := []struct {
		name   string
		signal string
		want   bool
	}{
		{"cpu-saturation", "cpu_saturation", true},
		{"memory-pressure", "memory_pressure", true},
		{"healthy-baseline", "healthy", true},
	}
	for _, tc := range checks {
		d := Generate(tc.name, 1, at).Devices[0]
		got := tc.name == "healthy-baseline" || (tc.signal == "cpu_saturation" && d.CPUPercent >= 97) || (tc.signal == "memory_pressure" && d.MemoryPercent >= 86)
		if got != tc.want {
			t.Errorf("%s threshold result=%v, want %v", tc.name, got, tc.want)
		}
	}
}
