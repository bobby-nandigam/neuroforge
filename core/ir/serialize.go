package ir

import (
	"encoding/json"
	"fmt"
)

// Marshal serializes a Module to indented JSON. MIR is JSON-native so the same
// artifact round-trips through the CLI, control-plane API, and dashboard.
func Marshal(m *Module) ([]byte, error) {
	if m.SchemaVersion == "" {
		m.SchemaVersion = SchemaVersion
	}
	return json.MarshalIndent(m, "", "  ")
}

// Unmarshal parses a serialized MIR Module and verifies the schema version is
// one this build understands. We accept the current major.minor line.
func Unmarshal(data []byte) (*Module, error) {
	var m Module
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("mir: decode: %w", err)
	}
	if m.SchemaVersion == "" {
		return nil, fmt.Errorf("mir: missing schema_version")
	}
	if !schemaCompatible(m.SchemaVersion) {
		return nil, fmt.Errorf("mir: incompatible schema_version %q (this build expects %q)", m.SchemaVersion, SchemaVersion)
	}
	return &m, nil
}

// schemaCompatible reports whether a serialized module's schema version can be
// loaded. We require an exact match of the major version and a minor version no
// greater than ours.
func schemaCompatible(v string) bool {
	var vmaj, vmin, cur0, cur1 int
	if _, err := fmt.Sscanf(v, "%d.%d", &vmaj, &vmin); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(SchemaVersion, "%d.%d", &cur0, &cur1); err != nil {
		return false
	}
	return vmaj == cur0 && vmin <= cur1
}
