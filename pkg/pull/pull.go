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

	"github.com/mona-actions/gh-migrate-lfs/internal/manifest"
	"github.com/mona-actions/gh-migrate-lfs/internal/worker"
	"github.com/pterm/pterm"
)

type Config struct {
	InputFile string
	Token     string
	WorkDir   string
	Workers   int
}

func Run(ctx context.Context, cfg Config) error {
	repositories, err := manifest.Load(cfg.InputFile)
	if err != nil {
		return err
	}

	jobChannel := make(chan manifest.Repository, len(repositories))
	for _, repository := range repositories {
		jobChannel <- repository
	}
	close(jobChannel)

	stats := worker.NewStats()
	err = worker.Run(ctx, jobChannel, max(cfg.Workers, 1), stats, func(repository manifest.Repository) error {
		gitEnv, err := gitAuthEnvironment(repository.CloneURL, cfg.Token)
		if err != nil {
			return fmt.Errorf("%s: %w", repository.Name, err)
		}
		return pullRepository(ctx, repository.Name, repository.CloneURL, cfg.WorkDir, gitEnv)
	})
	stats.PrintSummary(cfg.WorkDir)
	if err != nil {
		return err
	}

	fmt.Println("\nPull completed successfully!")
	return nil
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
		pterm.Info.Printf("Repository exists '%s', proceeding with update\n", repoName)
		if err := runGit(ctx, repoPath, gitEnv, "remote", "set-url", "origin", cloneURL); err != nil {
			return fmt.Errorf("set clean origin URL: %w", err)
		}
		if err := runGit(ctx, repoPath, gitEnv, "fetch", "--prune", "origin", "+refs/*:refs/*"); err != nil {
			return fmt.Errorf("update mirror: %w", err)
		}
	} else {
		pterm.Info.Printf("Cloning repository '%s'...\n", repoName)
		if err := runGit(ctx, workDir, gitEnv, "clone", "--mirror", "--bare", cloneURL, repoName); err != nil {
			return fmt.Errorf("clone mirror: %w", err)
		}
	}

	if err := runGit(ctx, repoPath, gitEnv, "lfs", "fetch", "--all"); err != nil {
		return fmt.Errorf("fetch LFS objects: %w", err)
	}
	pterm.Success.Printf("synchronized: %s\n", repoName)
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
