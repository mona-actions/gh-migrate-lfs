package export

import (
	"encoding/csv"
	"fmt"
	"os"
	"time"

	"github.com/mona-actions/gh-migrate-lfs/internal/api"
	"github.com/pterm/pterm"
	"github.com/spf13/viper"
)

// RepoLFSInfo holds information about a repository containing LFS data
type RepoLFSInfo struct {
	Name     string
	Path     string
	CloneURL string
}

func ExportLFSRepos() error {
	start := time.Now()
	spinner, _ := pterm.DefaultSpinner.Start("Searching for repositories with LFS content...")

	// Get configuration
	organization := viper.GetString("GHMLFS_SOURCE_ORGANIZATION")
	token := viper.GetString("GHMLFS_SOURCE_TOKEN")
	depth := viper.GetInt("GHMLFS_SEARCH_DEPTH")
	hostname := viper.GetString("GHMLFS_SOURCE_HOSTNAME")

	if organization == "" || token == "" {
		return fmt.Errorf("missing required parameters: organization, token")
	}

	if depth == 0 {
		depth = 1 // Default depth if not specified
	}

	// Fetch repositories
	pterm.Info.Printf("Fetching repository list for %s...", organization)
	repos, err := api.GetRepositories(organization, token, hostname)
	if err != nil {
		return fmt.Errorf("failed to fetch repositories: %w", err)
	}
	pterm.Info.Printf("Found %d repositories\n", len(repos))

	// Process repositories and collect LFS information
	var lfsRepos []RepoLFSInfo
	var successful, failed, found int

	pterm.Info.Printf("Checking repositories for LFS content (searching up to depth %d)...", depth)

	for _, repo := range repos {
		pterm.Info.Printf("Searching repository contents: '%s'...\n", repo)

		hasLFS, path, err := api.CheckGitAttributes(organization, repo, token, depth, hostname)
		if err != nil {
			pterm.Info.Printf("Warning: Failed to determine LFS status for repo %s: %v", repo, err)
			failed++
			continue
		}

		if hasLFS {
			cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", organization, repo)
			if hostname != "" {
				cloneURL = fmt.Sprintf("%s/%s/%s.git", hostname, organization, repo)
			}

			lfsRepos = append(lfsRepos, RepoLFSInfo{
				Name:     repo,
				Path:     path,
				CloneURL: cloneURL,
			})
			found++
			pterm.Success.Printf("LFS filter matched for repository '%s' (path: %s)\n", repo, path)
		}

		successful++
	}

	// Write results to CSV file
	outputFile := viper.GetString("GHMLFS_SOURCE_ORGANIZATION") + "_lfs.csv"
	if err := writeToCSV(outputFile, lfsRepos); err != nil {
		return fmt.Errorf("failed to write CSV file: %w", err)
	}
	spinner.Success()

	fmt.Printf("\n📊 Export Summary:\n")
	fmt.Printf("Total repositories found: %d\n", len(repos))
	fmt.Printf("✅ Successfully processed: %d repositories\n", successful)
	fmt.Printf("❌ Failed to process: %d repositories\n", failed)
	fmt.Printf("🔍 Maximum search depth: %d\n", depth)
	fmt.Printf("🔍 Repositories with LFS: %d\n", found)
	fmt.Printf("📁 Output file: %s\n", outputFile)
	fmt.Printf("🕐 Total time: %v\n", time.Since(start).Round(time.Second))

	return nil
}

func writeToCSV(filename string, repos []RepoLFSInfo) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating output file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	if err := writer.Write([]string{"Repository", "GitAttributesPaths", "CloneURL"}); err != nil {
		return fmt.Errorf("error writing header: %w", err)
	}

	// Write data
	for _, repo := range repos {
		if err := writer.Write([]string{
			repo.Name,
			repo.Path,
			repo.CloneURL,
		}); err != nil {
			return fmt.Errorf("error writing repository data: %w", err)
		}
	}

	return nil
}
