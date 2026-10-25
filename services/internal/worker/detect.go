package worker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// BuildPlan says how a build context becomes an image when it has no Dockerfile.
type BuildPlan struct {
	Stack      string `json:"stack"`   // e.g. "Node.js (npm)"
	Summary    string `json:"summary"` // install → build → start, for people
	Dockerfile string `json:"dockerfile"`
}

// GeneratedDockerfile is written into the build context when Ignition detects the stack.
const GeneratedDockerfile = ".ignition.Dockerfile"

var (
	nodeMajorRE = regexp.MustCompile(`(\d{2})`)
	procfileRE  = regexp.MustCompile(`(?m)^web:\s*(.+)$`)
)

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// jsonCmd renders a command for an exec-form CMD.
func jsonCmd(args ...string) string {
	b, _ := json.Marshal(args)
	return string(b)
}

// DetectStack looks at a build context without a Dockerfile and writes one for the stacks it recognises.
// port is the port the resource listens on (0 when unset).
// hasCommand is true when the resource has its own start command, which replaces CMD at runtime.
func DetectStack(dir string, port int, hasCommand bool) (*BuildPlan, error) {
	if port == 0 {
		port = 3000
	}
	switch {
	case exists(dir, "package.json"):
		return detectNode(dir, port, hasCommand)
	case exists(dir, "requirements.txt") || exists(dir, "pyproject.toml"):
		return detectPython(dir, port, hasCommand)
	case exists(dir, "go.mod"):
		return &BuildPlan{Stack: "Go", Summary: "go build → run the binary", Dockerfile: fmt.Sprintf(`FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app .

FROM alpine:3.20
COPY --from=build /out/app /usr/local/bin/app
ENV PORT=%d
EXPOSE %d
CMD ["app"]
`, port, port)}, nil
	case exists(dir, "index.html"):
		return &BuildPlan{Stack: "Static site", Summary: fmt.Sprintf("served by nginx on port %d", port), Dockerfile: fmt.Sprintf(`FROM nginx:1.27-alpine
COPY . /usr/share/nginx/html
RUN rm -f /usr/share/nginx/html/%s && printf 'server {\n  listen %d;\n  root /usr/share/nginx/html;\n  location / { try_files $uri $uri/ /index.html; }\n}\n' > /etc/nginx/conf.d/default.conf
EXPOSE %d
`, GeneratedDockerfile, port, port)}, nil
	}
	return nil, fmt.Errorf("there's no Dockerfile, and Ignition couldn't tell what this is (it looks for package.json, requirements.txt, pyproject.toml, go.mod or index.html). Add a Dockerfile, or point Build context at the app's folder")
}

func detectNode(dir string, port int, hasCommand bool) (*BuildPlan, error) {
	var pkg struct {
		Main    string            `json:"main"`
		Scripts map[string]string `json:"scripts"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || json.Unmarshal(raw, &pkg) != nil {
		return nil, fmt.Errorf("package.json can't be read as JSON")
	}
	node := "22"
	if m := nodeMajorRE.FindString(pkg.Engines.Node); m != "" {
		if v, _ := strconv.Atoi(m); v >= 18 && v <= 24 {
			node = m
		}
	}
	pm, install, run := "npm", "npm install", "npm run"
	lockCopy := "package*.json"
	switch {
	case exists(dir, "pnpm-lock.yaml"):
		pm, install, run, lockCopy = "pnpm", "corepack enable && pnpm install --frozen-lockfile", "pnpm run", "package.json pnpm-lock.yaml"
	case exists(dir, "yarn.lock"):
		pm, install, run, lockCopy = "yarn", "corepack enable && yarn install --frozen-lockfile", "yarn run", "package.json yarn.lock"
	case exists(dir, "package-lock.json"):
		install = "npm ci"
	}
	var start []string
	switch {
	case pkg.Scripts["start"] != "":
		start = strings.Fields(run + " start")
	case pkg.Main != "":
		start = []string{"node", pkg.Main}
	case exists(dir, "server.js"):
		start = []string{"node", "server.js"}
	case exists(dir, "index.js"):
		start = []string{"node", "index.js"}
	case hasCommand:
		start = []string{"sh", "-c", "echo 'Set a start command' >&2; exit 1"} // replaced by the resource's start command
	default:
		return nil, fmt.Errorf("package.json has no \"start\" script, so Ignition doesn't know how to run it. Add a start script, set a Start command below, or add a Dockerfile")
	}
	build := ""
	steps := []string{strings.SplitN(install, "&& ", 2)[len(strings.SplitN(install, "&& ", 2))-1]}
	if pkg.Scripts["build"] != "" {
		build = "RUN " + run + " build\n"
		steps = append(steps, run+" build")
	}
	steps = append(steps, strings.Join(start, " "))
	return &BuildPlan{
		Stack:   fmt.Sprintf("Node.js %s (%s)", node, pm),
		Summary: strings.Join(steps, " → "),
		Dockerfile: fmt.Sprintf(`FROM node:%s-alpine
WORKDIR /app
COPY %s ./
RUN %s
COPY . .
%sENV NODE_ENV=production PORT=%d
EXPOSE %d
CMD %s
`, node, lockCopy, install, build, port, port, jsonCmd(start...)),
	}, nil
}

func detectPython(dir string, port int, hasCommand bool) (*BuildPlan, error) {
	install := "pip install --no-cache-dir -r requirements.txt"
	if !exists(dir, "requirements.txt") {
		install = "pip install --no-cache-dir ."
	}
	var cmd string
	if raw, err := os.ReadFile(filepath.Join(dir, "Procfile")); err == nil {
		if m := procfileRE.FindSubmatch(raw); m != nil {
			cmd = jsonCmd("sh", "-c", strings.TrimSpace(string(m[1])))
		}
	}
	if cmd == "" {
		for _, f := range []string{"main.py", "app.py", "server.py"} {
			if exists(dir, f) {
				cmd = jsonCmd("python", f)
				break
			}
		}
	}
	if cmd == "" && hasCommand {
		cmd = jsonCmd("sh", "-c", "echo 'Set a start command' >&2; exit 1") // replaced by the resource's start command
	}
	if cmd == "" {
		return nil, fmt.Errorf("found a Python project but no way to start it. Add a Procfile with a \"web:\" line, a main.py or app.py, set a Start command below, or add a Dockerfile")
	}
	return &BuildPlan{
		Stack:   "Python 3.12",
		Summary: strings.TrimPrefix(install, "pip install --no-cache-dir ") + " → " + strings.Trim(cmd, "[]"),
		Dockerfile: fmt.Sprintf(`FROM python:3.12-slim
WORKDIR /app
ENV PYTHONUNBUFFERED=1 PORT=%d
COPY . .
RUN %s
EXPOSE %d
CMD %s
`, port, install, port, cmd),
	}, nil
}
