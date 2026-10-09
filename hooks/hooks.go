package hooks

//Standard hook points we support: pre-build, pre-test, pre-deploy, post-build.

import (
	"context"

	"dagger.io/dagger"
)

// HookFn is invoked at a named extension point with the base source directory.
// It returns a (possibly modified) directory or an error.
type HookFn func(ctx context.Context, dag *dagger.Client, src *dagger.Directory) (*dagger.Directory, error)

type Registry struct {
	fns map[string][]HookFn
}

func NewRegistry() *Registry {
	return &Registry{fns: make(map[string][]HookFn)}
}

func (r *Registry) Register(name string, fn HookFn) {
	r.fns[name] = append(r.fns[name], fn)
}

func (r *Registry) Run(ctx context.Context, name string, dag *dagger.Client, src *dagger.Directory) (*dagger.Directory, error) {
	for _, fn := range r.fns[name] {
		next, err := fn(ctx, dag, src)
		if err != nil {
			return src, err
		}
		if next != nil {
			src = next
		}
	}
	return src, nil
}