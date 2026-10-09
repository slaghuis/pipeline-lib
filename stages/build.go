package stages

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

type BuildResult struct {
	report.StageResult
	ImageRef string
	Digest   string
}

func Build(ctx context.Context, dag *dagger.Client, cfg *config.Config,
	src *dagger.Directory, version string) BuildResult {

	br := BuildResult{StageResult: report.NewStage("build")}
	defer br.Finish()

	ldflags := cfg.Build.LDFlags + fmt.Sprintf(" -X main.version=%s -X main.builtAt=%s",
		version, time.Now().UTC().Format(time.RFC3339))

	// Build for host arch; CI/registry publish does multi-arch separately.
	bin := Base(ctx, dag, cfg, src).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", "arm64").
		WithExec([]string{
			"go", "build",
			"-trimpath",
			"-ldflags", ldflags,
			"-o", "/out/app",
			cfg.MainPackage,
		}).
		File("/out/app")

	image := dag.Container().
		From("gcr.io/distroless/static-debian12:nonroot").
		WithFile("/app", bin, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithEntrypoint([]string{"/app"}).
		WithLabel("org.opencontainers.image.title", cfg.Service).
		WithLabel("org.opencontainers.image.version", version)

	if cfg.Build.Registry != "" {
		ref := fmt.Sprintf("%s/%s:%s",
			strings.TrimRight(cfg.Build.Registry, "/"),
			cfg.Build.Image, version)
		digest, err := image.Publish(ctx, ref)
		if err != nil {
			br.Fail(fmt.Errorf("publish: %w", err))
			return br
		}
		br.ImageRef = ref
		br.Digest = digest
	} else {
		_, err := image.AsTarball().Export(ctx, "./dist/image.tar")
		if err != nil {
			br.Fail(fmt.Errorf("export: %w", err))
			return br
		}
		br.ImageRef = "local://" + cfg.Service + ":" + version
	}

	return br
}