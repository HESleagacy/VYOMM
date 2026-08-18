// Command vyomm-simulator emits deterministic contract-shaped telemetry.
// It is intentionally small until the Controller's API runner is available.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/observability/logging"
	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

type request struct {
	RunID      string                      `json:"run_id"`
	ScenarioID string                      `json:"scenario_id"`
	Devices    []telemetry.DeviceTelemetry `json:"devices"`
	Logs       []string                    `json:"logs"`
}

func device(name string, role telemetry.DeviceRole, cpu, memory, bandwidth, temperature, latency, loss float64, at time.Time) telemetry.DeviceTelemetry {
	return telemetry.DeviceTelemetry{Hostname: name, Role: role, CPUPercent: cpu, MemoryPercent: memory, BandwidthPercent: bandwidth, TemperatureC: temperature, LatencyMS: latency, PacketLossPercent: loss, UptimeSeconds: 3600, Status: telemetry.StatusHealthy, ObservedAt: at, Source: telemetry.SourceSynthetic, Mode: telemetry.ModeTrial}
}

// Generate returns one deterministic batch. Scenario thresholds are derived
// from internal/detection/anomaly.go and internal/incidents/incidents.go.
func Generate(scenario string, seed int64, at time.Time) request {
	r := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
	base := device("rtr-01", telemetry.RoleRouter, 40+float64(r.IntN(10)), 40, 20, 50, 15, .1, at)
	switch scenario {
	case "cpu-saturation":
		base.CPUPercent = 98
	case "memory-pressure":
		base.MemoryPercent, base.CPUPercent = 95, 85
	case "high-latency":
		base.LatencyMS = 90
	case "packet-loss":
		base.PacketLossPercent, base.LatencyMS = 8.5, 130
	case "healthy-baseline":
	default:
		panic(fmt.Sprintf("unknown scenario %q", scenario))
	}
	return request{RunID: fmt.Sprintf("run-%d", seed), ScenarioID: scenario, Devices: []telemetry.DeviceTelemetry{base}}
}

func post(client *http.Client, endpoint string, payload request, logger *slog.Logger) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		res, requestErr := client.Post(endpoint, "application/json", bytes.NewReader(body))
		if requestErr == nil && res.StatusCode >= 200 && res.StatusCode < 300 {
			res.Body.Close()
			return nil
		}
		if res != nil {
			res.Body.Close()
		}
		last = requestErr
		if last == nil {
			last = fmt.Errorf("API returned non-success status")
		}
		logger.Warn("telemetry post retry", "event", "telemetry.post.retry", "attempt", attempt+1, "error", last)
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	return last
}

func main() {
	scenario := flag.String("scenario", "healthy-baseline", "scenario name")
	seed := flag.Int64("seed", 1, "deterministic seed")
	endpoint := flag.String("endpoint", "http://localhost:8080/api/v1/telemetry", "telemetry endpoint")
	flag.Parse()
	logger := logging.New(logging.Options{Service: "vyomm-simulator", Mode: "trial"})
	payload := Generate(*scenario, *seed, time.Now().UTC())
	if err := post(http.DefaultClient, *endpoint, payload, logger); err != nil {
		logger.Error("telemetry post failed", "event", "telemetry.post.failed", "error", err)
		os.Exit(1)
	}
}
