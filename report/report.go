package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type StageResult struct {
	Name      string         `json:"name"`
	Success   bool           `json:"success"`
	Skipped   bool           `json:"skipped"`
	Error     string         `json:"error,omitempty"`
	Output    string         `json:"output,omitempty"`
	Metrics   map[string]any `json:"metrics,omitempty"`
	StartedAt time.Time      `json:"started_at"`
	DurationS float64        `json:"duration_s"`

	errObj error
}

// Fail marks the stage as failed. Calling it again keeps earlier errors.
func (r *StageResult) Fail(err error) {
	r.errObj = errors.Join(r.errObj, err)
	r.Error = strings.ReplaceAll(r.errObj.Error(), "\n", "; ")
}

func (r *StageResult) Finish() {
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}
	r.Success = r.errObj == nil && !r.Skipped
	r.DurationS = time.Since(r.StartedAt).Seconds()
	r.Output = Truncate(r.Output, MaxStageOutput)
}

// MaxStageOutput caps StageResult.Output so the JSON report stays small
// enough for an agent to read.
const MaxStageOutput = 64 << 10

// Truncate shortens s to about max bytes, keeping the first three quarters
// and the last quarter, which hold the first error and the closing summary.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	head, tail := max*3/4, max/4
	return fmt.Sprintf("%s\n… [%d bytes truncated] …\n%s",
		strings.ToValidUTF8(s[:head], ""), len(s)-head-tail, strings.ToValidUTF8(s[len(s)-tail:], ""))
}

// OK reports whether the stage passed or was intentionally skipped.
func (r StageResult) OK() bool {
	return r.Success || r.Skipped
}

type Report struct {
	Pipeline  string        `json:"pipeline"`
	Service   string        `json:"service"`
	Version   string        `json:"version"`
	StartedAt time.Time     `json:"started_at"`
	DurationS float64       `json:"duration_s"`
	Success   bool          `json:"success"`
	Stages    []StageResult `json:"stages"`
}

func NewStage(name string) StageResult {
	return StageResult{Name: name, StartedAt: time.Now()}
}

// AllSuccess reports whether every stage passed or was skipped.
func AllSuccess(stages []StageResult) bool {
	for _, s := range stages {
		if !s.OK() {
			return false
		}
	}
	return true
}

// Emit writes a human-readable summary to stderr and a JSON doc to stdout
// (so agents can parse stdout cleanly).
func (r *Report) Emit() {
	// Human summary on stderr
	fmt.Fprintf(os.Stderr, "\n==== Pipeline %s %s@%s (%.1fs) ====\n", r.Pipeline, r.Service, r.Version, r.DurationS)
	for _, s := range r.Stages {
		status := "✓"
		switch {
		case s.Skipped:
			status = "⊘"
		case !s.Success:
			status = "✗"
		}
		fmt.Fprintf(os.Stderr, "  %s %-20s %5.1fs %s\n", status, s.Name, s.DurationS, s.Error)
	}
	fmt.Fprintf(os.Stderr, "overall: %v\n\n", r.Success)

	// Machine-readable on stdout
	_ = json.NewEncoder(os.Stdout).Encode(r)
}
