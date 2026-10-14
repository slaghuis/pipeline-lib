package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// DefaultTargetGlob is where workspace mode looks for main packages when
// build.targets is empty. Each matching directory becomes a target named
// after the directory.
const DefaultTargetGlob = "services/*/cmd/*"

// Module is one `use` entry of go.work.
type Module struct {
	Dir  string // slash-separated, relative to the project root
	Path string // module path from the module's go.mod
	Go   string // go directive of the module's go.mod, "" if absent
}

// Target is one binary and image built in workspace mode.
type Target struct {
	Name        string `yaml:"name"`
	MainPackage string `yaml:"main_package"` // default ./services/<name>/cmd/<name>
}

// IsWorkspace reports whether ResolveWorkspace switched on workspace mode.
func (c *Config) IsWorkspace() bool { return c.workspace }

// ResolveWorkspace decides between single-module and workspace mode for the
// project at root. In workspace mode it loads the go.work modules and the
// build targets. In single-module mode it changes nothing.
func (c *Config) ResolveWorkspace(root string) error {
	on := fileExists(filepath.Join(root, "go.work")) && !fileExists(filepath.Join(root, "go.mod"))
	if c.Workspace != nil {
		on = *c.Workspace
	}
	if !on {
		return nil
	}

	workPath := filepath.Join(root, "go.work")
	b, err := os.ReadFile(workPath)
	if err != nil {
		return err
	}
	wf, err := modfile.ParseWork(workPath, b, nil)
	if err != nil {
		return err
	}
	if len(wf.Use) == 0 {
		return fmt.Errorf("%s lists no modules", workPath)
	}

	var modules []Module
	for _, u := range wf.Use {
		dir := path.Clean(filepath.ToSlash(u.Path))
		if filepath.IsAbs(u.Path) || dir == ".." || strings.HasPrefix(dir, "../") {
			return fmt.Errorf("go.work: module %s is outside the project root, which is all the pipeline mounts", u.Path)
		}
		modPath := filepath.Join(root, filepath.FromSlash(dir), "go.mod")
		mb, err := os.ReadFile(modPath)
		if err != nil {
			return fmt.Errorf("go.work: %w", err)
		}
		mf, err := modfile.ParseLax(modPath, mb, nil)
		if err != nil {
			return err
		}
		if mf.Module == nil || mf.Module.Mod.Path == "" {
			return fmt.Errorf("%s has no module directive", modPath)
		}
		m := Module{Dir: dir, Path: mf.Module.Mod.Path}
		if mf.Go != nil {
			m.Go = mf.Go.Version
		}
		modules = append(modules, m)
	}

	c.workspace = true
	c.Modules = modules
	if wf.Go != nil {
		c.workGo = wf.Go.Version
	}
	return c.resolveTargets(root)
}

func (c *Config) resolveTargets(root string) error {
	if len(c.Build.Targets) == 0 {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(DefaultTargetGlob)))
		if err != nil {
			return err
		}
		for _, m := range matches {
			if fi, err := os.Stat(m); err != nil || !fi.IsDir() {
				continue
			}
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return err
			}
			c.Build.Targets = append(c.Build.Targets, Target{
				Name:        filepath.Base(m),
				MainPackage: "./" + filepath.ToSlash(rel),
			})
		}
		return nil
	}

	seen := make(map[string]bool, len(c.Build.Targets))
	for i := range c.Build.Targets {
		t := &c.Build.Targets[i]
		if t.Name == "" {
			return fmt.Errorf("build.targets[%d]: name is required", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("build.targets: duplicate name %q", t.Name)
		}
		seen[t.Name] = true
		if t.MainPackage == "" {
			t.MainPackage = "./services/" + t.Name + "/cmd/" + t.Name
		}
		// go build treats a path without "./" as an import path, not a directory.
		t.MainPackage = "./" + path.Clean(strings.TrimPrefix(filepath.ToSlash(t.MainPackage), "./"))
		if !fileExists(filepath.Join(root, filepath.FromSlash(t.MainPackage))) {
			return fmt.Errorf("build.targets[%s]: %s does not exist", t.Name, t.MainPackage)
		}
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
