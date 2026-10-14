package stages

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

func Test(ctx context.Context, dag *dagger.Client, cfg *config.Config, src *dagger.Directory) (r report.StageResult) {
	r = report.NewStage("test")
	defer r.Finish()

	args := []string{"go", "test", "-timeout", cfg.Test.Timeout, "-count=1"}
	if cfg.Test.Race {
		args = append(args, "-race")
	}
	if cfg.Test.Coverage {
		args = append(args, "-coverprofile=coverage.out", "-covermode=atomic")
	}
	if len(cfg.Test.Tags) > 0 {
		args = append(args, "-tags", strings.Join(cfg.Test.Tags, ","))
	}
	args = append(args, cfg.GoPackages()...)

	base := Base(ctx, dag, cfg, src)
	if cfg.Test.Race {
		base = base.
			WithEnvVariable("CGO_ENABLED", "1").
			WithExec([]string{"apk", "add", "--no-cache", "gcc", "musl-dev"})
	}

	runner := base.WithExec(args)
	out, err := runner.Stdout(ctx)
	if cfg.IsWorkspace() {
		stdout := out
		var ee *dagger.ExecError
		if errors.As(err, &ee) {
			stdout = ee.Stdout
		}
		status := testStatusByModule(stdout, cfg.Modules)
		setMetric(&r, "go_test", status)
		if failed := modulesWithStatus(status, "fail"); len(failed) > 0 {
			r.Fail(fmt.Errorf("go test failed in %d module(s): %s", len(failed), strings.Join(failed, ", ")))
		}
	}
	r.Output = out
	if err != nil {
		failExec(&r, "go test", err)
		return r
	}

	if cfg.Test.Coverage {
		covOut, _ := runner.WithExec([]string{"go", "tool", "cover", "-func=coverage.out"}).Stdout(ctx)
		pct := parseCoverage(covOut)
		setMetric(&r, "coverage_pct", pct)
		if pct < cfg.Test.CoverageMin {
			r.Fail(fmt.Errorf("coverage %.1f%% < required %.1f%%", pct, cfg.Test.CoverageMin))
		}
	}
	return r
}

var covRe = regexp.MustCompile(`total:\s+\(statements\)\s+([\d.]+)%`)

func parseCoverage(s string) float64 {
	m := covRe.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

var (
	// "ok  \tpkg\t0.1s", "FAIL\tpkg [build failed]", "?   \tpkg\t[no test files]"
	testLineRe = regexp.MustCompile(`(?m)^(ok|FAIL|\?)\s+(\S+)`)
	// With -cover, a package without tests prints "\tpkg\t\tcoverage: 0.0% of statements".
	noTestsCoverRe = regexp.MustCompile(`(?m)^\s+(\S+)\s+coverage:`)
)

// testStatusByModule maps go test package lines to the module owning each
// package. A module is "fail" if any package failed, else "ok" if any package
// passed, else "no tests"; modules that produced no lines are "not run".
func testStatusByModule(out string, modules []config.Module) map[string]string {
	rank := map[string]int{"not run": 0, "no tests": 1, "ok": 2, "fail": 3}
	status := make(map[string]string, len(modules))
	for _, m := range modules {
		status[m.Dir] = "not run"
	}
	mark := func(pkg, s string) {
		dir, ok := moduleForPackage(pkg, modules)
		if ok && rank[s] > rank[status[dir]] {
			status[dir] = s
		}
	}
	for _, m := range testLineRe.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		case "ok":
			mark(m[2], "ok")
		case "FAIL":
			mark(m[2], "fail")
		default:
			mark(m[2], "no tests")
		}
	}
	for _, m := range noTestsCoverRe.FindAllStringSubmatch(out, -1) {
		mark(m[1], "no tests")
	}
	return status
}

// moduleForPackage returns the directory of the module with the longest
// path that contains pkg.
func moduleForPackage(pkg string, modules []config.Module) (string, bool) {
	best, dir := -1, ""
	for _, m := range modules {
		if (pkg == m.Path || strings.HasPrefix(pkg, m.Path+"/")) && len(m.Path) > best {
			best, dir = len(m.Path), m.Dir
		}
	}
	return dir, best >= 0
}

func modulesWithStatus(status map[string]string, want string) []string {
	var dirs []string
	for dir, s := range status {
		if s == want {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}
