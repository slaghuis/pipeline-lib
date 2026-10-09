package stages

import (
	"context"
	"fmt"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

func Scan(ctx context.Context, dag *dagger.Client, cfg *config.Config, src *dagger.Directory) report.StageResult {
	r := report.NewStage("scan")
	defer r.Finish()

	if cfg.Scan.Govulncheck {
		c := Base(ctx, dag, cfg, src).
			WithExec([]string{"go", "install", "golang.org/x/vuln/cmd/govulncheck@latest"}).
			WithExec([]string{"/go/bin/govulncheck", "./..."})
		out, err := c.Stdout(ctx)
		r.Output += out
		if err != nil {
			r.Fail(fmt.Errorf("govulncheck: %w", err))
			return r
		}
	}

	if cfg.Scan.Trivy {
		trivy := dag.Container().From("aquasec/trivy:latest").
			WithMountedDirectory("/scan", src).
			WithWorkdir("/scan").
			WithExec([]string{
				"trivy", "fs",
				"--severity", severityChain(cfg.Scan.FailOn),
				"--exit-code", "1",
				".",
			})
		out, err := trivy.Stdout(ctx)
		r.Output += "\n" + out
		if err != nil {
			r.Fail(fmt.Errorf("trivy: %w", err))
		}
	}
	return r
}

func severityChain(level string) string {
	switch level {
	case "critical":
		return "CRITICAL"
	case "high":
		return "CRITICAL,HIGH"
	case "medium":
		return "CRITICAL,HIGH,MEDIUM"
	default:
		return "CRITICAL,HIGH"
	}
}