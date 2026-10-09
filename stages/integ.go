package stages

//Dagger's service bindings are magic here — Postgres is spun up for the test, torn down after, and never pollutes your host. Same mechanism works for any dependency (Redis, Kafka, etc.).


import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

func Integration(ctx context.Context, dag *dagger.Client, cfg *config.Config,
	src *dagger.Directory) report.StageResult {

	r := report.NewStage("integration")
	defer r.Finish()

	if !cfg.Integration.Enabled {
		r.Skipped = true
		return r
	}

	postgres := dag.Container().From("postgres:16-alpine").
		WithEnvVariable("POSTGRES_PASSWORD", "test").
		WithEnvVariable("POSTGRES_DB", "test").
		WithExposedPort(5432).
		AsService()

	cmd := strings.Fields(cfg.Integration.TestCommand)
	runner := Base(ctx, dag, cfg, src).
		WithServiceBinding("db", postgres).
		WithEnvVariable("DATABASE_URL",
			"postgres://postgres:test@db:5432/test?sslmode=disable").
		WithExec(cmd)

	out, err := runner.Stdout(ctx)
	r.Output = out
	if err != nil {
		r.Fail(fmt.Errorf("integration: %w", err))
	}
	return r
}