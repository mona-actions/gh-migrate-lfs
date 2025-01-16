package pull

import (
    "encoding/csv"
    "fmt"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "strings"

    "github.com/mona-actions/gh-migrate-lfs/pkg/common"
    "github.com/pterm/pterm"
    "github.com/spf13/viper"
)

type pullJob struct {
    name     string
    cloneURL string
}

func PullLFSFromCSV() error {
    inputFile := viper.GetString("GHMLFS_FILE")
    token := viper.GetString("GHMLFS_SOURCE_TOKEN")
    workDir := viper.GetString("GHMLFS_WORK_DIR")
    maxWorkers := viper.GetInt("GHMLFS_WORKERS")
    branchMode := viper.GetBool("GHMLFS_BRANCH_MODE")

    // Ensure at least 1 worker
    if maxWorkers <= 0 {
        maxWorkers = 1
    }

	if branchMode {
		pterm.Info.Printf("Mode: Branching\n")
	} else {
		pterm.Info.Printf("Mode: Mirroring\n")
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
    jobs := make(chan pullJob)
    seen := make(map[string]bool)

    // Start goroutine to send jobs
    go func() {
        defer close(jobs)
        for {
            record, err := reader.Read()
            if err != nil {
                if err == io.EOF {
                    break
                }
                fmt.Printf("Error reading CSV record: %v\n", err)
                continue
            }
            if len(record) != 3 {
                fmt.Printf("Invalid CSV record format, expected 3 columns got %d\n", len(record))
                continue
            }

            repoName := record[0]
            if seen[repoName] {
                continue
            }
            seen[repoName] = true

            jobs <- pullJob{
                name:     repoName,
                cloneURL: record[2],
            }
        }
    }()

    // Create and run worker pool
    stats := common.NewProcessStats()
    err = common.WorkerPool(jobs, maxWorkers, stats, func(job pullJob) error {
        // Authenticate URL here, in the worker
        urlParts := strings.SplitN(job.cloneURL, "://", 2)
        if len(urlParts) != 2 {
            return fmt.Errorf("invalid clone URL format for %s", job.name)
        }
        authenticatedURL := fmt.Sprintf("%s://%s@%s", urlParts[0], token, urlParts[1])

        if branchMode {
            return PullLFSContentBranchMode(job.name, authenticatedURL, token, workDir)
        }
        return PullLFSContentMirrorMode(job.name, authenticatedURL, token, workDir)
    })

    // Print summary
    stats.PrintSummary(workDir)

    if err != nil {
        return err
    }

    fmt.Println("\n✅ Pull completed successfully!")
    return nil
}

func PullLFSContentMirrorMode(repoName, cloneURL, token, workDir string) error {
    repoPath := filepath.Join(workDir, repoName)

    // Create working directory if it doesn't exist
    if err := os.MkdirAll(workDir, 0755); err != nil {
        return fmt.Errorf("❌ Failed to create working directory: %w", err)
    }

    // Check if the repository already exists
    if _, err := os.Stat(repoPath); err == nil {
        pterm.Info.Printf("Repository exists '%s', proceeding with update\n", repoName)

        pullCmd := exec.Command("git", "fetch", "--prune", "origin", "+refs/*:refs/*")
        pullCmd.Dir = repoPath
        if output, err := pullCmd.CombinedOutput(); err != nil {
            return fmt.Errorf("❌ Failed to pull updates: %s, %w", string(output), err)
        }

        lfsPullCmd := exec.Command("git", "lfs", "fetch", "--all")
        lfsPullCmd.Dir = repoPath
        if output, err := lfsPullCmd.CombinedOutput(); err != nil {
            return fmt.Errorf("❌ Failed to pull LFS content: %s, %w", string(output), err)
        }

        pterm.Success.Printf("Synchronization with '%s' completed successfully\n", repoName)
        return nil
    }

    pterm.Info.Printf("Cloning repository '%s'...\n", repoName)
    cloneCmd := exec.Command("git", "clone", "--mirror", "--bare", cloneURL, repoName)
    cloneCmd.Dir = workDir
    if output, err := cloneCmd.CombinedOutput(); err != nil {
        errMsg := strings.ReplaceAll(string(output), token, "****")
        return fmt.Errorf("❌ Failed to clone repository: %s, %w", errMsg, err)
    }

    pterm.Info.Printf("Pulling LFS objects for repository '%s'...\n", repoName)

    lfsPullCmd := exec.Command("git", "lfs", "fetch", "--all")
    lfsPullCmd.Dir = repoPath
    if output, err := lfsPullCmd.CombinedOutput(); err != nil {
        return fmt.Errorf("❌ Failed to fetch LFS content: %s, %w", string(output), err)
    }

    pterm.Success.Printf("synchronized: %s\n", repoName)
    return nil
}

func PullLFSContentBranchMode(repoName, cloneURL, token, workDir string) error {
    repoPath := filepath.Join(workDir, repoName)

    // Create working directory if it doesn't exist
    if err := os.MkdirAll(workDir, 0755); err != nil {
        return fmt.Errorf("❌ Failed to create working directory: %w", err)
    }

    // Check if the repository already exists
    if _, err := os.Stat(repoPath); err == nil {
        pterm.Info.Printf("Repository exists '%s', proceeding with update\n", repoName)
        
        fetchCmd := exec.Command("git", "fetch", "--all")
        fetchCmd.Dir = repoPath
        if output, err := fetchCmd.CombinedOutput(); err != nil {
            return fmt.Errorf("❌ Failed to fetch updates: %s, %w", string(output), err)
        }
    } else {
        pterm.Info.Printf("Cloning repository '%s'...\n", repoName)
        cloneCmd := exec.Command("git", "clone", cloneURL)
        cloneCmd.Dir = workDir
        if output, err := cloneCmd.CombinedOutput(); err != nil {
            errMsg := strings.ReplaceAll(string(output), token, "****")
            return fmt.Errorf("❌ Failed to clone repository: %s, %w", errMsg, err)
        }
    }

    // Pull LFS content for all branches
    lfsPullCmd := exec.Command("git", "lfs", "fetch", "--all")
    lfsPullCmd.Dir = repoPath
    if output, err := lfsPullCmd.CombinedOutput(); err != nil {
        return fmt.Errorf("❌ Failed to pull LFS content: %s, %w", string(output), err)
    }

    pterm.Success.Printf("synchronized: %s\n", repoName)
    return nil
}