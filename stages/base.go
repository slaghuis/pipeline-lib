package stages

//Two things matter here:
// CacheVolume — Dagger's content-addressed cache. go mod download runs once per go.sum change, then is free forever.
// Exclusions — don't ship your .git folder into the container; it bloats every stage.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

// Base returns a Go container with the module and dependencies cached.
// This is the foundation every stage builds on.
func Base(ctx context.Context, dag *dagger.Client, cfg *config.Config, src *dagger.Directory) *dagger.Container {
	// Shared caches — content-addressed by Dagger across runs.
	modCache := dag.CacheVolume("go-mod-" + cfg.GoVersion)
	buildCache := dag.CacheVolume("go-build-" + cfg.GoVersion + "-" + cfg.Service)

	return dag.Container().
		From(fmt.Sprintf("golang:%s-alpine", cfg.GoVersion)).
		WithEnvVariable("GOFLAGS", "-buildvcs=false").
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedCache("/go/pkg/mod", modCache).
		WithMountedCache("/root/.cache/go-build", buildCache).
		WithWorkdir("/src").
		WithMountedDirectory("/src", src).
		WithExec([]string{"go", "mod", "download"})
}

// failExec records err on r. For a failed container command it keeps the error
// to one line and moves the command's stdout/stderr into r.Output, so the JSON
// report stays readable instead of embedding Dagger's query chain.
func failExec(r *report.StageResult, label string, err error) {
	var ee *dagger.ExecError
	if !errors.As(err, &ee) {
		r.Fail(fmt.Errorf("%s: %w", label, err))
		return
	}
	var parts []string
	for _, s := range []string{r.Output, ee.Stdout, ee.Stderr} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	r.Output = strings.Join(parts, "\n")
	r.Fail(fmt.Errorf("%s: %q exited with code %d", label, strings.Join(ee.Cmd, " "), ee.ExitCode))
}

// Source returns the project directory with sensible exclusions.
func Source(dag *dagger.Client, root string) *dagger.Directory {
	return dag.Host().Directory(root, dagger.HostDirectoryOpts{
		Exclude: []string{
			".git", "node_modules", "dist", "bin", "coverage",
			"*.log", ".DS_Store", "pipeline/bin",
		},
	})
}
