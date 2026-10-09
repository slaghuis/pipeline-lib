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

