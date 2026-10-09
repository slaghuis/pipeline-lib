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
	return &Options{Hooks: hooks.NewRegistry()}
}

// Run is the standard entry point. Call from each service's main().
func Run(args []string, opts *Options) int {
	if opts == nil {
		opts = DefaultOptions()
	}
	if len(args) < 1 {
		usage()
		return 2
	}
	cmd := args[0]

	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	cfgPath := fs.String("config", "pipeline.yaml", "path to pipeline.yaml")
	root := fs.String("root", ".", "project root")
	version := fs.String("version", "", "release version (default: git describe)")
	env := fs.String("env", "staging", "deploy environment")
	changeLog := fs.String("changelog", "", "changelog text for approvals")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	ctx := context.Background()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}

	rootAbs, _ := filepath.Abs(*root)
	if *version == "" {
		*version = gitDescribe(rootAbs)
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

	// run pre-* hooks
	if src2, err := opts.Hooks.Run(ctx, "pre-"+cmd, dag, src); err != nil {
		s := report.NewStage("hooks:pre-" + cmd)
		s.Fail(err)
		s.Finish()
		rep.Stages = append(rep.Stages, s)
		return 1
	} else {
		src = src2
	}

	switch cmd {
	case "lint":
		rep.Stages = append(rep.Stages, stages.Lint(ctx, dag, cfg, src))
	case "test":
		rep.Stages = append(rep.Stages, stages.Test(ctx, dag, cfg, src))
	case "scan":
		rep.Stages = append(rep.Stages, stages.Scan(ctx, dag, cfg, src))
	case "build":
		br := stages.Build(ctx, dag, cfg, src, *version)
		rep.Stages = append(rep.Stages, br.StageResult)
	case "integ":
		rep.Stages = append(rep.Stages, stages.Integration(ctx, dag, cfg, src))
	case "full":
		rep.Stages = append(rep.Stages, stages.Lint(ctx, dag, cfg, src))
		if !lastOK(rep) {
			notifyFail(ctx, tg, cfg, rep)
			break
		}
		rep.Stages = append(rep.Stages, stages.Test(ctx, dag, cfg, src))
		if !lastOK(rep) {
			notifyFail(ctx, tg, cfg, rep)
			break
		}
		rep.Stages = append(rep.Stages, stages.Scan(ctx, dag, cfg, src))
		if !lastOK(rep) {
			notifyFail(ctx, tg, cfg, rep)
			break
		}
		br := stages.Build(ctx, dag, cfg, src, *version)
		rep.Stages = append(rep.Stages, br.StageResult)
		if !br.Success {
			notifyFail(ctx, tg, cfg, rep)
		}
	case "release":
		rep.Stages = append(rep.Stages,
			stages.Lint(ctx, dag, cfg, src),
			stages.Test(ctx, dag, cfg, src),
			stages.Scan(ctx, dag, cfg, src),
		)
		if !report.AllSuccess(rep.Stages) {
			notifyFail(ctx, tg, cfg, rep)
			break
		}
		br := stages.Build(ctx, dag, cfg, src, *version)
		rep.Stages = append(rep.Stages, br.StageResult)
		if !br.Success {
			notifyFail(ctx, tg, cfg, rep)
			break
		}
		rep.Stages = append(rep.Stages, stages.Deploy(ctx, dag, cfg, src, stages.DeployInput{
			Env: *env, ImageRef: br.ImageRef, Version: *version,
			ChangeLog: *changeLog, Approvers: tg,
		}))
	default:
		usage()
		return 2
	}

	if !report.AllSuccess(rep.Stages) {
		return 1
	}
	return 0
}

func lastOK(r *report.Report) bool {
	if len(r.Stages) == 0 {
		return true
	}
	s := r.Stages[len(r.Stages)-1]
	return s.Success || s.Skipped
}

func notifyFail(ctx context.Context, tg *telegram.Client, cfg *config.Config, rep *report.Report) {
	if tg == nil || !cfg.Notify.OnFailure {
		return
	}
	var failed []string
	for _, s := range rep.Stages {
		if !s.Success && !s.Skipped {
			failed = append(failed, s.Name)
		}
	}
	_ = tg.Notify(ctx,
		fmt.Sprintf("❌ Pipeline '%s' failed at: %s", rep.Pipeline, strings.Join(failed, ", ")),
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
  -changelog path or inline changelog text for approvals
`)
}