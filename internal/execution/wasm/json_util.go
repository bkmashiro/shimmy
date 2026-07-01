package wasm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseJSONResponse unmarshals a JSON object from the given string.
func parseJSONResponse(s string) (map[string]any, error) {
	s = strings.TrimSpace(s)
	var result map[string]any
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w (raw: %.200s)", err, s)
	}
	return result, nil
}
