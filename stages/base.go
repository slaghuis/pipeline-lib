package stages

//Two things matter here:
// CacheVolume — Dagger's content-addressed cache. go mod download runs once per go.sum change, then is free forever.
// Exclusions — don't ship your .git folder into the container; it bloats every stage.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

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
	r.Fail(fmt.Errorf("%s: %q exited with code %d", label, shortCmd(ee.Cmd), ee.ExitCode))
}

// shortCmd keeps error messages readable when a command carries one package
// pattern per workspace module.
func shortCmd(cmd []string) string {
	const keep = 8
	if len(cmd) <= keep {
		return strings.Join(cmd, " ")
	}
	return fmt.Sprintf("%s … (+%d more args)", strings.Join(cmd[:keep], " "), len(cmd)-keep)
}

// execDetail returns the command output carried by a failed container
// command, or the error text for any other error.
func execDetail(err error) string {
	var ee *dagger.ExecError
	if !errors.As(err, &ee) {
		return err.Error()
	}
	return strings.TrimSpace(strings.TrimSpace(ee.Stdout) + "\n" + strings.TrimSpace(ee.Stderr))
}

// parallelism caps how many modules or targets a workspace stage runs at once.
const parallelism = 4

// parallel calls fn for each item, at most parallelism at a time, and
// returns the results in item order.
func parallel[T, R any](items []T, fn func(T) R) []R {
	out := make([]R, len(items))
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = fn(it)
		}()
	}
	wg.Wait()
	return out
}

// maxModuleOutput caps one module's output in a stage report, so one noisy
// tool run across many modules cannot crowd out the rest.
const maxModuleOutput = 4 << 10

// moduleResult is the outcome of one command run in one workspace module.
type moduleResult struct {
	Dir    string
	Output string
	Err    error
}

// inModules runs argv from each workspace module's directory and records the
// results on r under metrics[tool]. A failing module does not stop the others.
func inModules(ctx context.Context, r *report.StageResult, cfg *config.Config,
	base *dagger.Container, tool string, argv []string) {

	results := parallel(cfg.Modules, func(m config.Module) moduleResult {
		out, err := base.WithWorkdir(path.Join("/src", m.Dir)).WithExec(argv).Stdout(ctx)
		return moduleResult{Dir: m.Dir, Output: out, Err: err}
	})
	recordModules(r, tool, results)
}

// recordModules stores one status per module under metrics[tool], appends
// the output of failed modules to r.Output, and fails r naming every failed
// module.
func recordModules(r *report.StageResult, tool string, results []moduleResult) {
	status := make(map[string]string, len(results))
	var failed, outputs []string
	for _, mr := range results {
		if mr.Err == nil {
			status[mr.Dir] = "ok"
			continue
		}
		status[mr.Dir] = "fail"
		failed = append(failed, mr.Dir)
		detail := strings.TrimSpace(strings.TrimSpace(mr.Output) + "\n" + execDetail(mr.Err))
		outputs = append(outputs, fmt.Sprintf("== %s: %s ==\n%s", tool, mr.Dir, report.Truncate(detail, maxModuleOutput)))
	}
	setMetric(r, tool, status)
	if len(outputs) > 0 {
		r.Output = strings.TrimSpace(r.Output + "\n" + strings.Join(outputs, "\n"))
	}
	if len(failed) > 0 {
		r.Fail(fmt.Errorf("%s failed in %d module(s): %s", tool, len(failed), strings.Join(failed, ", ")))
	}
}

func setMetric(r *report.StageResult, key string, v any) {
	if r.Metrics == nil {
		r.Metrics = map[string]any{}
	}
	r.Metrics[key] = v
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
