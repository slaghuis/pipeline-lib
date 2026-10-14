package stages

import (
	"context"
	"strings"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

func Lint(ctx context.Context, dag *dagger.Client, cfg *config.Config, src *dagger.Directory) (r report.StageResult) {
	r = report.NewStage("lint")
	defer r.Finish()

	if !cfg.Lint.Enabled {
		r.Skipped = true
		return r
	}

	linter := Base(ctx, dag, cfg, src).
		WithExec([]string{"apk", "add", "--no-cache", "wget"}).
		WithEnvVariable("GOLANGCI_LINT_VERSION", strings.TrimPrefix(cfg.Lint.GolangCILintVersion, "v")).
		WithExec([]string{"sh", "-c", installGolangCILint}).
		WithExec([]string{
			"golangci-lint", "run",
			"--timeout", cfg.Lint.Timeout,
			"./...",
		})

	out, err := linter.Stdout(ctx)
	r.Output = out
	if err != nil {
		failExec(&r, "golangci-lint", err)
		return r
	}

	vet := Base(ctx, dag, cfg, src).WithExec([]string{"go", "vet", "./..."})
	if _, err := vet.Stdout(ctx); err != nil {
		failExec(&r, "go vet", err)
	}
	return r
}

// installGolangCILint downloads a release archive and verifies it against the
// release's checksum file. The checksum line is matched on the exact file name
// because the file also lists "<archive>.sbom.json", which upstream install.sh
// picks up by mistake.
const installGolangCILint = `set -eu
v="$GOLANGCI_LINT_VERSION"
name="golangci-lint-${v}-linux-$(go env GOARCH)"
base="https://github.com/golangci/golangci-lint/releases/download/v${v}"
cd /tmp
wget -q "${base}/${name}.tar.gz"
wget -q "${base}/golangci-lint-${v}-checksums.txt"
grep "  ${name}.tar.gz\$" "golangci-lint-${v}-checksums.txt" > expected.sha256
sha256sum -c expected.sha256
tar -xzf "${name}.tar.gz"
mv "${name}/golangci-lint" /usr/local/bin/golangci-lint
`
