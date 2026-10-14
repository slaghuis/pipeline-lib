package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTree creates files under a temp dir; a key ending in "/" creates a directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func workspaceTree() map[string]string {
	return map[string]string{
		// CRLF line endings, as checked out on some machines.
		"go.work":                           "go 1.22\r\n\r\nuse (\r\n    ./libs/a\r\n    ./services/svc-one\r\n    ./services/svc-two\r\n)\r\n",
		"libs/a/go.mod":                     "module example.com/ws/libs/a\r\n\r\ngo 1.22\r\n",
		"services/svc-one/go.mod":           "module example.com/ws/services/svc-one\n\ngo 1.23.4\n",
		"services/svc-two/go.mod":           "module example.com/ws/services/svc-two\n\ngo 1.21\n",
		"services/svc-one/cmd/svc-one/":     "",
		"services/svc-two/cmd/svc-two/":     "",
		"services/svc-two/cmd/not-a-dir.go": "package main\n",
	}
}

func TestResolveWorkspaceDetectsModulesAndTargets(t *testing.T) {
	root := writeTree(t, workspaceTree())
	c := &Config{Service: "ws"}
	if err := c.ResolveWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if !c.IsWorkspace() {
		t.Fatal("workspace mode not detected")
	}
	wantModules := []Module{
		{Dir: "libs/a", Path: "example.com/ws/libs/a", Go: "1.22"},
		{Dir: "services/svc-one", Path: "example.com/ws/services/svc-one", Go: "1.23.4"},
		{Dir: "services/svc-two", Path: "example.com/ws/services/svc-two", Go: "1.21"},
	}
	if !reflect.DeepEqual(c.Modules, wantModules) {
		t.Fatalf("modules:\n got %+v\nwant %+v", c.Modules, wantModules)
	}
	wantTargets := []Target{
		{Name: "svc-one", MainPackage: "./services/svc-one/cmd/svc-one"},
		{Name: "svc-two", MainPackage: "./services/svc-two/cmd/svc-two"},
	}
	if !reflect.DeepEqual(c.Build.Targets, wantTargets) {
		t.Fatalf("targets:\n got %+v\nwant %+v", c.Build.Targets, wantTargets)
	}
	wantPkgs := []string{
		"example.com/ws/libs/a/...",
		"example.com/ws/services/svc-one/...",
		"example.com/ws/services/svc-two/...",
	}
	if got := c.GoPackages(); !reflect.DeepEqual(got, wantPkgs) {
		t.Fatalf("GoPackages: got %v, want %v", got, wantPkgs)
	}
}

func TestResolveWorkspaceGoVersionIsHighest(t *testing.T) {
	root := writeTree(t, workspaceTree())
	c := &Config{Service: "ws"}
	if err := c.ResolveWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveGoVersion(root); err != nil {
		t.Fatal(err)
	}
	if c.GoVersion != "1.23" {
		t.Fatalf("GoVersion = %q, want 1.23", c.GoVersion)
	}
}

func TestResolveWorkspaceSingleModuleUnchanged(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":  "module example.com/single\n\ngo 1.23\n",
		"go.work": "go 1.23\n\nuse .\n",
	})
	c := &Config{Service: "single"}
	if err := c.ResolveWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if c.IsWorkspace() || c.Modules != nil {
		t.Fatal("a root go.mod must keep single-module mode")
	}
	if got := c.GoPackages(); !reflect.DeepEqual(got, []string{"./..."}) {
		t.Fatalf("GoPackages = %v", got)
	}
}

func TestResolveWorkspaceOverride(t *testing.T) {
	off := false
	root := writeTree(t, workspaceTree())
	c := &Config{Service: "ws", Workspace: &off}
	if err := c.ResolveWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if c.IsWorkspace() {
		t.Fatal("workspace: false must disable detection")
	}
}

func TestResolveWorkspaceExplicitTargets(t *testing.T) {
	root := writeTree(t, workspaceTree())
	c := &Config{Service: "ws"}
	c.Build.Targets = []Target{{Name: "svc-one"}, {Name: "two", MainPackage: "services/svc-two/cmd/svc-two/"}}
	if err := c.ResolveWorkspace(root); err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{Name: "svc-one", MainPackage: "./services/svc-one/cmd/svc-one"},
		{Name: "two", MainPackage: "./services/svc-two/cmd/svc-two"},
	}
	if !reflect.DeepEqual(c.Build.Targets, want) {
		t.Fatalf("targets:\n got %+v\nwant %+v", c.Build.Targets, want)
	}
}

func TestResolveWorkspaceErrors(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		targets []Target
		wantErr string
	}{
		{"module outside root", map[string]string{"go.work": "go 1.22\nuse ../elsewhere\n"}, nil, "outside the project root"},
		{"missing go.mod", map[string]string{"go.work": "go 1.22\nuse ./gone\n"}, nil, "go.work:"},
		{"no modules", map[string]string{"go.work": "go 1.22\n"}, nil, "lists no modules"},
		{"missing target dir", workspaceTree(), []Target{{Name: "nope"}}, "does not exist"},
		{"duplicate target", workspaceTree(), []Target{{Name: "svc-one"}, {Name: "svc-one"}}, "duplicate"},
		{"unnamed target", workspaceTree(), []Target{{MainPackage: "./services/svc-one/cmd/svc-one"}}, "name is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTree(t, tc.files)
			c := &Config{Service: "ws"}
			c.Build.Targets = tc.targets
			err := c.ResolveWorkspace(root)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestIntegrationCommand(t *testing.T) {
	root := writeTree(t, workspaceTree())
	c := &Config{Service: "ws"}
	if err := c.ResolveWorkspace(root); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(c.IntegrationCommand(), " ")
	want := "go test -tags integration example.com/ws/libs/a/... example.com/ws/services/svc-one/... example.com/ws/services/svc-two/..."
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	c.Integration.TestCommand = "make itest"
	if got := c.IntegrationCommand(); !reflect.DeepEqual(got, []string{"make", "itest"}) {
		t.Fatalf("explicit test_command: got %v", got)
	}
}
