package planner

// JobKindByName maps every catalog job name to its evidence-vocabulary kind
// (the `kind` field of the job specs). Admission-tier jobs produce no
// evidence records: their kinds are outside the required-evidence vocabulary.
var jobKindByName = buildJobKindIndex()

func buildJobKindIndex() map[string]string {
	index := make(map[string]string)
	catalogs := [][]JobSpec{
		tier0Jobs, tier1SelectedJobs, tier1FullJobs,
		tier2SelectedBase, tier2FullBase,
		{tier2PaymentJob, tier2IdempotencyJob},
		tier3Jobs, tier4Jobs,
	}
	for _, cat := range catalogs {
		for _, j := range cat {
			index[j.Name] = j.Kind
		}
	}
	return index
}

// EvidenceKindForJob returns the evidence vocabulary kind for one job name;
// gate jobs map to their own non-evidence kind.
func EvidenceKindForJob(jobName string) string {
	if kind, ok := jobKindByName[jobName]; ok {
		return kind
	}
	return "hermetic_build"
}

// EvidenceKindsForJobs returns the deduplicated evidence kinds a tier's job
// list produces. Gate jobs (secret_scan etc.) contribute nothing because
// their kinds are not part of the required-evidence vocabulary.
func EvidenceKindsForJobs(jobNames []string) []string {
	var out []string
	for _, name := range jobNames {
		kind, ok := jobKindByName[name]
		if !ok {
			continue
		}
		dup := false
		for _, k := range out {
			if k == kind {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, kind)
		}
	}
	return out
}

// RequiredKindsCoveredByTier intersects the evidence kinds a tier produces
// with the plan's required kinds — the kinds a completed run of this tier may
// satisfy (one run per producing job; I-03 keeps one accepted record each).
func RequiredKindsCoveredByTier(jobNames, requiredKinds []string) []string {
	produced := EvidenceKindsForJobs(jobNames)
	var out []string
	for _, produced1 := range produced {
		for _, req := range requiredKinds {
			if produced1 == req {
				out = append(out, produced1)
				break
			}
		}
	}
	return out
}
