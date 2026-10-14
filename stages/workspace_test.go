package stages

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/slaghuis/pipeline-lib/config"
	"github.com/slaghuis/pipeline-lib/report"
)

var testModules = []config.Module{
	{Dir: "libs/a", Path: "example.com/ws/libs/a"},
	{Dir: "libs/ab", Path: "example.com/ws/libs/ab"},
	{Dir: "services/svc", Path: "example.com/ws/services/svc"},
	{Dir: "services/svc/nested", Path: "example.com/ws/services/svc/nested"},
	{Dir: "tools/idle", Path: "example.com/ws/tools/idle"},
}

func TestTestStatusByModule(t *testing.T) {
	out := strings.Join([]string{
		"ok  \texample.com/ws/libs/a\t0.012s\tcoverage: 81.0% of statements",
		"ok  \texample.com/ws/libs/a/inner\t0.010s",
		"--- FAIL: TestX (0.00s)",
		"FAIL\texample.com/ws/libs/ab [build failed]",
		"?   \texample.com/ws/services/svc\t[no test files]",
		"\texample.com/ws/services/svc/nested/cmd\t\tcoverage: 0.0% of statements",
		"FAIL",
	}, "\n")
	got := testStatusByModule(out, testModules)
	want := map[string]string{
		"libs/a":              "ok",
		"libs/ab":             "fail",
		"services/svc":        "no tests",
		"services/svc/nested": "no tests",
		"tools/idle":          "not run",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	if failed := modulesWithStatus(got, "fail"); !reflect.DeepEqual(failed, []string{"libs/ab"}) {
		t.Fatalf("failed = %v", failed)
	}
}

func TestModuleForPackagePrefersLongestPath(t *testing.T) {
	cases := map[string]string{
		"example.com/ws/services/svc/nested/x": "services/svc/nested",
		"example.com/ws/services/svc/x":        "services/svc",
		"example.com/ws/libs/ab":               "libs/ab",
		"example.com/ws/libs/a":                "libs/a",
	}
	for pkg, want := range cases {
		if got, ok := moduleForPackage(pkg, testModules); !ok || got != want {
			t.Errorf("%s: got (%q, %v), want %q", pkg, got, ok, want)
		}
	}
	if _, ok := moduleForPackage("example.com/other", testModules); ok {
		t.Error("package outside every module must not match")
	}
}

func TestRecordModules(t *testing.T) {
	r := report.NewStage("lint")
	recordModules(&r, "golangci_lint", []moduleResult{
		{Dir: "libs/a"},
		{Dir: "libs/ab", Output: "x.go:1: unused", Err: errors.New("exit 1")},
		{Dir: "services/svc", Err: errors.New("exit 2")},
	})
	r.Finish()

	if r.Success {
		t.Fatal("stage must fail when a module fails")
	}
	want := map[string]string{"libs/a": "ok", "libs/ab": "fail", "services/svc": "fail"}
	if got := r.Metrics["golangci_lint"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("metrics = %v", got)
	}
	if !strings.Contains(r.Error, "2 module(s): libs/ab, services/svc") {
		t.Fatalf("error = %q", r.Error)
	}
	if !strings.Contains(r.Output, "== golangci_lint: libs/ab ==\nx.go:1: unused") || strings.Contains(r.Output, "libs/a ==") {
		t.Fatalf("output must list failed modules only:\n%s", r.Output)
	}
}

func TestParallelKeepsOrder(t *testing.T) {
	in := make([]int, 50)
	for i := range in {
		in[i] = i
	}
	got := parallel(in, func(i int) int { return i * i })
	for i, v := range got {
		if v != i*i {
			t.Fatalf("got[%d] = %d", i, v)
		}
	}
}
