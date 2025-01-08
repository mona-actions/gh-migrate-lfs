package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/mona-actions/gh-migrate-lfs/cmd"
)

func main() {
	// Check if git-lfs is available in the system.
	if err := checkGitLFS(); err != nil {
		fmt.Fprintf(os.Stderr, "git lfs command not found. Please download Git LFS from https://git-lfs.com\n%v", err)
		return // Exit the program
	}

	cmd.Execute()
}

// checkGitLFS checks for the presence of git lfs and returns an error if it's not found.
func checkGitLFS() error {
	cmd := exec.Command("git", "lfs", "--version")
	output, err := cmd.CombinedOutput()

	if err != nil {
		return fmt.Errorf("failed to execute 'git lfs --version': exit status 1")
	}

	fmt.Println(string(output)) // Print the version for debugging
	return nil
}
