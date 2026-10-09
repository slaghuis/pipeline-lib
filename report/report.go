package report

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type StageResult struct {
	Name     string                 `json:"name"`
	Success  bool                   `json:"success"`
	Skipped  bool                   `json:"skipped"`
	Error    string                 `json:"error,omitempty"`
	Output   string                 `json:"output,omitempty"`
	Metrics  map[string]any         `json:"metrics,omitempty"`
	StartedAt time.Time             `json:"started_at"`
	DurationS float64               `json:"duration_s"`

	errObj error
}

func (r *StageResult) Fail(err error) {
	r.errObj = err
	r.Error = err.Error()
}

func (r *StageResult) Finish() {
	r.Success = r.errObj == nil && !r.Skipped
	r.DurationS = time.Since(r.StartedAt).Seconds()
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
		r.DurationS = 0
	}
}

type Report struct {
	Pipeline  string         `json:"pipeline"`
	Version   string         `json:"version"`
	StartedAt time.Time      `json:"started_at"`
	DurationS float64        `json:"duration_s"`
	Success   bool           `json:"success"`
	Stages    []StageResult  `json:"stages"`
}

func NewStage(name string) StageResult {
	return StageResult{Name: name, StartedAt: time.Now()}
}

// Print writes a human-readable summary to stderr and a JSON doc to stdout
// (so agents can parse stdout cleanly).
func (r *Report) Print() {
	// Human summary on stderr
	fmt.Fprintf(os.Stderr, "\n==== Pipeline %s (%.1fs) ====\n", r.Pipeline, r.DurationS)
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