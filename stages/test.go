package stages

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

func Test(ctx context.Context, dag *dagger.Client, cfg *config.Config, src *dagger.Directory) report.StageResult {
	r := report.NewStage("test")
	defer r.Finish()

	args := []string{"go", "test", "-timeout", cfg.Test.Timeout, "-count=1"}
	if cfg.Test.Race {
		args = append(args, "-race")
	}
	if cfg.Test.Coverage {
		args = append(args, "-coverprofile=coverage.out", "-covermode=atomic")
	}
	if len(cfg.Test.Tags) > 0 {
		args = append(args, "-tags", strings.Join(cfg.Test.Tags, ","))
	}
	args = append(args, "./...")

	base := Base(ctx, dag, cfg, src)
	if cfg.Test.Race {
		base = base.
			WithEnvVariable("CGO_ENABLED", "1").
			WithExec([]string{"apk", "add", "--no-cache", "gcc", "musl-dev"})
	}

	runner := base.WithExec(args)
	out, err := runner.Stdout(ctx)
	r.Output = out
	if err != nil {
		r.Fail(fmt.Errorf("go test: %w", err))
		return r
	}

	if cfg.Test.Coverage {
		covOut, _ := runner.WithExec([]string{"go", "tool", "cover", "-func=coverage.out"}).Stdout(ctx)
		pct := parseCoverage(covOut)
		r.Metrics = map[string]any{"coverage_pct": pct}
		if pct < cfg.Test.CoverageMin {
			r.Fail(fmt.Errorf("coverage %.1f%% < required %.1f%%", pct, cfg.Test.CoverageMin))
		}
	}
	return r
}

var covRe = regexp.MustCompile(`total:\s+\(statements\)\s+([\d.]+)%`)

func parseCoverage(s string) float64 {
	m := covRe.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}