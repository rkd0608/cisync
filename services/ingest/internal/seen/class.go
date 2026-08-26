package seen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// volatileKeys are transport-envelope fields that vary between deliveries
// carrying IDENTICAL content (redelivery metadata). They are stripped at any
// depth before hashing so near-replays hash equal. Conservative by design:
// only keys that never carry domain semantics are listed — a missed key at
// worst misses a replay, a wrongly-stripped one would hide real changes.
var volatileKeys = map[string]struct{}{
	"request_id":   {},
	"delivery":     {},
	"delivery_id":  {},
	"received_at":  {},
	"uploaded_at":  {},
	"forwarded_at": {},
}

// ClassHash derives the delivery content-class key:
// sha256(source|repo|event_kind|normalized-significant-payload-fields).
// The payload is canonicalized (recursive key sort, whitespace-insensitive,
// volatile fields dropped) so byte-different-but-semantically-equal payloads
// collide into the same class.
//
// WHY best-effort normalization: JSON number re-decoding can merge values
// that differ only beyond float64 precision; such collisions fail OPEN — the
// delivery is accepted, flagged duplicate_suspect, forwarded record-only and
// alerted on, never rejected.
func ClassHash(source, repo, eventKind string, rawPayload []byte) string {
	canonical := canonicalize(rawPayload)
	sum := sha256.Sum256([]byte(source + "|" + repo + "|" + eventKind + "|" + canonical))
	return hex.EncodeToString(sum[:])
}

// canonicalize renders raw JSON as sorted-key compact JSON with volatile
// keys removed recursively. Non-object/array leaves pass through unchanged;
// undecodable input hashes as its raw bytes so malformed traffic still gets
// a stable class (fail-open, EC-005 posture).
func canonicalize(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, err := encodeCanonical(v)
	if err != nil {
		return string(raw)
	}
	return out
}

func encodeCanonical(v any) (string, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			if _, drop := volatileKeys[k]; drop {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := "{"
		for i, k := range keys {
			if i > 0 {
				out += ","
			}
			keyJSON, err := json.Marshal(k)
			if err != nil {
				return "", fmt.Errorf("canonical key: %w", err)
			}
			valJSON, err := encodeCanonical(t[k])
			if err != nil {
				return "", err
			}
			out += string(keyJSON) + ":" + valJSON
		}
		return out + "}", nil
	case []any:
		out := "["
		for i, item := range t {
			if i > 0 {
				out += ","
			}
			itemJSON, err := encodeCanonical(item)
			if err != nil {
				return "", err
			}
			out += itemJSON
		}
		return out + "]", nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("canonical value: %w", err)
		}
		return string(b), nil
	}
}
