package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type failingWriter struct {
	err error
}

func (writer failingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func TestRendererSerializesLinesAroundTTYStatus(t *testing.T) {
	var stderr bytes.Buffer
	renderer := New(Options{ErrOut: &stderr, TTY: true})
	renderer.Status("Repositories 0/32")

	var waitGroup sync.WaitGroup
	for index := range 32 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			renderer.Line("Complete repo-%02d", index)
			renderer.Status("Repositories %d/32", index+1)
		}()
	}
	waitGroup.Wait()
	renderer.FinishStatus("Repositories 32/32 complete")

	output := stderr.String()
	for index := range 32 {
		line := fmt.Sprintf("Complete repo-%02d\n", index)
		if count := strings.Count(output, line); count != 1 {
			t.Fatalf("%q count = %d", line, count)
		}
	}
	if count := strings.Count(output, "Repositories 32/32 complete\n"); count != 1 {
		t.Fatalf("final status count = %d", count)
	}
	if err := renderer.Err(); err != nil {
		t.Fatalf("Renderer.Err() = %v", err)
	}
}

func TestRendererUsesStableLinesWithoutTTY(t *testing.T) {
	var stderr bytes.Buffer
	renderer := New(Options{ErrOut: &stderr})
	renderer.Status("Searching 1/2")
	renderer.Line("Found api")
	renderer.FinishStatus("Searching 2/2 complete")

	want := "Searching 1/2\nFound api\nSearching 2/2 complete\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRendererQuietStillPrintsErrors(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	renderer := New(Options{Out: &stdout, ErrOut: &stderr, Quiet: true})
	renderer.Status("ignored")
	renderer.Line("ignored")
	renderer.Result("ignored")
	renderer.Error("failed: %s", "denied")

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if got, want := stderr.String(), "failed: denied\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRendererJSONKeepsStdoutMachineReadable(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	renderer := New(Options{Out: &stdout, ErrOut: &stderr, JSON: true})
	renderer.Status("Working")
	renderer.Result("human summary")
	renderer.Record("sync", map[string]int{"uploaded": 3})
	renderer.FlushJSON("sync", nil)

	var document struct {
		Command  string `json:"command"`
		Complete bool   `json:"complete"`
		Phases   struct {
			Sync struct {
				Uploaded int `json:"uploaded"`
			} `json:"sync"`
		} `json:"phases"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, stdout.String())
	}
	if document.Command != "sync" || !document.Complete || document.Phases.Sync.Uploaded != 3 {
		t.Fatalf("document = %#v", document)
	}
	if got := stderr.String(); got != "Working\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRendererThrottlesPlainStatus(t *testing.T) {
	var stderr bytes.Buffer
	renderer := New(Options{ErrOut: &stderr})

	renderer.Status("Searching 1/3")
	renderer.Status("Searching 2/3")
	if got, want := stderr.String(), "Searching 1/3\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}

	renderer.lastStatusAt = time.Now().Add(-plainStatusInterval)
	renderer.Status("Searching 3/3")
	if got, want := stderr.String(), "Searching 1/3\nSearching 3/3\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRendererVerboseRedrawsTTYStatus(t *testing.T) {
	var stderr bytes.Buffer
	renderer := New(Options{ErrOut: &stderr, TTY: true, Verbose: true})

	renderer.Status("Repositories 1/2")
	renderer.Verbose("Retrying request")
	renderer.ClearStatus()

	want := clearLine + "Repositories 1/2" + clearLine + "Retrying request\n" + "Repositories 1/2" + clearLine
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if renderer.status != "" {
		t.Fatalf("status = %q", renderer.status)
	}
}

func TestRendererVerboseDisabledProducesNoOutput(t *testing.T) {
	var stderr bytes.Buffer
	renderer := New(Options{ErrOut: &stderr})
	renderer.Verbose("hidden")
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRendererJSONIncludesRunFailure(t *testing.T) {
	var stdout bytes.Buffer
	renderer := New(Options{Out: &stdout, JSON: true})
	wantErr := errors.New("upload denied")
	renderer.FlushJSON("sync", wantErr)

	var document struct {
		Complete bool   `json:"complete"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Complete || document.Error != wantErr.Error() {
		t.Fatalf("document = %#v", document)
	}
}

func TestRendererReportsWriterFailures(t *testing.T) {
	wantErr := errors.New("writer failed")
	renderer := New(Options{Out: failingWriter{err: wantErr}, ErrOut: failingWriter{err: wantErr}, JSON: true})

	renderer.Status("working")
	renderer.Error("failed")
	renderer.FlushJSON("sync", nil)

	if err := renderer.Err(); !errors.Is(err, wantErr) {
		t.Fatalf("Renderer.Err() = %v", err)
	}
}
