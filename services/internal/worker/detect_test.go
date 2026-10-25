package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectNode(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"package.json":   `{"scripts":{"build":"vite build","start":"node server.js"},"engines":{"node":">=20"}}`,
		"pnpm-lock.yaml": "",
	})
	plan, err := DetectStack(dir, 8080, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM node:20-alpine", "pnpm install --frozen-lockfile", "RUN pnpm run build", "PORT=8080", `CMD ["pnpm","run","start"]`} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("missing %q in\n%s", want, plan.Dockerfile)
		}
	}
	if plan.Stack != "Node.js 20 (pnpm)" {
		t.Errorf("stack %q", plan.Stack)
	}
	// NODE_ENV=production must come after the build, or devDependencies needed to build are skipped.
	if strings.Index(plan.Dockerfile, "NODE_ENV") < strings.Index(plan.Dockerfile, "run build") {
		t.Error("NODE_ENV set before the build")
	}
}

func TestDetectNeedsAWayToStart(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"package.json": `{"scripts":{"build":"tsc"}}`})
	if _, err := DetectStack(dir, 3000, false); err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("expected a start-script error, got %v", err)
	}
	if _, err := DetectStack(dir, 3000, true); err != nil {
		t.Fatalf("a start command should be enough: %v", err)
	}
}

func TestDetectOthers(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"python": {map[string]string{"requirements.txt": "flask", "Procfile": "web: gunicorn app:app\n"}, `CMD ["sh","-c","gunicorn app:app"]`},
		"go":     {map[string]string{"go.mod": "module x"}, "go build -o /out/app ."},
		"static": {map[string]string{"index.html": "<h1>hi</h1>"}, "listen 8081;"},
	}
	for name, c := range cases {
		dir := t.TempDir()
		write(t, dir, c.files)
		plan, err := DetectStack(dir, 8081, false)
		if err != nil || !strings.Contains(plan.Dockerfile, c.want) {
			t.Errorf("%s: %v\n%v", name, err, plan)
		}
	}
	if _, err := DetectStack(t.TempDir(), 80, false); err == nil {
		t.Error("an empty folder can't be detected")
	}
}
