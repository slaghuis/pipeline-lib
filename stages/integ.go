package stages

//Dagger's service bindings are magic here — Postgres is spun up for the test, torn down after, and never pollutes your host. Same mechanism works for any dependency (Redis, Kafka, etc.).

import (
	"context"
	"fmt"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

func Integration(ctx context.Context, dag *dagger.Client, cfg *config.Config,
	src *dagger.Directory) (r report.StageResult) {

	r = report.NewStage("integration")
	defer r.Finish()

	if !cfg.Integration.Enabled {
		r.Skipped = true
		return r
	}
	cmd := cfg.IntegrationCommand()
	if len(cmd) == 0 {
		r.Fail(fmt.Errorf("integration.test_command is empty"))
		return r
	}

	postgres := dag.Container().From("postgres:16-alpine").
		WithEnvVariable("POSTGRES_PASSWORD", "test").
		WithEnvVariable("POSTGRES_DB", "test").
		WithExposedPort(5432).
		AsService()

	runner := Base(ctx, dag, cfg, src).
		WithServiceBinding("db", postgres).
		WithEnvVariable("DATABASE_URL",
			"postgres://postgres:test@db:5432/test?sslmode=disable").
		WithExec(cmd)

	out, err := runner.Stdout(ctx)
	r.Output = out
	if err != nil {
		failExec(&r, "integration", err)
	}
	return r
}
