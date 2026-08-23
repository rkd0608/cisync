package evidence

// AcceptedRecord is the minimal projection of an accepted evidence record
// needed for sufficiency computation (D8).
type AcceptedRecord struct {
	Kind    string
	Verdict string
	Status  string
}

// Accepted statuses relevant to sufficiency.
const (
	StatusAccepted = "accepted"
)

// Sufficiency computes the evidence dossier completeness percentage per D8:
// |accepted ∩ required_kinds| / |required_kinds|, counting each distinct
// required kind at most once. Only records with verdict=pass and status
// accepted contribute. An empty requirement set is vacuously satisfied (1.0).
func Sufficiency(requiredKinds []string, accepted []AcceptedRecord) float64 {
	required := dedupeNonEmpty(requiredKinds)
	if len(required) == 0 {
		return 1.0
	}
	satisfied := make(map[string]struct{}, len(required))
	for _, rec := range accepted {
		if rec.Verdict != VerdictPass || rec.Status != StatusAccepted {
			continue
		}
		for _, k := range required {
			if k == rec.Kind {
				satisfied[k] = struct{}{}
			}
		}
	}
	return float64(len(satisfied)) / float64(len(required))
}

func dedupeNonEmpty(kinds []string) []string {
	seen := make(map[string]struct{}, len(kinds))
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}
