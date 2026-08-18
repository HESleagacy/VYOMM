-- +goose Up
CREATE TABLE telemetry (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hostname TEXT NOT NULL,
    role TEXT NOT NULL,
    payload TEXT NOT NULL,      -- JSON-encoded telemetry.DeviceTelemetry
    observed_at TEXT NOT NULL,  -- RFC3339 UTC
    run_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL    -- RFC3339 UTC, insertion time
);
CREATE INDEX idx_telemetry_hostname_observed ON telemetry(hostname, observed_at);
CREATE INDEX idx_telemetry_created_at ON telemetry(created_at);

CREATE TABLE incidents (
    id TEXT PRIMARY KEY,
    occurrence_key TEXT NOT NULL,
    occurrence_sequence INTEGER NOT NULL,
    severity TEXT NOT NULL,
    affected_devices TEXT NOT NULL, -- JSON array
    root_cause TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    confidence REAL NOT NULL,
    predicted_sla_breach_minutes INTEGER NOT NULL,
    recommended_action TEXT NOT NULL
);
CREATE INDEX idx_incidents_occurrence_key ON incidents(occurrence_key);
CREATE INDEX idx_incidents_status ON incidents(status);

CREATE TABLE incident_decisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id TEXT NOT NULL REFERENCES incidents(id),
    status TEXT NOT NULL,
    actor TEXT NOT NULL,
    decided_at TEXT NOT NULL
);
CREATE INDEX idx_decisions_incident_id ON incident_decisions(incident_id);

-- +goose Down
DROP TABLE incident_decisions;
DROP TABLE incidents;
DROP TABLE telemetry;
