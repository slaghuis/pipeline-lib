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
	src *dagger.Directory, version string) (br BuildResult) {

	br = BuildResult{StageResult: report.NewStage("build")}
	defer func() {
		if br.ImageRef != "" {
			br.Metrics = map[string]any{"image_ref": br.ImageRef}
			if br.Digest != "" {
				br.Metrics["digest"] = br.Digest
			}
		}
		br.Finish()
	}()

	ldflags := strings.TrimSpace(cfg.Build.LDFlags + fmt.Sprintf(" -X main.version=%s -X main.builtAt=%s",
		version, time.Now().UTC().Format(time.RFC3339)))

	// Build for the engine's platform so the binary matches the runtime image;
	// CI/registry publish does multi-arch separately.
	platform, err := dag.DefaultPlatform(ctx)
	if err != nil {
		br.Fail(fmt.Errorf("default platform: %w", err))
		return br
	}
	goos, goarch, _ := strings.Cut(string(platform), "/")
	goarch, _, _ = strings.Cut(goarch, "/")

	bin := Base(ctx, dag, cfg, src).
		WithEnvVariable("GOOS", goos).
		WithEnvVariable("GOARCH", goarch).
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
			failExec(&br.StageResult, "publish", err)
			return br
		}
		br.ImageRef = ref
		br.Digest = digest
	} else {
		_, err := image.AsTarball().Export(ctx, "./dist/image.tar")
		if err != nil {
			failExec(&br.StageResult, "export", err)
			return br
		}
		br.ImageRef = "local://" + cfg.Service + ":" + version
	}

	return br
}
