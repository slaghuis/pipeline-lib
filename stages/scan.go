package stages

import (
	"context"
	"strings"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

func Scan(ctx context.Context, dag *dagger.Client, cfg *config.Config, src *dagger.Directory) (r report.StageResult) {
	r = report.NewStage("scan")
	defer r.Finish()

	if cfg.Scan.Govulncheck {
		// govulncheck@latest may need a newer Go than the service targets, so
		// let the install fetch its own toolchain; the scan itself still runs
		// with the service's Go version.
		c := Base(ctx, dag, cfg, src).
			WithEnvVariable("GOTOOLCHAIN", "auto").
			WithExec([]string{"go", "install", "golang.org/x/vuln/cmd/govulncheck@latest"}).
			WithEnvVariable("GOTOOLCHAIN", "local").
			WithExec([]string{"/go/bin/govulncheck", "./..."})
		out, err := c.Stdout(ctx)
		r.Output += out
		if err != nil {
			failExec(&r, "govulncheck", err)
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
				// pipeline/ is this build tool's own module; it never ships.
				"--skip-dirs", "pipeline",
				".",
			})
		out, err := trivy.Stdout(ctx)
		r.Output = strings.TrimSpace(r.Output + "\n" + out)
		if err != nil {
			failExec(&r, "trivy", err)
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
