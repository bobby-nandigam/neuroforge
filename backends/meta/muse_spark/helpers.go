package muse_spark

import (
	"encoding/json"
	"fmt"

	"neuroforge/backends"
)

func asText(v backends.Value) string {
	if s, ok := v.Data.(string); ok {
		return s
	}
	if v.Ref != "" {
		return v.Ref
	}
	return fmt.Sprintf("%v", v.Data)
}

func asJSONString(v backends.Value) string {
	if v.Data == nil {
		return "{}"
	}
	b, err := json.MarshalIndent(v.Data, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v.Data)
	}
	return string(b)
}
