package stages

//Two things matter here:
// CacheVolume — Dagger's content-addressed cache. go mod download runs once per go.sum change, then is free forever.
// Exclusions — don't ship your .git folder into the container; it bloats every stage.

import (
	"context"
	"fmt"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
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

// Source returns the project directory with sensible exclusions.
func Source(dag *dagger.Client, root string) *dagger.Directory {
	return dag.Host().Directory(root, dagger.HostDirectoryOpts{
		Exclude: []string{
			".git", "node_modules", "dist", "bin", "coverage",
			"*.log", ".DS_Store", "pipeline/bin",
		},
	})
}