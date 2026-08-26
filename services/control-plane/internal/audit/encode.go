package audit

import (
	"encoding/json"
	"fmt"
)

// marshalObject serializes m as a JSON object; nil maps become the empty
// object so jsonb columns always hold objects, never SQL NULL or scalars.
func marshalObject(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte(`{}`), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal jsonb object: %w", err)
	}
	return raw, nil
}
