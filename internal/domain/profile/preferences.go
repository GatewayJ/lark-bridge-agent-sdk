package profile

import "encoding/json"

func MaxConcurrentRuns(preferences map[string]any) int {
	var value float64
	switch raw := preferences["maxConcurrentRuns"].(type) {
	case int:
		value = float64(raw)
	case int64:
		value = float64(raw)
	case float64:
		value = raw
	case json.Number:
		parsed, err := raw.Float64()
		if err != nil {
			return 10
		}
		value = parsed
	default:
		return 10
	}
	if value < 1 {
		return 10
	}
	if value > 50 {
		return 50
	}
	return int(value)
}
