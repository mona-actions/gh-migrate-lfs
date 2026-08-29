package export

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mona-actions/gh-migrate-lfs/internal/api"
	"github.com/pterm/pterm"
)

type Config struct {
	Organization string
	Token        string
	Hostname     string
	Depth        int
	OutputFile   string
}

type repositoryInfo struct {
	Name              string
	GitAttributesPath string
	CloneURL          string
}

func Run(ctx context.Context, cfg Config) error {
	start := time.Now()
	if cfg.Organization == "" || cfg.Organization == "." || cfg.Organization == ".." || strings.ContainsAny(cfg.Organization, "/\\") {
		return errors.New("organization must be one path segment")
	}
	if cfg.Depth < 1 {
		cfg.Depth = 1
	}
	if cfg.OutputFile == "" {
		cfg.OutputFile = cfg.Organization + "_lfs.csv"
	}

	pterm.Info.Printf("Fetching repository list for %s...\n", cfg.Organization)
	repositories, err := api.ListRepositories(ctx, cfg.Organization, cfg.Token, cfg.Hostname)
	if err != nil {
		return fmt.Errorf("fetch repositories: %w", err)
	}
	pterm.Info.Printf("Found %d repositories\n", len(repositories))

	var lfsRepositories []repositoryInfo
	var repositoryErrors []error
	for _, repository := range repositories {
		pterm.Info.Printf("Searching repository contents: '%s'...\n", repository)
		hasLFS, path, err := api.FindLFSAttributes(ctx, cfg.Organization, repository, cfg.Token, cfg.Depth, cfg.Hostname)
		if err != nil {
			repositoryErrors = append(repositoryErrors, fmt.Errorf("%s: %w", repository, err))
			continue
		}
		if !hasLFS {
			continue
		}

		cloneURL, err := api.RepositoryCloneURL(cfg.Hostname, cfg.Organization, repository)
		if err != nil {
			return fmt.Errorf("build clone URL for %s: %w", repository, err)
		}
		lfsRepositories = append(lfsRepositories, repositoryInfo{Name: repository, GitAttributesPath: path, CloneURL: cloneURL})
		pterm.Success.Printf("LFS filter matched for repository '%s' (path: %s)\n", repository, path)
	}

	if err := writeToCSV(cfg.OutputFile, lfsRepositories); err != nil {
		return fmt.Errorf("write CSV file: %w", err)
	}
	printSummary(start, cfg, len(repositories), len(repositoryErrors), len(lfsRepositories))
	if len(repositoryErrors) > 0 {
		return fmt.Errorf("failed to inspect %d repositories: %w", len(repositoryErrors), errors.Join(repositoryErrors...))
	}
	return nil
}

func printSummary(start time.Time, cfg Config, total, failed, found int) {
	fmt.Printf("\nExport Summary:\n")
	fmt.Printf("Total repositories found: %d\n", total)
	fmt.Printf("Successfully processed: %d repositories\n", total-failed)
	fmt.Printf("Failed to process: %d repositories\n", failed)
	fmt.Printf("Maximum search depth: %d\n", cfg.Depth)
	fmt.Printf("Repositories with LFS: %d\n", found)
	fmt.Printf("Output file: %s\n", cfg.OutputFile)
	fmt.Printf("Total time: %v\n", time.Since(start).Round(time.Second))
}

func writeToCSV(filename string, repositories []repositoryInfo) error {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("set output file permissions: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"Repository", "GitAttributesPaths", "CloneURL"}); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	for _, repository := range repositories {
		if err := writer.Write([]string{repository.Name, repository.GitAttributesPath, repository.CloneURL}); err != nil {
			return fmt.Errorf("write repository data: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush CSV data: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync CSV file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close CSV file: %w", err)
	}
	closed = true
	return nil
}
