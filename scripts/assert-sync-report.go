package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type document struct {
	Complete bool `json:"complete"`
	Phases   struct {
		Sync syncSummary `json:"sync"`
	} `json:"phases"`
}

type syncSummary struct {
	Complete bool `json:"complete"`
	Failed   int  `json:"failed"`
	Issues   int  `json:"issues"`
	Stats    struct {
		Objects           int `json:"objects"`
		WouldUpload       int `json:"would_upload"`
		Uploaded          int `json:"uploaded"`
		AlreadyPresent    int `json:"already_present"`
		RemotePresent     int `json:"remote_present"`
		RemoteMissing     int `json:"remote_missing"`
		RemoteErrors      int `json:"remote_errors"`
		ServerErrors      int `json:"server_errors"`
		UploadFailures    int `json:"upload_failures"`
		BatchFailures     int `json:"batch_failures"`
		UnexpectedReplies int `json:"unexpected_replies"`
	} `json:"stats"`
}

func main() {
	if len(os.Args) != 7 {
		fmt.Fprintln(os.Stderr, "usage: assert-sync-report REPORT OBJECTS WOULD_UPLOAD UPLOADED PRESENT REMOTE_PRESENT")
		os.Exit(2)
	}

	expected := make([]int, 5)
	for index, value := range os.Args[2:] {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			fmt.Fprintf(os.Stderr, "invalid expected counter %q\n", value)
			os.Exit(2)
		}
		expected[index] = parsed
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read report: %v\n", err)
		os.Exit(1)
	}
	var report document
	if err := json.Unmarshal(data, &report); err != nil {
		fmt.Fprintf(os.Stderr, "decode report: %v\n", err)
		os.Exit(1)
	}

	stats := report.Phases.Sync.Stats
	actual := []int{
		stats.Objects,
		stats.WouldUpload,
		stats.Uploaded,
		stats.AlreadyPresent,
		stats.RemotePresent,
	}
	names := []string{"objects", "would_upload", "uploaded", "already_present", "remote_present"}
	valid := report.Complete && report.Phases.Sync.Complete && report.Phases.Sync.Failed == 0 && report.Phases.Sync.Issues == 0
	for index, value := range actual {
		if value != expected[index] {
			fmt.Fprintf(os.Stderr, "%s = %d, want %d\n", names[index], value, expected[index])
			valid = false
		}
	}
	failureCounters := map[string]int{
		"remote_missing":     stats.RemoteMissing,
		"remote_errors":      stats.RemoteErrors,
		"server_errors":      stats.ServerErrors,
		"upload_failures":    stats.UploadFailures,
		"batch_failures":     stats.BatchFailures,
		"unexpected_replies": stats.UnexpectedReplies,
	}
	for name, value := range failureCounters {
		if value != 0 {
			fmt.Fprintf(os.Stderr, "%s = %d, want 0\n", name, value)
			valid = false
		}
	}
	if !report.Complete || !report.Phases.Sync.Complete || report.Phases.Sync.Failed != 0 || report.Phases.Sync.Issues != 0 {
		fmt.Fprintf(os.Stderr, "sync incomplete: document_complete=%t phase_complete=%t failed=%d issues=%d\n",
			report.Complete, report.Phases.Sync.Complete, report.Phases.Sync.Failed, report.Phases.Sync.Issues)
	}
	if !valid {
		os.Exit(1)
	}
}
