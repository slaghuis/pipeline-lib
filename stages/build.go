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

// Image is one container image produced by the build stage.
type Image struct {
	Name   string // deployment and container name; the service, or the target in workspace mode
	Ref    string // pushed reference, or local://<name>:<version> when exported to dist/
	Digest string
}

type BuildResult struct {
	report.StageResult
	ImageRef string // single-module mode only
	Digest   string // single-module mode only
	Images   []Image
}

func Build(ctx context.Context, dag *dagger.Client, cfg *config.Config,
	src *dagger.Directory, version string) (br BuildResult) {

	br = BuildResult{StageResult: report.NewStage("build")}
	defer br.Finish()

	// Build for the engine's platform so the binary matches the runtime image;
	// CI/registry publish does multi-arch separately.
	platform, err := dag.DefaultPlatform(ctx)
	if err != nil {
		br.Fail(fmt.Errorf("default platform: %w", err))
		return br
	}
	b := imageBuilder{ctx: ctx, dag: dag, cfg: cfg, src: src, version: version}
	b.goos, b.goarch, _ = strings.Cut(string(platform), "/")
	b.goarch, _, _ = strings.Cut(b.goarch, "/")

	if !cfg.IsWorkspace() {
		img, err := b.build(cfg.Service, cfg.Build.Image, cfg.MainPackage, "./dist/image.tar")
		if err != nil {
			failExec(&br.StageResult, "build", err)
			return br
		}
		br.ImageRef, br.Digest, br.Images = img.Ref, img.Digest, []Image{img}
		setMetric(&br.StageResult, "image_ref", img.Ref)
		if img.Digest != "" {
			setMetric(&br.StageResult, "digest", img.Digest)
		}
		return br
	}

	if len(cfg.Build.Targets) == 0 {
		br.Skipped = true
		br.Output = "no build targets: build.targets is empty and nothing matches " + config.DefaultTargetGlob
		return br
	}

	type result struct {
		name string
		img  Image
		err  error
	}
	results := parallel(cfg.Build.Targets, func(t config.Target) result {
		img, err := b.build(t.Name, t.Name, t.MainPackage, "./dist/"+t.Name+".tar")
		return result{t.Name, img, err}
	})

	images := map[string]string{}
	status := map[string]string{}
	var failed, outputs []string
	for _, res := range results {
		if res.err != nil {
			status[res.name] = "fail"
			failed = append(failed, res.name)
			outputs = append(outputs, fmt.Sprintf("== build: %s ==\n%s", res.name, execDetail(res.err)))
			continue
		}
		status[res.name] = "ok"
		images[res.name] = res.img.Ref
		br.Images = append(br.Images, res.img)
	}
	setMetric(&br.StageResult, "targets", status)
	setMetric(&br.StageResult, "images", images)
	br.Output = strings.Join(outputs, "\n")
	if len(failed) > 0 {
		br.Fail(fmt.Errorf("build failed for %d target(s): %s", len(failed), strings.Join(failed, ", ")))
	}
	return br
}

type imageBuilder struct {
	ctx          context.Context
	dag          *dagger.Client
	cfg          *config.Config
	src          *dagger.Directory
	version      string
	goos, goarch string
}

// build compiles mainPkg into a distroless image and publishes it as
// <registry>/<image>:<version>, or exports it to exportPath when no registry
// is configured.
func (b imageBuilder) build(name, image, mainPkg, exportPath string) (Image, error) {
	ldflags := strings.TrimSpace(b.cfg.Build.LDFlags + fmt.Sprintf(" -X main.version=%s -X main.builtAt=%s",
		b.version, time.Now().UTC().Format(time.RFC3339)))

	bin := Base(b.ctx, b.dag, b.cfg, b.src).
		WithEnvVariable("GOOS", b.goos).
		WithEnvVariable("GOARCH", b.goarch).
		WithExec([]string{
			"go", "build",
			"-trimpath",
			"-ldflags", ldflags,
			"-o", "/out/app",
			mainPkg,
		}).
		File("/out/app")

	ctr := b.dag.Container().
		From("gcr.io/distroless/static-debian12:nonroot").
		WithFile("/app", bin, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithEntrypoint([]string{"/app"}).
		WithLabel("org.opencontainers.image.title", name).
		WithLabel("org.opencontainers.image.version", b.version)

	if b.cfg.Build.Registry != "" {
		ref := fmt.Sprintf("%s/%s:%s", strings.TrimRight(b.cfg.Build.Registry, "/"), image, b.version)
		digest, err := ctr.Publish(b.ctx, ref)
		if err != nil {
			return Image{}, fmt.Errorf("publish %s: %w", ref, err)
		}
		return Image{Name: name, Ref: ref, Digest: digest}, nil
	}
	if _, err := ctr.AsTarball().Export(b.ctx, exportPath); err != nil {
		return Image{}, fmt.Errorf("export %s: %w", exportPath, err)
	}
	return Image{Name: name, Ref: "local://" + name + ":" + b.version}, nil
}
