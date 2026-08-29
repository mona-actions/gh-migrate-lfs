package pull

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mona-actions/gh-migrate-lfs/internal/manifest"
	"github.com/mona-actions/gh-migrate-lfs/internal/output"
	"github.com/mona-actions/gh-migrate-lfs/internal/worker"
)

type Config struct {
	InputFile string
	Token     string
	WorkDir   string
	Workers   int
	Output    *output.Renderer
}

type Summary struct {
	Complete              bool   `json:"complete"`
	Repositories          int    `json:"repositories"`
	RepositoriesSucceeded int    `json:"repositories_succeeded"`
	RepositoriesFailed    int    `json:"repositories_failed"`
	Duration              string `json:"duration"`
	OutputDirectory       string `json:"output_directory"`
}

func Run(ctx context.Context, cfg Config) error {
	return run(ctx, cfg, pullRepository)
}

func run(ctx context.Context, cfg Config, puller func(context.Context, string, string, string, []string) error) error {
	repositories, err := manifest.Load(cfg.InputFile)
	if err != nil {
		return err
	}

	jobChannel := make(chan manifest.Repository, len(repositories))
	for _, repository := range repositories {
		jobChannel <- repository
	}
	close(jobChannel)

	workers := max(cfg.Workers, 1)
	cfg.Output.Line("Pulling Git LFS objects with %d workers", workers)
	stats := worker.NewStats()
	var completed atomic.Int32
	var active atomic.Int32
	err = worker.Run(ctx, jobChannel, workers, stats, func(repository manifest.Repository) error {
		active.Add(1)
		cfg.Output.Status("Repositories %d/%d complete | %d active", completed.Load(), len(repositories), active.Load())
		startedAt := time.Now()
		gitEnv, err := gitAuthEnvironment(repository.CloneURL, cfg.Token)
		if err == nil {
			err = puller(ctx, repository.Name, repository.CloneURL, cfg.WorkDir, gitEnv)
		}
		active.Add(-1)
		completed.Add(1)
		duration := time.Since(startedAt).Round(time.Second)
		if err != nil {
			cfg.Output.Line("Failed %s  %v", repository.Name, err)
		} else {
			cfg.Output.Line("Complete %s  %v", repository.Name, duration)
		}
		cfg.Output.Status("Repositories %d/%d complete | %d active", completed.Load(), len(repositories), active.Load())
		if err != nil {
			return fmt.Errorf("%s: %w", repository.Name, err)
		}
		return nil
	})
	cfg.Output.FinishStatus("Repositories %d/%d complete", completed.Load(), len(repositories))
	workerSummary := stats.Summary()
	status := "complete"
	if err != nil {
		status = "incomplete"
	}
	summary := Summary{
		Complete:              err == nil,
		Repositories:          len(repositories),
		RepositoriesSucceeded: workerSummary.Succeeded,
		RepositoriesFailed:    workerSummary.Failed,
		Duration:              workerSummary.Duration.Round(time.Second).String(),
		OutputDirectory:       cfg.WorkDir,
	}
	cfg.Output.Record("pull", summary)
	cfg.Output.Result("\nPull %s\n\n", status)
	cfg.Output.Result("Repositories succeeded: %d\n", summary.RepositoriesSucceeded)
	cfg.Output.Result("Repositories failed:    %d\n", summary.RepositoriesFailed)
	cfg.Output.Result("Duration:               %s\n", summary.Duration)
	cfg.Output.Result("Output:                 %s\n", summary.OutputDirectory)
	return errors.Join(err, cfg.Output.Err())
}

func pullRepository(ctx context.Context, repoName, cloneURL, workDir string, gitEnv []string) error {
	repoPath := filepath.Join(workDir, repoName)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create working directory: %w", err)
	}

	exists, err := directoryExists(repoPath)
	if err != nil {
		return err
	}
	if exists {
		if err := runGit(ctx, repoPath, gitEnv, "remote", "set-url", "origin", cloneURL); err != nil {
			return fmt.Errorf("set clean origin URL: %w", err)
		}
		if err := runGit(ctx, repoPath, gitEnv, "fetch", "--prune", "origin", "+refs/*:refs/*"); err != nil {
			return fmt.Errorf("update mirror: %w", err)
		}
	} else {
		if err := runGit(ctx, workDir, gitEnv, "clone", "--mirror", "--bare", cloneURL, repoName); err != nil {
			return fmt.Errorf("clone mirror: %w", err)
		}
	}

	if err := runGit(ctx, repoPath, gitEnv, "lfs", "fetch", "--all"); err != nil {
		return fmt.Errorf("fetch LFS objects: %w", err)
	}
	return nil
}

func gitAuthEnvironment(rawURL, token string) ([]string, error) {
	if token == "" {
		return nil, errors.New("GitHub token is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid clone URL %q", rawURL)
	}
	if parsed.User != nil {
		return nil, errors.New("clone URL must not contain credentials")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" {
		return nil, errors.New("clone URL must use HTTPS")
	}

	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	scope := fmt.Sprintf("http.%s://%s/.extraheader", parsed.Scheme, parsed.Host)
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_TRACE_REDACT=1",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0="+scope,
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+auth,
	), nil
}

func runGit(ctx context.Context, dir string, env []string, args ...string) error {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = env
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, message)
	}
	return nil
}

func directoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect repository path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("repository path must not be a symbolic link: %s", path)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("repository path is not a directory: %s", path)
	}
	return true, nil
}
