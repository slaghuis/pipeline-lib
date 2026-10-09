 # Dagger Pipeline Template for Go Services
A Dagger pipeline, written in Go, that implements the full SDLC for a Go microservice with cache-friendly stages, Telegram approval gates, and intelligent integration with the AI stack you've built. Runs identically on your Mac Mini and in CI.
 ## Why Dagger (Not Make/Shell)
 | Concern | Make/Shell | Dagger |
 | ------- | ---------- | ------ |
 | Runs identically locally and in CI | ❌ "works on my machine" | ✅ containerized, deterministic |
 | Agents can invoke stages programmatically | ❌ shell parsing  | ✅ Go functions  | 
 | Caching across stages | ❌ manual | ✅ content-addressed, automatic | 
 | Composable stages | ⚠️ awkward | ✅ Go composition |
 | Secret handling | ❌ env leakage | ✅ typed secrets |
 | Observability | ❌ grep logs | ✅ structured spans  | 

Dagger makes stages first-class Go functions. The agent can call pipeline.Test() as naturally as any other Go code — which matters because agents will drive this pipeline, not just humans.
 ## Pipeline Design
```
┌───────────────────────────────────────────────────────────────┐
│  Pipeline Entry Points (CLI + Agent-callable)                 │
│    pipeline lint        pipeline test      pipeline build     │
│    pipeline scan        pipeline integ     pipeline deploy    │
│    pipeline full        pipeline release                      │
└───────────────────────────────────────────────────────────────┘
                             │
      ┌──────────────────────┼──────────────────────┐
      ▼                      ▼                      ▼
  Fast Gates           Build Stages          Deploy Gates
  (parallel)           (sequential)         (approval-gated)
  ─ lint               ─ build binary       ─ ask Telegram
  ─ vet                ─ build image        ─ run deploy
  ─ unit tests         ─ sign image         ─ smoke tests
  ─ sec scan           ─ push               ─ notify
  ─ govulncheck
```
Stages compose: `full` = `lint && test && scan && build`. `release` = `full && approval-gate && deploy`.

 ## Build & Run
Install Dagger CLI once
```
brew install dagger/tap/dagger
```

```
cd myservice/pipeline
go build -o ../bin/pipeline .

# From the service root:
./bin/pipeline lint
./bin/pipeline test
./bin/pipeline full
./bin/pipeline release -env staging -changelog "feat: rate limiting + jwt refresh"
```
Dagger will:
 1. Spin up a BuildKit engine (first run only, ~10s).
 2. Pull Go/alpine/distroless images (first run only, cached).
 3. Run each stage with content-addressed caching.
Second runs on unchanged code take ~5–10 seconds total.

 ## Agent Integration — Expose as MCP Tools
Agents should drive the pipeline. The simplest wiring: a small MCP server that wraps `./bin/pipeline` as tools. Skeleton:
```
// pipeline-mcp/main.go — reuses mark3labs/mcp-go
tool := mcp.NewTool("pipeline_run",
    mcp.WithDescription("Run a pipeline stage. Returns JSON report."),
    mcp.WithString("command", mcp.Required(),
        mcp.Enum("lint","test","scan","build","integ","full","release")),
    mcp.WithString("env"),
    mcp.WithString("changelog"),
)
s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    args := []string{req.GetString("command","full")}
    if env := req.GetString("env",""); env != "" { args = append(args, "-env", env) }
    if cl := req.GetString("changelog",""); cl != "" { args = append(args, "-changelog", cl) }
    out, err := exec.CommandContext(ctx, "./bin/pipeline", args...).Output()
    if err != nil {
        return mcp.NewToolResultError(string(out)), nil
    }
    return mcp.NewToolResultText(string(out)), nil // JSON goes straight to agent
})
```
Then an agent can issue:
```"Run the full pipeline, then if it passes, release to staging with changelog from the last 5 commits."```
...and the agent calls `pipeline_run(command="full")`, parses the JSON, calls `pipeline_run(command="release", env="staging", changelog="...")`, waits for your Telegram tap, and reports back.

## Local Kubernetes Setup
For the `deploy` stage to work locally:
```
k3d cluster create dev --servers 1 --agents 0 -p "8080:30080@loadbalancer"
kubectl config use-context k3d-dev

# Namespace + minimal deployment (one-time)
kubectl create ns myservice-staging
kubectl -n myservice-staging apply -f deploy/k8s/base-deployment.yaml
```
Your `pipeline.yaml` already points at `k3d-dev` for staging.

 ## The Full Physical Layout on Your Mac
Here's the recommended directory layout, with everything you've built so far:
```
~/code/
│
├── ai-factory/                      # The shared AI infrastructure
│   │                                # (one git repo, or four — your choice)
│   │
│   ├── indexer/                     # Service 1: Qdrant indexer
│   │   ├── go.mod
│   │   ├── cmd/indexer/
│   │   ├── internal/
│   │   └── config.yaml
│   │
│   ├── mcp-code/                    # Service 2: Code-search MCP
│   │   ├── go.mod
│   │   ├── cmd/mcp-code/
│   │   ├── internal/
│   │   └── config.yaml
│   │
│   ├── cache-proxy/                 # Service 3: LiteLLM cache proxy
│   │   ├── go.mod
│   │   ├── cmd/cache-proxy/
│   │   ├── internal/
│   │   └── config.yaml
│   │
│   ├── telegram-mcp/                # Service 4: Telegram MCP
│   │   ├── go.mod
│   │   ├── cmd/telegram-mcp/
│   │   ├── internal/
│   │   └── config.yaml
│   │
│   ├── pipeline-mcp/                # Service 5: Pipeline MCP (next build)
│   │   ├── go.mod
│   │   ├── cmd/pipeline-mcp/
│   │   ├── internal/
│   │   └── config.yaml
│   │
│   └── pipeline-lib/                # SHARED pipeline library
│       ├── go.mod                   # module: github.com/you/pipeline-lib
│       ├── stages/                  # lint.go test.go build.go deploy.go
│       ├── config/
│       ├── report/
│       └── telegram/
│
└── services/                        # Your actual Go services
    │
    ├── myservice/                   # A real microservice
    │   ├── go.mod                   # module: github.com/you/myservice
    │   ├── main.go
    │   ├── internal/
    │   ├── deploy/
    │   │   └── k8s/
    │   ├── pipeline.yaml            # ← per-service pipeline config
    │   └── pipeline/                # ← tiny per-service pipeline binary
    │       ├── go.mod               # module: github.com/you/myservice/pipeline
    │       └── main.go              # ~30 lines, just calls pipeline-lib
    │
    ├── another-service/
    │   ├── go.mod
    │   ├── pipeline.yaml
    │   └── pipeline/
    │       └── main.go
    │
    └── a-third-service/
        └── ...
```

 ## Minimal Per-Service Pipeline Wrapper

 ### Service Repo Layout
```
~/code/services/myservice/
├── go.mod                           # module: github.com/slaghuis/myservice
├── main.go
├── internal/
├── deploy/
│   └── k8s/
├── pipeline.yaml                    # service's pipeline config
└── pipeline/
    ├── go.mod                       # module: github.com/slaghuis/myservice/pipeline
    └── main.go                      # ~10 lines
```
 ### Part Two `services/myservice/pipeline/go.mod`
```
module github.com/slaghuis/myservice/pipeline

go 1.23

require github.com/slaghuis/pipeline-lib v0.0.0

// During development, point at local pipeline-lib:
replace github.com/slaghuis/pipeline-lib => ../../../ai-factory/pipeline-lib
```
Once you push `pipeline-lib` to GitHub and tag it, drop the `replace` directive and `go get` the tagged version.

### Part 3 Here's what `~/code/services/myservice/pipeline/main.go becomes` — essentially a thin wrapper:
```
package main

import (
    "os"
    "github.com/you/pipeline-lib/cli"
)

func main() {
    // Everything (flags, dagger connect, stage dispatch) lives in pipeline-lib.
    // This file exists only to produce a per-service binary.
    os.Exit(cli.Run(os.Args[1:]))
}
```
That's it. Every service has an identical main.go. Variation lives in pipeline.yaml.
If a service needs a custom stage (e.g., "run my proto codegen before build"), it can override:
```
package main

import (
    "context"
    "github.com/you/pipeline-lib/cli"
    "github.com/you/pipeline-lib/stages"
    "dagger.io/dagger"
)

func main() {
    cli.RegisterHook("pre-build", func(ctx context.Context, dag *dagger.Client, src *dagger.Directory) error {
        _, err := stages.Base(ctx, dag, cfg, src).
            WithExec([]string{"go", "generate", "./..."}).
            Sync(ctx)
        return err
    })
    os.Exit(cli.Run(os.Args[1:]))
}
```
### Part 4 `services/myservice/pipeline.yaml`
```
service: myservice
main_package: ./cmd/myservice
go_version: "1.23"

lint:
  enabled: true

test:
  race: true
  coverage: true
  coverage_min: 70

scan:
  govulncheck: true
  trivy: true

build:
  image: myservice
  registry: ${REGISTRY}

integration:
  enabled: false

deploy:
  manifests: ./deploy/k8s
  environments:
    staging:
      kube_context: k3d-dev
      namespace: myservice-staging
    production:
      kube_context: prod
      namespace: myservice

approval:
  telegram_mcp_url: http://localhost:8765
  required_for: [staging, production]
  timeout_seconds: 3600

notify:
  on_failure: true
  on_success: true
```
 ### Part 5 Build the Per-Service Binary
```
cd ~/code/services/myservice/pipeline
go build -o ../bin/pipeline .

# From the service root:
cd ..
./bin/pipeline test
./bin/pipeline release -env staging
```



 ## Service Registry — How Pipeline-MCP Finds Services
A simple YAML file tells the pipeline-MCP where your services live:
```
# ~/.config/ai-factory/services.yaml
services:
  - name: myservice
    path: ~/code/services/myservice
    pipeline_binary: ./bin/pipeline     # relative to path
    default_env: staging

  - name: another-service
    path: ~/code/services/another-service
    pipeline_binary: ./bin/pipeline

  - name: payments
    path: ~/code/services/payments
    pipeline_binary: ./bin/pipeline
```
The pipeline-MCP server reads this on startup (and on SIGHUP), and agents can call:
 - `pipeline_list_services()` → returns the list
 - `pipeline_run(service="myservice", command="test")` → shells out to that service's binary
 - `pipeline_run(service="myservice", command="release", env="staging")`

 ## Operational Notes
 - **First run is slow (~2 min)** — Dagger pulls base images and builds caches. Second run on no code changes: ~5 s.
 - **Cache volumes persist** across runs on the same machine. In CI, use Dagger Cloud or BuildKit cache mounts.
 - **Secrets**: never put API keys in pipeline.yaml. Use dagger.Secret for registry auth, kubeconfig, etc. Example: dag.SetSecret("REGISTRY_PASSWORD", os.Getenv("REGISTRY_PASSWORD")).
 - **Multi-arch publishing** for production — use docker buildx outside Dagger, or Dagger Cloud which handles it natively. The template defaults to single-arch for local speed.
 - **Parallelism**: Dagger will automatically run independent stages concurrently. lint, test, and scan all build from the same base so they share cache but can run in parallel. To parallelize explicitly:
```
var wg sync.WaitGroup
results := make(chan report.StageResult, 3)
for _, fn := range []func() report.StageResult{
    func() report.StageResult { return stages.Lint(ctx, dag, cfg, src) },
    func() report.StageResult { return stages.Test(ctx, dag, cfg, src) },
    func() report.StageResult { return stages.Scan(ctx, dag, cfg, src) },
} {
    wg.Add(1)
    go func(f func() report.StageResult) { defer wg.Done(); results <- f() }(fn)
}
wg.Wait(); close(results)
for r := range results { rep.Stages = append(rep.Stages, r) }
```
 - **CI parity**: this exact same binary runs in GitHub Actions / Gitea with no changes:
```
- uses: dagger/dagger-for-github@v6
  with:
    version: "latest"
    verb: "call"
    args: "full"
```

