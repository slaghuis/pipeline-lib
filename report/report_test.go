package report

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 100); got != "short" {
		t.Fatalf("short input changed: %q", got)
	}
	s := "HEAD" + strings.Repeat("x", 1000) + "TAIL"
	got := Truncate(s, 100)
	if !strings.HasPrefix(got, "HEAD") || !strings.HasSuffix(got, "TAIL") {
		t.Fatalf("must keep head and tail: %q", got)
	}
	if !strings.Contains(got, "[908 bytes truncated]") {
		t.Fatalf("missing truncation marker: %q", got)
	}
}

func TestFinishCapsOutput(t *testing.T) {
	r := NewStage("scan")
	r.Output = strings.Repeat("y", MaxStageOutput*2)
	r.Finish()
	if len(r.Output) > MaxStageOutput+100 {
		t.Fatalf("output not capped: %d bytes", len(r.Output))
	}
}
