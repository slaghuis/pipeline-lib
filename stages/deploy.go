package stages

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
	"github.com/slaghuis/pipeline-lib/telegram"
)

type DeployInput struct {
	Env       string
	ImageRef  string
	Version   string
	ChangeLog string
	Approvers *telegram.Client
}

func Deploy(ctx context.Context, dag *dagger.Client, cfg *config.Config,
	src *dagger.Directory, in DeployInput) report.StageResult {

	r := report.NewStage("deploy:" + in.Env)
	defer r.Finish()

	env, ok := cfg.Deploy.Environments[in.Env]
	if !ok {
		r.Fail(fmt.Errorf("unknown environment %q", in.Env))
		return r
	}

	if cfg.EnvRequiresApproval(in.Env) {
		if in.Approvers == nil {
			r.Fail(fmt.Errorf("env %s requires approval; configure approval.telegram_mcp_url", in.Env))
			return r
		}
		choice, err := in.Approvers.RequestApproval(ctx,
			fmt.Sprintf("Deploy *%s* to *%s*?", in.Version, in.Env),
			buildContext(cfg, in),
			cfg.Service+":"+in.Env,
			[]string{"Deploy", "Hold", "Abort"},
			cfg.Approval.TimeoutSeconds,
		)
		if err != nil {
			r.Fail(fmt.Errorf("approval: %w", err))
			return r
		}
		if !strings.EqualFold(choice, "Deploy") {
			r.Fail(fmt.Errorf("not approved (choice=%s)", choice))
			return r
		}
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.ExpandEnv("$HOME/.kube/config")
	}

	kube := dag.Container().From("bitnami/kubectl:1.30").
		WithMountedFile("/root/.kube/config", dag.Host().File(kubeconfig)).
		WithEnvVariable("KUBECONFIG", "/root/.kube/config").
		WithExec([]string{
			"kubectl", "--context", env.KubeContext,
			"-n", env.Namespace,
			"set", "image", "deployment/" + cfg.Service,
			cfg.Service + "=" + in.ImageRef,
		}).
		WithExec([]string{
			"kubectl", "--context", env.KubeContext,
			"-n", env.Namespace,
			"rollout", "status", "deployment/" + cfg.Service, "--timeout=300s",
		})

	out, err := kube.Stdout(ctx)
	r.Output = out
	if err != nil {
		r.Fail(fmt.Errorf("kubectl: %w", err))
		if in.Approvers != nil && cfg.Notify.OnFailure {
			_ = in.Approvers.Notify(ctx,
				fmt.Sprintf("❌ Deploy %s to %s FAILED: %v", in.Version, in.Env, err),
				cfg.Service)
		}
		return r
	}

	if in.Approvers != nil && cfg.Notify.OnSuccess {
		_ = in.Approvers.Notify(ctx,
			fmt.Sprintf("✅ Deployed %s to %s", in.Version, in.Env),
			cfg.Service)
	}
	return r
}

func buildContext(cfg *config.Config, in DeployInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "service:  %s\n", cfg.Service)
	fmt.Fprintf(&b, "env:      %s\n", in.Env)
	fmt.Fprintf(&b, "image:    %s\n", in.ImageRef)
	fmt.Fprintf(&b, "version:  %s\n", in.Version)
	if in.ChangeLog != "" {
		fmt.Fprintf(&b, "\n--- changes ---\n%s", in.ChangeLog)
	}
	return b.String()
}