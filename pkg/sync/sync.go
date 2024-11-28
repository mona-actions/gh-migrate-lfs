package sync

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mona-actions/gh-migrate-lfs/pkg/common"
	"github.com/spf13/viper"
)

type syncJob struct {
	repoName  string
	workDir   string
	targetOrg string
}

func SyncFromCSV() error {
	// Get configuration from viper
	inputFile := viper.GetString("GHMLFS_FILE")
	workDir := viper.GetString("GHMLFS_WORK_DIR")
	targetOrg := viper.GetString("GHMLFS_TARGET_ORGANIZATION")
	token := viper.GetString("GHMLFS_TARGET_TOKEN")
	maxWorkers := viper.GetInt("GHMLFS_WORKERS")

	// Read CSV file
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("error opening input file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// Skip header
	if _, err := reader.Read(); err != nil {
		return fmt.Errorf("error reading CSV header: %w", err)
	}

	// Create jobs channel and track unique repositories
	jobs := make(chan syncJob)
	seen := make(map[string]bool)

	// Start goroutine to send jobs
	go func() {
		defer close(jobs)
		for {
			record, err := reader.Read()
			if err != nil {
				break
			}
			repoName := record[0]
			if seen[repoName] {
				continue
			}
			seen[repoName] = true

			jobs <- syncJob{
				repoName:  repoName,
				workDir:   workDir,
				targetOrg: targetOrg,
			}
		}
	}()

	// Create and run worker pool
	stats := common.NewProcessStats()
	err = common.WorkerPool(jobs, maxWorkers, stats, func(job syncJob) error {
		// Pass token here instead of in the job struct for better security
		return SyncLFSContent(job.repoName, job.workDir, job.targetOrg, token)
	})

	// Print summary
	stats.PrintSummary(workDir)

	if err != nil {
		return err
	}

	fmt.Println("\n✅ Sync completed successfully!")
	return nil
}

func SyncLFSContent(repoName, workDir, targetOrg, token string) error {
	repoPath := filepath.Join(workDir, repoName)

	// Configure GitHub authentication
	authCmd := exec.Command("sh", "-c", fmt.Sprintf("echo %q | gh auth login --with-token", token))
	if output, err := authCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("❌ Failed to configure GitHub authentication: %s, %w", string(output), err)
	}

	// Configure git credential helper
	credCmd := exec.Command("git", "config", "--global", "credential.helper", "!gh auth git-credential")
	if output, err := credCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("❌ Failed to configure git credential helper: %s, %w", string(output), err)
	}

	// Set environment variables
	env := append(os.Environ(),
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_TRACE=1",
		"GIT_CURL_VERBOSE=1",
	)

	fmt.Printf("Syncing %s to %s/%s...\n", repoName, targetOrg, repoName)

	// Initialize Git LFS
	lfsInstallCmd := exec.Command("git", "lfs", "install")
	lfsInstallCmd.Dir = repoPath
	lfsInstallCmd.Env = env
	if err := lfsInstallCmd.Run(); err != nil {
		return fmt.Errorf("failed to install git lfs: %w", err)
	}

	// Set the remote URL without embedding the token
	baseURL := fmt.Sprintf("https://github.com/%s/%s.git", targetOrg, repoName)
	remoteCmd := exec.Command("git", "remote", "set-url", "origin", baseURL)
	remoteCmd.Dir = repoPath
	remoteCmd.Env = env
	if err := remoteCmd.Run(); err != nil {
		return fmt.Errorf("failed to set remote url: %w", err)
	}

	// Verify the remote URL
	verifyCmd := exec.Command("git", "remote", "get-url", "origin")
	verifyCmd.Dir = repoPath
	verifyCmd.Env = env
	output, err := verifyCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get remote url: %w", err)
	}
	remoteURL := strings.TrimSpace(string(output))
	fmt.Printf("Verified remote URL: %s\n", remoteURL)

	// Push all branches
	fmt.Printf("Pushing content for %s...\n", repoName)
	pushCmd := exec.Command("git", "push", "--all", "origin")
	pushCmd.Dir = repoPath
	pushCmd.Env = env
	if output, err := pushCmd.CombinedOutput(); err != nil {
		// Mask token in error message
		errMsg := strings.ReplaceAll(string(output), token, "****")
		return fmt.Errorf("failed to push content: %s, %w", errMsg, err)
	}

	// Push LFS content
	lfsPushCmd := exec.Command("git", "lfs", "push", "--all", "origin")
	lfsPushCmd.Dir = repoPath
	lfsPushCmd.Env = env
	if output, err := lfsPushCmd.CombinedOutput(); err != nil {
		// Mask token in error message
		errMsg := strings.ReplaceAll(string(output), token, "****")
		return fmt.Errorf("failed to push LFS content: %s, %w", errMsg, err)
	}

	fmt.Printf("Successfully synced content for %s\n", repoName)
	return nil
}
