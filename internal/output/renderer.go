package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const clearLine = "\r\x1b[2K"
const plainStatusInterval = 30 * time.Second

type Options struct {
	Out     io.Writer
	ErrOut  io.Writer
	TTY     bool
	Quiet   bool
	Verbose bool
	JSON    bool
}

type Renderer struct {
	mu           sync.Mutex
	out          io.Writer
	errOut       io.Writer
	tty          bool
	quiet        bool
	verbose      bool
	jsonMode     bool
	status       string
	lastStatusAt time.Time
	results      map[string]any
	writeErr     error
}

func New(options Options) *Renderer {
	out := options.Out
	if out == nil {
		out = io.Discard
	}
	errOut := options.ErrOut
	if errOut == nil {
		errOut = io.Discard
	}
	return &Renderer{
		out:      out,
		errOut:   errOut,
		tty:      options.TTY,
		quiet:    options.Quiet,
		verbose:  options.Verbose,
		jsonMode: options.JSON,
		results:  make(map[string]any),
	}
}

func (renderer *Renderer) Status(format string, args ...any) {
	if renderer == nil {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if renderer.quiet {
		return
	}
	message := cleanLine(fmt.Sprintf(format, args...))
	if renderer.tty {
		renderer.status = message
		renderer.write(renderer.errOut, clearLine+message)
		return
	}
	now := time.Now()
	if !renderer.lastStatusAt.IsZero() && now.Sub(renderer.lastStatusAt) < plainStatusInterval {
		return
	}
	renderer.lastStatusAt = now
	renderer.write(renderer.errOut, message+"\n")
}

func (renderer *Renderer) Line(format string, args ...any) {
	if renderer == nil {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if renderer.quiet {
		return
	}
	renderer.lineLocked(fmt.Sprintf(format, args...))
}

func (renderer *Renderer) Error(format string, args ...any) {
	if renderer == nil {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	renderer.lineLocked(fmt.Sprintf(format, args...))
}

func (renderer *Renderer) Verbose(format string, args ...any) {
	if renderer == nil || !renderer.verbose {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	renderer.lineLocked(fmt.Sprintf(format, args...))
}

func (renderer *Renderer) FinishStatus(format string, args ...any) {
	if renderer == nil {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if renderer.quiet {
		return
	}
	message := cleanLine(fmt.Sprintf(format, args...))
	if renderer.tty {
		renderer.clearStatusLocked()
	}
	renderer.status = ""
	renderer.write(renderer.errOut, message+"\n")
}

func (renderer *Renderer) Result(format string, args ...any) {
	if renderer == nil {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if renderer.quiet || renderer.jsonMode {
		return
	}
	renderer.clearStatusLocked()
	renderer.write(renderer.out, fmt.Sprintf(format, args...))
}

func (renderer *Renderer) ClearStatus() {
	if renderer == nil {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	renderer.clearStatusLocked()
	renderer.status = ""
}

func (renderer *Renderer) Err() error {
	if renderer == nil {
		return nil
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	return renderer.writeErr
}

func (renderer *Renderer) lineLocked(message string) {
	message = strings.TrimRight(message, "\r\n")
	status := renderer.status
	if renderer.tty && status != "" {
		renderer.clearStatusLocked()
	}
	renderer.write(renderer.errOut, message+"\n")
	if renderer.tty && status != "" {
		renderer.write(renderer.errOut, status)
	}
}

func (renderer *Renderer) clearStatusLocked() {
	if renderer.tty && renderer.status != "" {
		renderer.write(renderer.errOut, clearLine)
	}
}

func (renderer *Renderer) write(writer io.Writer, value string) {
	if _, err := io.WriteString(writer, value); err != nil {
		renderer.writeErr = errors.Join(renderer.writeErr, err)
	}
}

func cleanLine(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(value))
}

func (renderer *Renderer) Record(name string, value any) {
	if renderer == nil {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	renderer.results[name] = value
}

func (renderer *Renderer) FlushJSON(command string, runErr error) {
	if renderer == nil || !renderer.jsonMode {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	renderer.clearStatusLocked()
	document := struct {
		Command  string         `json:"command"`
		Complete bool           `json:"complete"`
		Error    string         `json:"error,omitempty"`
		Phases   map[string]any `json:"phases"`
	}{
		Command:  command,
		Complete: runErr == nil,
		Phases:   renderer.results,
	}
	if runErr != nil {
		document.Error = runErr.Error()
	}
	encoder := json.NewEncoder(renderer.out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		renderer.writeErr = errors.Join(renderer.writeErr, err)
	}
}
