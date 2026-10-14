package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Service     string `yaml:"service"`
	MainPackage string `yaml:"main_package"`
	GoVersion   string `yaml:"go_version"`
	// Workspace forces workspace mode on or off. Unset means: on when the
	// root has a go.work and no go.mod.
	Workspace *bool `yaml:"workspace"`

	// Modules lists the go.work modules; filled by ResolveWorkspace and
	// empty in single-module mode.
	Modules   []Module `yaml:"-"`
	workspace bool
	workGo    string

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
		Govulncheck bool   `yaml:"govulncheck"`
		Trivy       bool   `yaml:"trivy"`
		FailOn      string `yaml:"fail_on"` // "critical" | "high" | "medium"
	} `yaml:"scan"`

	Build struct {
		Image     string            `yaml:"image"`
		Registry  string            `yaml:"registry"`
		Platforms []string          `yaml:"platforms"`
		LDFlags   string            `yaml:"ldflags"`
		BuildArgs map[string]string `yaml:"build_args"`
		// Targets are the binaries and images built in workspace mode.
		// Empty means discover DefaultTargetGlob.
		Targets []Target `yaml:"targets"`
	} `yaml:"build"`

	Integration struct {
		Enabled     bool   `yaml:"enabled"`
		ComposeFile string `yaml:"compose_file"`
		TestCommand string `yaml:"test_command"`
	} `yaml:"integration"`

	Deploy struct {
		KubeContext  string         `yaml:"kube_context"`
		Namespace    string         `yaml:"namespace"`
		Manifests    string         `yaml:"manifests"`
		Environments map[string]Env `yaml:"environments"`
	} `yaml:"deploy"`

	Approval struct {
		TelegramMCPURL string   `yaml:"telegram_mcp_url"`
		RequiredFor    []string `yaml:"required_for"` // ["staging","production"]
		TimeoutSeconds int      `yaml:"timeout_seconds"`
	} `yaml:"approval"`

	Notify struct {
		OnFailure bool `yaml:"on_failure"`
		OnSuccess bool `yaml:"on_success"`
	} `yaml:"notify"`
}

type Env struct {
	KubeContext string   `yaml:"kube_context"`
	Namespace   string   `yaml:"namespace"`
	ValuesFile  string   `yaml:"values_file"`
	ExtraArgs   []string `yaml:"extra_args"`
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
	if c.Service == "" {
		return nil, fmt.Errorf("%s: service is required", path)
	}
	applyDefaults(&c)
	// env expansion
	c.Approval.TelegramMCPURL = os.ExpandEnv(c.Approval.TelegramMCPURL)
	c.Build.Registry = os.ExpandEnv(c.Build.Registry)
	return &c, nil
}

func applyDefaults(c *Config) {
	if c.MainPackage == "" {
		c.MainPackage = "."
	}
	if c.Lint.GolangCILintVersion == "" {
		c.Lint.GolangCILintVersion = "v2.14.0"
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
	if c.Build.Image == "" {
		c.Build.Image = c.Service
	}
}

// GoPackages returns the package patterns the Go toolchain stages run over:
// every workspace module in workspace mode, "./..." otherwise. Relative
// wildcards such as "./..." do not cross module boundaries in a workspace.
func (c *Config) GoPackages() []string {
	if !c.workspace {
		return []string{"./..."}
	}
	pkgs := make([]string, len(c.Modules))
	for i, m := range c.Modules {
		pkgs[i] = m.Path + "/..."
	}
	return pkgs
}

// IntegrationCommand returns integration.test_command split into argv, or
// the default integration test run over GoPackages.
func (c *Config) IntegrationCommand() []string {
	if c.Integration.TestCommand != "" {
		return strings.Fields(c.Integration.TestCommand)
	}
	return append([]string{"go", "test", "-tags", "integration"}, c.GoPackages()...)
}

const defaultGoVersion = "1.23"

var goDirective = regexp.MustCompile(`(?m)^go\s+(\d+)\.(\d+)`)

// ResolveGoVersion raises GoVersion to the major.minor required by the go.mod in
// root, or in workspace mode to the highest go directive across go.work and
// its modules. The official golang images set GOTOOLCHAIN=local, so an older
// image cannot download the toolchain the module asks for and every stage
// fails. Call ResolveWorkspace first.
// It returns the configured version when it had to be raised, "" otherwise.
func (c *Config) ResolveGoVersion(root string) (raisedFrom string, err error) {
	configured := c.GoVersion
	if c.GoVersion == "" {
		c.GoVersion = defaultGoVersion
	}
	required, err := c.requiredGoVersion(root)
	if err != nil || required == "" {
		return "", err
	}
	if configured == "" || versionLess(configured, required) {
		c.GoVersion = required
		if configured != "" {
			return configured, nil
		}
	}
	return "", nil
}

func (c *Config) requiredGoVersion(root string) (string, error) {
	if c.workspace {
		required := majorMinorString(c.workGo)
		for _, m := range c.Modules {
			if v := majorMinorString(m.Go); v != "" && (required == "" || versionLess(required, v)) {
				required = v
			}
		}
		return required, nil
	}
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	m := goDirective.FindSubmatch(b)
	if m == nil {
		return "", nil
	}
	return string(m[1]) + "." + string(m[2]), nil
}

// majorMinorString trims a Go version such as "1.22.3" to "1.22"; it returns
// "" when v has no minor part.
func majorMinorString(v string) string {
	parts := strings.SplitN(strings.TrimPrefix(v, "go"), ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// versionLess compares the major.minor parts of two Go versions.
func versionLess(a, b string) bool {
	pa, pb := majorMinor(a), majorMinor(b)
	if pa[0] != pb[0] {
		return pa[0] < pb[0]
	}
	return pa[1] < pb[1]
}

func majorMinor(v string) [2]int {
	var out [2]int
	parts := strings.SplitN(strings.TrimPrefix(v, "go"), ".", 3)
	for i := 0; i < 2 && i < len(parts); i++ {
		out[i], _ = strconv.Atoi(parts[i])
	}
	return out
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
