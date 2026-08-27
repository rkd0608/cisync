package providers

import (
	"os"
	"path/filepath"
)

// writePresetScripts materializes the stack-check shell scripts into the
// sandbox's ro-mounted scripts dir. WHY generated-per-job instead of baked
// into images: any base image (tools node image AND golang) gets identical,
// reviewable check semantics, and tests can exercise real script bytes
// without a daemon. Scripts are POSIX sh (busybox ash in alpine).
//
// MARKER PROTOCOL (parsed by checkparse.go):
//
//	[cisync-check] {"tool":"...","verdict":"pass|fail|skip","duration_ms":N}
//	[cisync-tail] <tool>|<single-line detail>
//
// Timestamps have second granularity by design: busybox date lacks %N and the
// workloads run long enough that ±1s is acceptable for evidence display.
func writePresetScripts(dir string) error {
	scripts := map[string]string{presetNode: nodePresetScript, presetPython: pythonPresetScript, presetGo: goPresetScript}
	for name, body := range scripts {
		path := filepath.Join(dir, name+".sh")
		if err := os.WriteFile(path, []byte(body), 0o555); err != nil {
			return err
		}
	}
	return nil
}

// sharedPOSIXHelpers holds the emit/run/ms helpers every preset reuses.
const sharedPOSIXHelpers = `
ms() { date +%s"000"; }
emit_pass_or_fail() { # tool rc start duration
  if [ "$2" -eq 0 ]; then verdict=pass; else verdict=fail; fi
  printf '[cisync-check] {"tool":"%s","verdict":"%s","duration_ms":%s}\n' "$1" "$verdict" "$4"
}
emit_tail() { # tool output...
  cleaned=$(printf '%b' "$2" | tr -d '\000' | tr '\t\r\n' '   ' | sed 's/  */ /g' | tail -c 240)
  printf '[cisync-tail] %s|%s\n' "$1" "$cleaned"
}
`

const nodePresetScript = "#!/bin/sh\n" +
	"set -u\n" +
	"mkdir -p /scratch/src && cp -r /src/. /scratch/src/ && cd /scratch/src || exit 125\n" +
	sharedPOSIXHelpers +
	"# Setup step is context, NOT evidence: failures never fabricate passes.\n" +
	"if [ -f package-lock.json ]; then out=$(npm ci --omit=dev --ignore-scripts 2>&1 || true); echo \"$out\" | tail -c 400; fi\n" +
	"# eslint: runs only with a config present (skip-with-reason otherwise).\n" +
	"if ls .eslintrc* >/dev/null 2>&1 || [ -f eslint.config.js ] || [ -f eslint.config.mjs ] || [ -f eslint.config.cjs ]; then\n" +
	"  if command -v eslint >/dev/null 2>&1; then\n" +
	"    start=$(ms); out=$(eslint . 2>&1); rc=$?; dur=$(($(ms)-start))\n" +
	"    emit_pass_or_fail eslint \"$rc\" \"$start\" \"$dur\"; emit_tail eslint \"$out\"\n" +
	"  else\n" +
	`    printf '[cisync-check] {"tool":"eslint","verdict":"skip","duration_ms":0}\n'; printf '[cisync-tail] eslint|tool_unavailable_offline\n'` + "\n" +
	"  fi\n" +
	"else\n" +
	`  printf '[cisync-check] {"tool":"eslint","verdict":"skip","duration_ms":0}\n'; printf '[cisync-tail] eslint|no_eslint_config\n'` + "\n" +
	"fi\n" +
	"# tsc --noEmit: typecheck only, no emit; needs tsconfig.json to be real.\n" +
	"if [ -f tsconfig.json ]; then\n" +
	"  if command -v tsc >/dev/null 2>&1; then\n" +
	"    start=$(ms); out=$(tsc --noEmit 2>&1); rc=$?; dur=$(($(ms)-start))\n" +
	"    emit_pass_or_fail tsc \"$rc\" \"$start\" \"$dur\"; emit_tail tsc \"$out\"\n" +
	"  else\n" +
	`    printf '[cisync-check] {"tool":"tsc","verdict":"skip","duration_ms":0}\n'; printf '[cisync-tail] tsc|tool_unavailable_offline\n'` + "\n" +
	"  fi\n" +
	"else\n" +
	`  printf '[cisync-check] {"tool":"tsc","verdict":"skip","duration_ms":0}\n'; printf '[cisync-tail] tsc|no_tsconfig\n'` + "\n" +
	"fi\n"

const pythonPresetScript = "#!/bin/sh\n" +
	"set -u\n" +
	"mkdir -p /scratch/src && cp -r /src/. /scratch/src/ && cd /scratch/src || exit 125\n" +
	sharedPOSIXHelpers +
	"# pip install is setup-only context (offline sandbox ⇒ best-effort).\n" +
	"if [ -f requirements.txt ]; then out=$(pip install -r requirements.txt --quiet 2>&1 || true); echo \"$out\" | tail -c 400; fi\n" +
	"# Bytecode sanity over a WRITABLE copy: the ro workspace mount would reject __pycache__.\n" +
	"start=$(ms); out=$(python3 -m compileall -q . 2>&1); rc=$?; dur=$(($(ms)-start))\n" +
	"emit_pass_or_fail compileall \"$rc\" \"$start\" \"$dur\"; emit_tail compileall \"$out\"\n" +
	`printf '[cisync-check] {"tool":"ruff","verdict":"skip","duration_ms":0}\n'` + "\n" +
	`printf '[cisync-tail] ruff|not_shipped_v0: flagged for dependency approval\n'` + "\n"

const goPresetScript = "#!/bin/sh\n" +
	"set -u\n" +
	"mkdir -p /scratch/src /tmp/gocache /tmp/gopath && cp -r /src/. /scratch/src/ && cd /scratch/src || exit 125\n" +
	"export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath GOPROXY=off GOFLAGS=-mod=mod HOME=/tmp\n" +
	sharedPOSIXHelpers +
	"# GOPROXY=off keeps vet honest offline: modules must resolve from stdlib or vendor/.\n" +
	"if [ -d vendor ]; then export GOFLAGS=-mod=vendor; fi\n" +
	"start=$(ms); out=$(go vet ./... 2>&1); rc=$?; dur=$(($(ms)-start))\n" +
	"emit_pass_or_fail go_vet \"$rc\" \"$start\" \"$dur\"; emit_tail go_vet \"$out\"\n"
