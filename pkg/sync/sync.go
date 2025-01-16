package sync

import (
    "encoding/csv"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "bufio"

    "github.com/mona-actions/gh-migrate-lfs/pkg/common"
    "github.com/spf13/viper"
)

type syncJob struct {
    repoName  string
    workDir   string
    targetOrg string
}

func SyncFromCSV() error {
    inputFile := viper.GetString("GHMLFS_FILE")
    workDir := viper.GetString("GHMLFS_WORK_DIR")
    targetOrg := viper.GetString("GHMLFS_TARGET_ORGANIZATION")
    token := viper.GetString("GHMLFS_TARGET_TOKEN")
    maxWorkers := viper.GetInt("GHMLFS_WORKERS")
    branchMode := viper.GetBool("GHMLFS_BRANCH_MODE")

	// Ensure at least 1 worker
	if maxWorkers <= 0 {
		maxWorkers = 1
	}

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
        if branchMode {
            return SyncLFSContentBranchMode(job.repoName, job.workDir, job.targetOrg, token)
        }
        return SyncLFSContentMirrorMode(job.repoName, job.workDir, job.targetOrg, token)
    })

    // Print summary
    stats.PrintSummary(workDir)

    if err != nil {
        return err
    }

    fmt.Println("\n✅ Sync completed successfully!")
    return nil
}

func SyncLFSContentMirrorMode(repoName, workDir, targetOrg, token string) error {
    repoPath := filepath.Join(workDir, repoName)

    if err := configureGitAuth(token); err != nil {
        return err
    }

    // Set environment variables
    env := setupGitEnv()

    fmt.Printf("Syncing %s to %s/%s...\n", repoName, targetOrg, repoName)

    // Set the remote URL without embedding the token
    baseURL := fmt.Sprintf("https://github.com/%s/%s.git", targetOrg, repoName)
    if err := setAndVerifyRemote(repoPath, baseURL, env); err != nil {
        return err
    }

    // Push all LFS content
    lfsPushCmd := exec.Command("git", "lfs", "push", "--all", "origin")
    lfsPushCmd.Dir = repoPath
    lfsPushCmd.Env = env
    if output, err := lfsPushCmd.CombinedOutput(); err != nil {
        errMsg := strings.ReplaceAll(string(output), token, "****")
        return fmt.Errorf("failed to push LFS content: %s, %w", errMsg, err)
    }

    fmt.Printf("Successfully synced content for %s\n", repoName)
    return nil
}

func SyncLFSContentBranchMode(repoName, workDir, targetOrg, token string) error {
    repoPath := filepath.Join(workDir, repoName)

    if err := configureGitAuth(token); err != nil {
        return err
    }

    // Set environment variables
    env := setupGitEnv()

    fmt.Printf("Syncing %s to %s/%s...\n", repoName, targetOrg, repoName)

    // Set the remote URL without embedding the token
    baseURL := fmt.Sprintf("https://github.com/%s/%s.git", targetOrg, repoName)
    if err := setAndVerifyRemote(repoPath, baseURL, env); err != nil {
        return err
    }

    // Get the default branch using symbolic-ref
    defaultBranchCmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
    defaultBranchCmd.Dir = repoPath
    output, err := defaultBranchCmd.Output()
    if err != nil {
        return fmt.Errorf("failed to get default branch: %w", err)
    }
    defaultBranch := strings.TrimPrefix(
        strings.TrimSpace(string(output)),
        "refs/remotes/origin/",
    )

    // Get list of all remote branches
    branchCmd := exec.Command("git", "for-each-ref", "--format=%(refname)", "refs/remotes/origin")
    branchCmd.Dir = repoPath
    output, err = branchCmd.Output()
    if err != nil {
        return fmt.Errorf("failed to list branches: %w", err)
    }

    // Process branches, starting with default branch
    branches := []string{}
    scanner := bufio.NewScanner(strings.NewReader(string(output)))
    for scanner.Scan() {
        branch := scanner.Text()
        if strings.HasSuffix(branch, "/HEAD") {
            continue
        }
        branchName := strings.TrimPrefix(branch, "refs/remotes/origin/")
        if branchName != defaultBranch {
            branches = append(branches, branchName)
        }
    }

    // Process default branch first
    if err := processBranch(repoPath, defaultBranch, env, token); err != nil {
        return err
    }

    // Process remaining branches
    for _, branchName := range branches {
        if err := processBranch(repoPath, branchName, env, token); err != nil {
            return err
        }
    }

    return nil
}

// Helper function to process a single branch
func processBranch(repoPath, branchName string, env []string, token string) error {
    // Checkout branch
    checkoutCmd := exec.Command("git", "checkout", branchName)
    checkoutCmd.Dir = repoPath
    if output, err := checkoutCmd.CombinedOutput(); err != nil {
        return fmt.Errorf("failed to checkout branch %s: %s, %w", branchName, string(output), err)
    }

    // Reset and clean
    resetCmd := exec.Command("git", "reset", "--hard")
    resetCmd.Dir = repoPath
    if output, err := resetCmd.CombinedOutput(); err != nil {
        return fmt.Errorf("failed to reset branch %s: %s, %w", branchName, string(output), err)
    }

    cleanCmd := exec.Command("git", "clean", "-f", "-d")
    cleanCmd.Dir = repoPath
    if output, err := cleanCmd.CombinedOutput(); err != nil {
        return fmt.Errorf("failed to clean branch %s: %s, %w", branchName, string(output), err)
    }

    // Push LFS content for this branch
    lfsPushCmd := exec.Command("git", "lfs", "push", "origin", branchName, "--all")
    lfsPushCmd.Dir = repoPath
    lfsPushCmd.Env = env
    if output, err := lfsPushCmd.CombinedOutput(); err != nil {
        errMsg := strings.ReplaceAll(string(output), token, "****")
        return fmt.Errorf("failed to push LFS content for branch %s: %s, %w", branchName, errMsg, err)
    }

    fmt.Printf("Successfully synced content for branch %s\n", branchName)
    return nil
}

func configureGitAuth(token string) error {
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

    return nil
}

func setAndVerifyRemote(repoPath, baseURL string, env []string) error {
    // Set the remote URL
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
    fmt.Printf("remote URL: %s\n", remoteURL)

    return nil
}

// Helper function to set up git environment variables.
func setupGitEnv() []string {
    return append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
}