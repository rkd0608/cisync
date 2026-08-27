package providers

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Preset is one recognized stack the tools image can validate. Detection is
// purely structural (no network, no file contents beyond existence): it must
// stay cheap and deterministic because it decides which REAL checks run.
type Preset struct {
	Name string // node | python | go
}

const (
	presetNode   = "node"
	presetPython = "python"
	presetGo     = "go"

	nodeDepManifest    = "package.json"
	goDepManifest      = "go.mod"
	requirementsFile   = "requirements.txt"
	pyProjectFile      = "pyproject.toml"
	maxPresetWalkDepth = 6
)

// DetectPresets inspects the extracted workspace root and returns every
// matching preset in deterministic execution order (node → python → go).
// Node triggers on package.json alone; Python needs *.py sources AND a
// dependency manifest (requirements.txt/pyproject.toml) per the v0 scope —
// pip without a manifest would be a fabricated install step; Go on go.mod.
func DetectPresets(root string) []Preset {
	if !dirExists(root) {
		return nil
	}
	var presets []Preset
	hasNode := fileExists(filepath.Join(root, nodeDepManifest))
	hasGo := fileExists(filepath.Join(root, goDepManifest))
	python := detectPython(root)
	if hasNode {
		presets = append(presets, Preset{Name: presetNode})
	}
	if python {
		presets = append(presets, Preset{Name: presetPython})
	}
	if hasGo {
		presets = append(presets, Preset{Name: presetGo})
	}
	return presets
}

// detectPython requires at least one .py file (root or nested within a small
// depth bound to keep deep vendored trees from dominating) AND a dependency
// manifest at the workspace root.
func detectPython(root string) bool {
	if !fileExists(filepath.Join(root, requirementsFile)) &&
		!fileExists(filepath.Join(root, pyProjectFile)) {
		return false
	}
	foundPy := false
	stop := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || stop || foundPy {
			if foundPy {
				return fs.SkipAll
			}
			return nil
		}
		if d.IsDir() {
			depth := strings.Count(strings.TrimPrefix(path, root), string(os.PathSeparator))
			if depth >= maxPresetWalkDepth {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".py") {
			foundPy = true
			return fs.SkipAll
		}
		return nil
	})
	return foundPy
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
