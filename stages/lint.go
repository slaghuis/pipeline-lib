package stages

import (
	"context"
	"fmt"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

func Lint(ctx context.Context, dag *dagger.Client, cfg *config.Config, src *dagger.Directory) report.StageResult {
	r := report.NewStage("lint")
	defer r.Finish()

	if !cfg.Lint.Enabled {
		r.Skipped = true
		return r
	}

	installCmd := fmt.Sprintf(
		"wget -qO- https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | "+
			"sh -s -- -b /usr/local/bin %s", cfg.Lint.GolangCILintVersion)

	linter := Base(ctx, dag, cfg, src).
		WithExec([]string{"apk", "add", "--no-cache", "wget"}).
		WithExec([]string{"sh", "-c", installCmd}).
		WithExec([]string{
			"golangci-lint", "run",
			"--timeout", cfg.Lint.Timeout,
			"./...",
		})

	out, err := linter.Stdout(ctx)
	r.Output = out
	if err != nil {
		r.Fail(fmt.Errorf("golangci-lint: %w", err))
		return r
	}

	vet := Base(ctx, dag, cfg, src).WithExec([]string{"go", "vet", "./..."})
	if vetOut, err := vet.Stdout(ctx); err != nil {
		r.Fail(fmt.Errorf("go vet: %w", err))
		r.Output += "\n" + vetOut
	}
	return r
}