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
	ImageRef  string  // single-module mode
	Images    []Image // workspace mode: every image built in this run
	Version   string
	ChangeLog string
	Approvers *telegram.Client
}

// images returns what to deploy: one deployment per image, named after it.
func (in DeployInput) images(cfg *config.Config) []Image {
	if cfg.IsWorkspace() {
		return in.Images
	}
	return []Image{{Name: cfg.Service, Ref: in.ImageRef}}
}

func Deploy(ctx context.Context, dag *dagger.Client, cfg *config.Config,
	src *dagger.Directory, in DeployInput) (r report.StageResult) {

	r = report.NewStage("deploy:" + in.Env)
	defer r.Finish()

	env, ok := cfg.Deploy.Environments[in.Env]
	if !ok {
		r.Fail(fmt.Errorf("unknown environment %q", in.Env))
		return r
	}
	if env.KubeContext == "" {
		env.KubeContext = cfg.Deploy.KubeContext
	}
	if env.Namespace == "" {
		env.Namespace = cfg.Deploy.Namespace
	}
	images := in.images(cfg)
	if len(images) == 0 {
		r.Fail(fmt.Errorf("no images were built; nothing to deploy"))
		return r
	}
	for _, img := range images {
		if strings.HasPrefix(img.Ref, "local://") {
			r.Fail(fmt.Errorf("image %s was not pushed; set build.registry to deploy", img.Ref))
			return r
		}
	}

	if cfg.EnvRequiresApproval(in.Env) {
		if in.Approvers == nil {
			r.Fail(fmt.Errorf("env %s requires approval; configure approval.telegram_mcp_url", in.Env))
			return r
		}
		choice, err := in.Approvers.RequestApproval(ctx,
			fmt.Sprintf("Deploy *%s* to *%s*?", in.Version, in.Env),
			buildContext(cfg, in, images),
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

	kubectl := func(args ...string) []string {
		return append([]string{"kubectl", "--context", env.KubeContext, "-n", env.Namespace}, args...)
	}
	kube := dag.Container().From("bitnami/kubectl:1.30").
		WithMountedFile("/root/.kube/config", dag.Host().File(kubeconfig)).
		WithEnvVariable("KUBECONFIG", "/root/.kube/config")
	for _, img := range images {
		kube = kube.WithExec(kubectl("set", "image", "deployment/"+img.Name, img.Name+"="+img.Ref))
	}
	for _, img := range images {
		kube = kube.WithExec(kubectl("rollout", "status", "deployment/"+img.Name, "--timeout=300s"))
	}

	out, err := kube.Stdout(ctx)
	r.Output = out
	if err != nil {
		failExec(&r, "kubectl", err)
		return r
	}

	if in.Approvers != nil && cfg.Notify.OnSuccess {
		_ = in.Approvers.Notify(ctx,
			fmt.Sprintf("✅ Deployed %s to %s", in.Version, in.Env),
			cfg.Service)
	}
	return r
}

func buildContext(cfg *config.Config, in DeployInput, images []Image) string {
	var b strings.Builder
	fmt.Fprintf(&b, "service:  %s\n", cfg.Service)
	fmt.Fprintf(&b, "env:      %s\n", in.Env)
	if len(images) == 1 {
		fmt.Fprintf(&b, "image:    %s\n", images[0].Ref)
	} else {
		fmt.Fprintf(&b, "images:   %d\n", len(images))
		for _, img := range images {
			fmt.Fprintf(&b, "  %s\n", img.Ref)
		}
	}
	fmt.Fprintf(&b, "version:  %s\n", in.Version)
	if in.ChangeLog != "" {
		fmt.Fprintf(&b, "\n--- changes ---\n%s", in.ChangeLog)
	}
	return b.String()
}
