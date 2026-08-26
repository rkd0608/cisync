package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cisync.dev/cisync/github-connector/internal/domain"
)

// migrationFile is one discovered *.up.sql migration.
type migrationFile struct {
	version int
	file    string
	sql     string
}

func domainVerb(v string) domain.DecisionVerb { return domain.DecisionVerb(v) }

// migrationFiles reads and sorts the *.up.sql files under dir.
func migrationFiles(dir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("ghconn store: read migrations dir %s: %w", dir, err)
	}
	var ups []migrationFile
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		base := strings.TrimSuffix(name, ".up.sql")
		versionStr := base
		if i := strings.Index(base, "_"); i >= 0 {
			versionStr = base[:i]
		}
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			return nil, fmt.Errorf("ghconn store: bad migration name %s: %w", name, err)
		}
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("ghconn store: read %s: %w", name, err)
		}
		ups = append(ups, migrationFile{version: version, file: name, sql: string(sqlBytes)})
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].version < ups[j].version })
	return ups, nil
}
