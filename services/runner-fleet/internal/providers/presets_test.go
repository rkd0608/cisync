package providers

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Preset detection is the boundary between the materialized repo snapshot and
// the executed stack checks. Wrong detection either fabricates coverage
// (running nothing yet reporting something) or hides stacks entirely — hence
// an explicit table over the exact repo layouts CISync will see.
func TestDetectPresets(t *testing.T) {
	tests := []struct {
		name     string
		files    []string // relative paths written into the sandbox root
		expected []string // preset names in deterministic execution order
	}{
		{"empty repo", nil, nil},
		{"node via package.json", []string{"package.json"}, []string{"node"}},
		{
			"python requires dep manifest",
			[]string{"app.py"},
			nil,
		},
		{
			"python via requirements.txt",
			[]string{"main.py", "requirements.txt"},
			[]string{"python"},
		},
		{
			"python via pyproject.toml",
			[]string{"pkg/mod.py", "pyproject.toml"},
			[]string{"python"},
		},
		{"go via go.mod", []string{"go.mod"}, []string{"go"}},
		{
			"multi-stack orders node-python-go",
			[]string{"package.json", "server.py", "requirements.txt", "go.mod"},
			[]string{"node", "python", "go"},
		},
		{
			"nested python sources count",
			[]string{"src/deep/util.py", "requirements.txt"},
			[]string{"python"},
		},
		{"unrelated tree", []string{"README.md", "Makefile"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, rel := range tc.files {
				p := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := DetectPresets(root)
			names := make([]string, 0, len(got))
			for _, p := range got {
				names = append(names, p.Name)
			}
			if len(tc.expected) == 0 && len(names) == 0 {
				return
			}
			if !reflect.DeepEqual(names, tc.expected) {
				t.Fatalf("presets = %v, want %v", names, tc.expected)
			}
		})
	}
}

// A missing root is not a crash: an unusable bundle must degrade to "no
// presets" and let the caller report the honest skip-with-reason outcome.
func TestDetectPresetsMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	if got := DetectPresets(root); len(got) != 0 {
		t.Fatalf("missing root must detect zero presets, got %v", got)
	}
}
