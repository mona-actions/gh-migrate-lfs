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
	"github.com/mona-actions/gh-migrate-lfs/internal/output"
)

type Config struct {
	Organization string
	Token        string
	Hostname     string
	Depth        int
	OutputFile   string
	Output       *output.Renderer
}

type repositoryInfo struct {
	Name              string
	GitAttributesPath string
	CloneURL          string
}

type Summary struct {
	Complete              bool   `json:"complete"`
	RepositoriesInspected int    `json:"repositories_inspected"`
	RepositoriesProcessed int    `json:"repositories_processed"`
	RepositoriesFailed    int    `json:"repositories_failed"`
	RepositoriesUsingLFS  int    `json:"repositories_using_lfs"`
	MaximumSearchDepth    int    `json:"maximum_search_depth"`
	Duration              string `json:"duration"`
	Manifest              string `json:"manifest"`
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

	cfg.Output.Line("Finding Git LFS repositories in %s", cfg.Organization)
	cfg.Output.Status("Fetching repository list")
	repositories, err := api.ListRepositories(ctx, cfg.Organization, cfg.Token, cfg.Hostname, cfg.Output)
	if err != nil {
		return fmt.Errorf("fetch repositories: %w", err)
	}

	var lfsRepositories []repositoryInfo
	var repositoryErrors []error
	for index, repository := range repositories {
		cfg.Output.Status("Searching repositories %d/%d | %d use LFS", index, len(repositories), len(lfsRepositories))
		hasLFS, path, err := api.FindLFSAttributes(ctx, cfg.Organization, repository, cfg.Token, cfg.Depth, cfg.Hostname, cfg.Output)
		if err != nil {
			repositoryErrors = append(repositoryErrors, fmt.Errorf("%s: %w", repository, err))
			cfg.Output.Line("Failed %s: %v", repository, err)
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
		cfg.Output.Line("Found %s  %s", repository, path)
	}
	cfg.Output.FinishStatus("Searched %d/%d repositories | %d use LFS", len(repositories), len(repositories), len(lfsRepositories))

	if err := writeToCSV(cfg.OutputFile, lfsRepositories); err != nil {
		return fmt.Errorf("write CSV file: %w", err)
	}
	summary := Summary{
		Complete:              len(repositoryErrors) == 0,
		RepositoriesInspected: len(repositories),
		RepositoriesProcessed: len(repositories) - len(repositoryErrors),
		RepositoriesFailed:    len(repositoryErrors),
		RepositoriesUsingLFS:  len(lfsRepositories),
		MaximumSearchDepth:    cfg.Depth,
		Duration:              time.Since(start).Round(time.Second).String(),
		Manifest:              cfg.OutputFile,
	}
	cfg.Output.Record("export", summary)
	printSummary(cfg.Output, summary)
	if len(repositoryErrors) > 0 {
		return errors.Join(
			fmt.Errorf("failed to inspect %d repositories: %w", len(repositoryErrors), errors.Join(repositoryErrors...)),
			cfg.Output.Err(),
		)
	}
	return cfg.Output.Err()
}

func printSummary(renderer *output.Renderer, summary Summary) {
	status := "complete"
	if !summary.Complete {
		status = "incomplete"
	}
	renderer.Result("\nExport %s\n\n", status)
	renderer.Result("Repositories inspected:  %d\n", summary.RepositoriesInspected)
	renderer.Result("Successfully processed:  %d\n", summary.RepositoriesProcessed)
	renderer.Result("Failed to inspect:       %d\n", summary.RepositoriesFailed)
	renderer.Result("Repositories using LFS: %d\n", summary.RepositoriesUsingLFS)
	renderer.Result("Maximum search depth:    %d\n", summary.MaximumSearchDepth)
	renderer.Result("Duration:                %s\n", summary.Duration)
	renderer.Result("Manifest:                %s\n", summary.Manifest)
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
