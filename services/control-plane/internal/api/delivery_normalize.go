package api

import (
	"strings"
)

// NormalizedKind is the §3.1 mapping target the ledger effects key on.
type NormalizedKind string

// Normalized kinds (plan §3.1 table).
const (
	KindPROpened       NormalizedKind = "pr.opened"
	KindPRSynchronize  NormalizedKind = "pr.synchronize"
	KindPRClosed       NormalizedKind = "pr.closed"
	KindPushBaseAdv    NormalizedKind = "push.base_advanced"
	KindPushBranch     NormalizedKind = "push.branch"
	KindInstallCreated NormalizedKind = "installation.created"
	KindInstallDeleted NormalizedKind = "installation.deleted"
	KindInstallPerms   NormalizedKind = "installation.permissions_changed"
	KindCheckRereq     NormalizedKind = "check_run.rerequested"
)

// prEventView carries every pull_request field the effects read.
type prEventView struct {
	Number  int
	HeadSHA string
	BaseSHA string
	BaseRef string
	DiffURL string
	Title   string
	Sender  string
	Merged  bool
}

// pushEventView carries the push fields the base-advance cascade reads.
type pushEventView struct {
	Ref     string
	Branch  string
	OldSHA  string
	NewSHA  string
	Deleted bool
}

// installationEventView carries the installation lifecycle fields (G10).
type installationEventView struct {
	Account string
	Repos   []string
}

// deliveryView bundles everything the effect builders need from one delivery.
type deliveryView struct {
	Kind        NormalizedKind
	Raw         string // unknown.<event>[.<action>] park label
	Repo        string
	TrackedBase bool
	PR          prEventView
	Push        pushEventView
	Install     installationEventView
}

// normalizeDelivery maps (event_kind, payload) → normalized kind + typed
// view. event_kind arrives as X-GitHub-Event[.action] (ingest composes the
// action); when the alias route forwards a bare event name the action is
// recovered from the payload so both paths normalize identically.
func normalizeDelivery(eventKind, repo string, payload map[string]any, trackedBases []string) deliveryView {
	eventName, action := splitEventKind(eventKind)
	if action == "" {
		action = payloadString(payload, "action")
	}
	view := deliveryView{Repo: repo}
	switch eventName {
	case "pull_request":
		view.PR = prView(payload)
		switch action {
		case "opened", "reopened":
			view.Kind = KindPROpened
		case "synchronize":
			view.Kind = KindPRSynchronize
		case "closed":
			view.Kind = KindPRClosed
			view.PR.Merged = payloadBoolPath(payload, "pull_request", "merged")
		default:
			view.Kind = unknownKind(eventName, action, &view.Raw)
		}
	case "push":
		view.Push = pushView(payload)
		view.TrackedBase = trackedBranch(trackedBases, view.Push.Branch)
		if !view.Push.Deleted && view.TrackedBase && len(view.Push.NewSHA) == 40 && len(view.Push.OldSHA) == 40 {
			view.Kind = KindPushBaseAdv
		} else {
			view.Kind = KindPushBranch
		}
	case "installation":
		view.Install = installationView(payload)
		switch action {
		case "created":
			view.Kind = KindInstallCreated
		case "deleted":
			view.Kind = KindInstallDeleted
		case "new_permissions_accepted":
			view.Kind = KindInstallPerms
		default:
			view.Kind = unknownKind(eventName, action, &view.Raw)
		}
	case "check_run":
		if action == "rerequested" {
			view.Kind = KindCheckRereq
		} else {
			view.Kind = unknownKind(eventName, action, &view.Raw)
		}
	default:
		view.Kind = unknownKind(eventName, action, &view.Raw)
	}
	return view
}

func splitEventKind(eventKind string) (name, action string) {
	parts := strings.SplitN(eventKind, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return eventKind, ""
}

// unknownKind parks anything outside the consumed set as never-4xx label.
func unknownKind(event, action string, raw *string) NormalizedKind {
	label := "unknown." + event
	if action != "" {
		label += "." + action
	}
	*raw = label
	return ""
}

func prView(payload map[string]any) prEventView {
	pr := prEventView{}
	prMap, _ := payload["pull_request"].(map[string]any)
	if prMap == nil {
		return pr
	}
	pr.Number = int(payloadNumber(prMap, "number"))
	pr.HeadSHA = payloadStringPath(prMap, "head", "sha")
	pr.BaseSHA = payloadStringPath(prMap, "base", "sha")
	pr.BaseRef = payloadStringPath(prMap, "base", "ref")
	pr.DiffURL = payloadString(prMap, "diff_url")
	pr.Title = payloadString(prMap, "title")
	pr.Merged, _ = prMap["merged"].(bool)
	pr.Sender = payloadStringPath(payload, "sender", "login")
	return pr
}

func pushView(payload map[string]any) pushEventView {
	push := pushEventView{
		Ref:    payloadString(payload, "ref"),
		OldSHA: payloadString(payload, "before"),
		NewSHA: payloadString(payload, "after"),
		// Branch deletions arrive as after=000…0; they never advance a base.
		Deleted: payloadString(payload, "after") == strings.Repeat("0", 40),
	}
	push.Branch = strings.TrimPrefix(push.Ref, "refs/heads/")
	return push
}

func installationView(payload map[string]any) installationEventView {
	inst := installationEventView{
		Account: payloadStringPath(payload, "installation", "account", "login"),
	}
	if repos, ok := payload["repositories"].([]any); ok {
		for _, r := range repos {
			repoMap, _ := r.(map[string]any)
			if repoMap == nil {
				continue
			}
			name := payloadString(repoMap, "full_name")
			if name == "" {
				name = payloadString(repoMap, "name")
			}
			if name != "" {
				inst.Repos = append(inst.Repos, name)
			}
		}
	}
	if len(inst.Repos) == 0 {
		if repo := payloadStringPath(payload, "repository", "full_name"); repo != "" {
			inst.Repos = append(inst.Repos, repo)
		}
	}
	return inst
}

// trackedBranch matches a branch against CISYNC_CTRL_TRACKED_BASE_BRANCHES
// (exact names only in v0.2; comma globs deferred per plan §5.1 note).
func trackedBranch(bases []string, branch string) bool {
	for _, b := range bases {
		if b == branch {
			return true
		}
	}
	return false
}

func payloadString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func payloadBoolPath(m map[string]any, path ...string) bool {
	cur := m
	for i, p := range path {
		next, ok := cur[p]
		if !ok {
			return false
		}
		if i == len(path)-1 {
			b, _ := next.(bool)
			return b
		}
		cur, ok = next.(map[string]any)
		if !ok {
			return false
		}
	}
	return false
}

func payloadStringPath(m map[string]any, path ...string) string {
	cur := m
	for i, p := range path {
		next, ok := cur[p]
		if !ok {
			return ""
		}
		if i == len(path)-1 {
			s, _ := next.(string)
			return s
		}
		cur, ok = next.(map[string]any)
		if !ok {
			return ""
		}
	}
	return ""
}

func payloadNumber(m map[string]any, key string) float64 {
	v, _ := m[key].(float64)
	return v
}
