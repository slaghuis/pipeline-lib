package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/hooks"
	"github.com/slaghuis/pipeline-lib/report"
	"github.com/slaghuis/pipeline-lib/stages"
	"github.com/slaghuis/pipeline-lib/telegram"
)

// Options allows per-service customization before Run executes.
type Options struct {
	Hooks *hooks.Registry
}

func DefaultOptions() *Options {
	return &Options{Hooks: defaultHooks}
}

var defaultHooks = hooks.NewRegistry()

// RegisterHook adds a hook to the registry used when Run is called without Options.
// Hook names are "pre-<stage>" and "post-<stage>" where stage is one of
// lint, test, scan, build, integ, deploy.
func RegisterHook(name string, fn hooks.HookFn) {
	defaultHooks.Register(name, fn)
}

var commands = map[string]bool{
	"lint": true, "test": true, "scan": true, "build": true,
	"integ": true, "full": true, "release": true,
}

// Run is the standard entry point. Call from each service's main().
func Run(args []string, opts ...*Options) int {
	o := DefaultOptions()
	if len(opts) > 0 && opts[0] != nil {
		o = opts[0]
	}
	if len(args) < 1 {
		usage()
		return 2
	}
	cmd := args[0]
	if !commands[cmd] {
		usage()
		if cmd == "help" || cmd == "-h" || cmd == "--help" {
			return 0
		}
		return 2
	}

	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	cfgPath := fs.String("config", "pipeline.yaml", "path to pipeline.yaml")
	root := fs.String("root", ".", "project root")
	version := fs.String("version", "", "release version (default: git describe)")
	env := fs.String("env", "staging", "deploy environment")
	changeLog := fs.String("changelog", "", "changelog text (or path to a file) for approvals")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	ctx := context.Background()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}

	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "root: %v\n", err)
		return 1
	}
	if raisedFrom, err := cfg.ResolveGoVersion(rootAbs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: go version: %v; using go_version %s\n", err, cfg.GoVersion)
	} else if raisedFrom != "" {
		fmt.Fprintf(os.Stderr, "warning: go_version %s is older than go.mod requires; using %s\n",
			raisedFrom, cfg.GoVersion)
	}
	if *version == "" {
		*version = gitDescribe(rootAbs)
	}
	if *changeLog != "" {
		if b, err := os.ReadFile(*changeLog); err == nil {
			*changeLog = string(b)
		}
	}

	dag, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dagger connect: %v\n", err)
		return 1
	}
	defer dag.Close()

	src := stages.Source(dag, rootAbs)

	var tg *telegram.Client
	if cfg.Approval.TelegramMCPURL != "" {
		tg = telegram.New(cfg.Approval.TelegramMCPURL)
	}

	rep := &report.Report{
		Pipeline: cmd, Service: cfg.Service,
		Version: *version, StartedAt: time.Now(),
	}
	defer func() {
		rep.DurationS = time.Since(rep.StartedAt).Seconds()
		rep.Success = report.AllSuccess(rep.Stages)
		rep.Emit()
	}()

	hook := func(name string) bool {
		next, err := o.Hooks.Run(ctx, name, dag, src)
		if err != nil {
			s := report.NewStage("hooks:" + name)
			s.Fail(err)
			s.Finish()
			rep.Stages = append(rep.Stages, s)
			return false
		}
		src = next
		return true
	}

	// stage runs pre-/post- hooks around fn; fn must read src lazily so it
	// sees any directory returned by the pre-hook.
	stage := func(name string, fn func() report.StageResult) bool {
		if !hook("pre-" + name) {
			return false
		}
		res := fn()
		rep.Stages = append(rep.Stages, res)
		if !res.OK() {
			return false
		}
		return hook("post-" + name)
	}

	var br stages.BuildResult
	lint := func() bool {
		return stage("lint", func() report.StageResult { return stages.Lint(ctx, dag, cfg, src) })
	}
	test := func() bool {
		return stage("test", func() report.StageResult { return stages.Test(ctx, dag, cfg, src) })
	}
	scan := func() bool {
		return stage("scan", func() report.StageResult { return stages.Scan(ctx, dag, cfg, src) })
	}
	build := func() bool {
		return stage("build", func() report.StageResult {
			br = stages.Build(ctx, dag, cfg, src, *version)
			return br.StageResult
		})
	}
	integ := func() bool {
		return stage("integ", func() report.StageResult { return stages.Integration(ctx, dag, cfg, src) })
	}
	deploy := func() bool {
		return stage("deploy", func() report.StageResult {
			return stages.Deploy(ctx, dag, cfg, src, stages.DeployInput{
				Env: *env, ImageRef: br.ImageRef, Version: *version,
				ChangeLog: *changeLog, Approvers: tg,
			})
		})
	}

	switch cmd {
	case "lint":
		lint()
	case "test":
		test()
	case "scan":
		scan()
	case "build":
		build()
	case "integ":
		integ()
	case "full":
		if !(lint() && test() && scan() && build()) {
			notifyFail(ctx, tg, cfg, rep)
		}
	case "release":
		if !(lint() && test() && scan() && build() && deploy()) {
			notifyFail(ctx, tg, cfg, rep)
		}
	}

	if !report.AllSuccess(rep.Stages) {
		return 1
	}
	return 0
}

func notifyFail(ctx context.Context, tg *telegram.Client, cfg *config.Config, rep *report.Report) {
	if tg == nil || !cfg.Notify.OnFailure {
		return
	}
	var failed []string
	for _, s := range rep.Stages {
		if !s.OK() {
			failed = append(failed, fmt.Sprintf("%s (%s)", s.Name, s.Error))
		}
	}
	_ = tg.Notify(ctx,
		fmt.Sprintf("❌ Pipeline '%s' %s@%s failed at: %s",
			rep.Pipeline, rep.Service, rep.Version, strings.Join(failed, ", ")),
		cfg.Service)
}

func gitDescribe(dir string) string {
	out, err := exec.Command("git", "-C", dir,
		"describe", "--tags", "--always", "--dirty").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "dev"
		}
		return "dev-" + time.Now().Format("20060102-150405")
	}
	return strings.TrimSpace(string(out))
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: pipeline <command> [flags]

commands:
  lint     Run golangci-lint and go vet
  test     Run tests with coverage
  scan     Run govulncheck and trivy
  build    Build binary and container image
  integ    Run integration tests
  full     lint + test + scan + build
  release  full + deploy (requires -env, triggers Telegram approval)

flags:
  -config    path to pipeline.yaml (default: ./pipeline.yaml)
  -root      project root (default: .)
  -env       deploy environment: staging | production
  -version   override version (default: git describe)
  -changelog path or inline changelog text for approvals`)
}
