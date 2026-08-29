package manifest

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var header = []string{"Repository", "GitAttributesPaths", "CloneURL"}

type Repository struct {
	Name     string
	CloneURL string
}

func Load(path string) ([]Repository, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open repository manifest: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	columns, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read manifest header: %w", err)
	}
	if len(columns) > 0 {
		columns[0] = strings.TrimPrefix(columns[0], "\ufeff")
	}
	if len(columns) != len(header) {
		return nil, fmt.Errorf("invalid manifest header: expected %d columns, got %d", len(header), len(columns))
	}
	for index, want := range header {
		if columns[index] != want {
			return nil, fmt.Errorf("invalid manifest header column %d: got %q, want %q", index+1, columns[index], want)
		}
	}

	seen := make(map[string]struct{})
	var repositories []Repository
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return repositories, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read manifest record: %w", err)
		}
		if !validRepositoryName(record[0]) {
			return nil, fmt.Errorf("invalid repository name %q: must be one path segment", record[0])
		}
		if _, duplicate := seen[record[0]]; duplicate {
			continue
		}
		seen[record[0]] = struct{}{}
		repositories = append(repositories, Repository{
			Name:     record[0],
			CloneURL: record[2],
		})
	}
}

func validRepositoryName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\")
}
