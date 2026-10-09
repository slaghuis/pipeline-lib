package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Service     string   `yaml:"service"`
	MainPackage string   `yaml:"main_package"`
	GoVersion   string   `yaml:"go_version"`

	Lint struct {
		GolangCILintVersion string   `yaml:"golangci_lint_version"`
		Timeout             string   `yaml:"timeout"`
		Enabled             bool     `yaml:"enabled"`
		SkipDirs            []string `yaml:"skip_dirs"`
	} `yaml:"lint"`

	Test struct {
		Race        bool     `yaml:"race"`
		Coverage    bool     `yaml:"coverage"`
		CoverageMin float64  `yaml:"coverage_min"`
		Tags        []string `yaml:"tags"`
		Timeout     string   `yaml:"timeout"`
	} `yaml:"test"`

	Scan struct {
		Govulncheck bool `yaml:"govulncheck"`
		Trivy       bool `yaml:"trivy"`
		FailOn      string `yaml:"fail_on"` // "critical" | "high" | "medium"
	} `yaml:"scan"`

	Build struct {
		Image       string            `yaml:"image"`
		Registry    string            `yaml:"registry"`
		Platforms   []string          `yaml:"platforms"`
		LDFlags     string            `yaml:"ldflags"`
		BuildArgs   map[string]string `yaml:"build_args"`
	} `yaml:"build"`

	Integration struct {
		Enabled      bool     `yaml:"enabled"`
		ComposeFile  string   `yaml:"compose_file"`
		TestCommand  string   `yaml:"test_command"`
	} `yaml:"integration"`

	Deploy struct {
		KubeContext string            `yaml:"kube_context"`
		Namespace   string            `yaml:"namespace"`
		Manifests   string            `yaml:"manifests"`
		Environments map[string]Env   `yaml:"environments"`
	} `yaml:"deploy"`

	Approval struct {
		TelegramMCPURL string `yaml:"telegram_mcp_url"`
		RequiredFor    []string `yaml:"required_for"` // ["staging","production"]
		TimeoutSeconds int    `yaml:"timeout_seconds"`
	} `yaml:"approval"`

	Notify struct {
		OnFailure bool `yaml:"on_failure"`
		OnSuccess bool `yaml:"on_success"`
	} `yaml:"notify"`
}

type Env struct {
	KubeContext string            `yaml:"kube_context"`
	Namespace   string            `yaml:"namespace"`
	ValuesFile  string            `yaml:"values_file"`
	ExtraArgs   []string          `yaml:"extra_args"`
}

func Load(path string) (*Config, error) {
	if !filepath.IsAbs(path) {
		wd, _ := os.Getwd()
		path = filepath.Join(wd, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	applyDefaults(&c)
	// env expansion
	c.Approval.TelegramMCPURL = os.ExpandEnv(c.Approval.TelegramMCPURL)
	c.Build.Registry = os.ExpandEnv(c.Build.Registry)
	return &c, nil
}

func applyDefaults(c *Config) {
	if c.GoVersion == "" {
		c.GoVersion = "1.23"
	}
	if c.MainPackage == "" {
		c.MainPackage = "."
	}
	if c.Lint.GolangCILintVersion == "" {
		c.Lint.GolangCILintVersion = "v1.61.0"
	}
	if c.Lint.Timeout == "" {
		c.Lint.Timeout = "5m"
	}
	if c.Test.Timeout == "" {
		c.Test.Timeout = "10m"
	}
	if c.Approval.TimeoutSeconds == 0 {
		c.Approval.TimeoutSeconds = 3600
	}
	if c.Scan.FailOn == "" {
		c.Scan.FailOn = "high"
	}
	if len(c.Build.Platforms) == 0 {
		c.Build.Platforms = []string{"linux/amd64", "linux/arm64"}
	}
}

func (e Env) IsSet() bool {
	return e.KubeContext != "" || e.Namespace != ""
}

func (c *Config) EnvRequiresApproval(env string) bool {
	for _, e := range c.Approval.RequiredFor {
		if strings.EqualFold(e, env) {
			return true
		}
	}
	return false
}