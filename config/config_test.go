package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGoVersion(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		goMod      string
		want       string
		raisedFrom string
	}{
		{"unset uses go.mod", "", "module x\n\ngo 1.25.0\n", "1.25", ""},
		{"older config is raised", "1.23", "module x\n\ngo 1.25.0\n", "1.25", "1.23"},
		{"newer config is kept", "1.26", "module x\n\ngo 1.25\n", "1.26", ""},
		{"no go directive keeps default", "", "module x\n", defaultGoVersion, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tc.goMod), 0o644); err != nil {
				t.Fatal(err)
			}
			c := &Config{GoVersion: tc.configured}
			raisedFrom, err := c.ResolveGoVersion(dir)
			if err != nil {
				t.Fatal(err)
			}
			if c.GoVersion != tc.want || raisedFrom != tc.raisedFrom {
				t.Fatalf("got (%q, raisedFrom %q), want (%q, raisedFrom %q)",
					c.GoVersion, raisedFrom, tc.want, tc.raisedFrom)
			}
		})
	}
}
