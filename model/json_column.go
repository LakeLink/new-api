package model

import (
	"bytes"
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

// scanJSONObject decodes JSON object columns returned by different SQL
// drivers. MySQL and SQLite commonly return []byte, while text-compatible
// PostgreSQL columns may be returned as string.
func scanJSONObject(value any, destination any) error {
	var data []byte
	switch value := value.(type) {
	case nil:
		return nil
	case []byte:
		data = value
	case string:
		data = []byte(value)
	default:
		return fmt.Errorf("unsupported JSON column type %T", value)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return common.Unmarshal(data, destination)
}
