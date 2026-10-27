// Package compose turns a Docker Compose file into the resources IGNITION would create for it.
//
// Each service becomes one resource on the project's private network, where it answers to its service
// name, so services keep finding each other the way they did under Compose. What doesn't translate
// (bind mounts, extra published ports, unsupported keys) is reported as a warning instead of being
// dropped silently.
package compose

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Service struct {
	Name       string            `json:"name"`
	Original   string            `json:"original"` // the service name in the file
	Type       string            `json:"type"`     // application, database, worker
	SourceType string            `json:"sourceType"`
	Image      string            `json:"image"`
	Build      *Build            `json:"build,omitempty"`
	Port       *int              `json:"port"`
	Command    string            `json:"command"`
	Env        []EnvVar          `json:"env"`
	Volumes    []Volume          `json:"volumes"`
	MemoryMB   int               `json:"memoryMb"`
	CPUs       float64           `json:"cpus"`
	Restart    string            `json:"restart"` // always, on-failure, never
	Health     string            `json:"health,omitempty"`
	DependsOn  []string          `json:"dependsOn"`
	Warnings   []string          `json:"warnings"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type Build struct {
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile"`
	Target     string            `json:"target"`
	Args       map[string]string `json:"args"`
}

type EnvVar struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type Volume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Plan struct {
	Services []Service `json:"services"`
	Warnings []string  `json:"warnings"`
}

// Database images IGNITION recognises; their services become databases.
var databaseImages = []string{"postgres", "postgis", "mysql", "mariadb", "mongo", "redis", "valkey", "dragonfly", "clickhouse", "timescale", "cockroach", "keydb", "memcached", "elasticsearch", "opensearch", "rabbitmq", "nats", "minio"}

var (
	nameRE   = regexp.MustCompile(`[^a-z0-9-]+`)
	interpRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?+])([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	secretRE = regexp.MustCompile(`(?i)(PASSWORD|SECRET|TOKEN|KEY|PASS$|CREDENTIAL)`)
	sizeRE   = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([kmgKMG]?)[bB]?$`)
)

// SafeName makes a valid resource name from a service name.
func SafeName(s string) string {
	n := strings.Trim(nameRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(n) > 50 {
		n = strings.Trim(n[:50], "-")
	}
	if n == "" {
		n = "service"
	}
	return n
}

// Parse reads a compose file. vars fills ${VAR} references (e.g. from a .env file).
func Parse(data []byte, vars map[string]string) (*Plan, error) {
	var doc struct {
		Services map[string]yaml.Node `yaml:"services"`
		Version  string               `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("this isn't valid YAML: %v", cleanYAMLErr(err))
	}
	if len(doc.Services) == 0 {
		return nil, fmt.Errorf("the file has no services")
	}
	if len(doc.Services) > 30 {
		return nil, fmt.Errorf("the file has %d services; up to 30 can be imported at once", len(doc.Services))
	}
	plan := &Plan{Warnings: []string{}}
	missing := map[string]bool{}
	names := make([]string, 0, len(doc.Services))
	for n := range doc.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	used := map[string]string{}
	for _, n := range names {
		node := doc.Services[n]
		var raw map[string]any
		if err := node.Decode(&raw); err != nil {
			return nil, fmt.Errorf("service %s: %v", n, cleanYAMLErr(err))
		}
		svc := service(n, raw, vars, missing)
		if prev, ok := used[svc.Name]; ok {
			return nil, fmt.Errorf("services %q and %q both become %q; rename one", prev, n, svc.Name)
		}
		used[svc.Name] = n
		plan.Services = append(plan.Services, svc)
	}
	if len(missing) > 0 {
		var list []string
		for k := range missing {
			list = append(list, k)
		}
		sort.Strings(list)
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("No value for %s; they're left empty. Paste your .env file to fill them, or set them on each resource afterwards.", strings.Join(list, ", ")))
	}
	return plan, nil
}

func cleanYAMLErr(err error) string {
	return strings.TrimPrefix(err.Error(), "yaml: ")
}

func interpolate(s string, vars map[string]string, missing map[string]bool) string {
	s = strings.ReplaceAll(s, "$$", "\x00")
	s = interpRE.ReplaceAllStringFunc(s, func(m string) string {
		p := interpRE.FindStringSubmatch(m)
		name, op, def := p[1], p[2], p[3]
		if name == "" {
			name = p[4]
		}
		v, ok := vars[name]
		switch op {
		case ":-":
			if v == "" {
				return def
			}
		case "-":
			if !ok {
				return def
			}
		case ":+", "+":
			if (op == ":+" && v != "") || (op == "+" && ok) {
				return def
			}
			return ""
		}
		if !ok && op == "" {
			missing[name] = true
		}
		return v
	})
	return strings.ReplaceAll(s, "\x00", "$")
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func service(name string, raw map[string]any, vars map[string]string, missing map[string]bool) Service {
	in := func(v any) string { return interpolate(str(v), vars, missing) }
	s := Service{Name: SafeName(name), Original: name, Env: []EnvVar{}, Volumes: []Volume{}, DependsOn: []string{}, Warnings: []string{}, MemoryMB: 512, CPUs: 1, Restart: "always"}
	if s.Name != name {
		s.Warnings = append(s.Warnings, fmt.Sprintf("Renamed to %s (resource names are lowercase letters, numbers and dashes). Other services that call it %q need updating.", s.Name, name))
	}
	known := map[string]bool{"image": true, "build": true, "ports": true, "expose": true, "environment": true, "env_file": true, "command": true, "entrypoint": true,
		"volumes": true, "depends_on": true, "restart": true, "healthcheck": true, "deploy": true, "mem_limit": true, "cpus": true, "container_name": true,
		"labels": true, "networks": true, "working_dir": true, "user": true, "hostname": true}

	if img, ok := raw["image"]; ok {
		s.Image, s.SourceType = in(img), "image"
	}
	if b, ok := raw["build"]; ok {
		s.Build = &Build{Context: ".", Dockerfile: "Dockerfile", Args: map[string]string{}}
		switch t := b.(type) {
		case string:
			s.Build.Context = in(t)
		case map[string]any:
			if c := in(t["context"]); c != "" {
				s.Build.Context = c
			}
			if d := in(t["dockerfile"]); d != "" {
				s.Build.Dockerfile = d
			}
			s.Build.Target = in(t["target"])
			for k, v := range kv(t["args"]) {
				s.Build.Args[k] = in(v)
			}
		}
		s.SourceType = "git" // builds need the repository; a pasted file falls back to the image if there is one
	}
	if s.Image == "" && s.Build == nil {
		s.Warnings = append(s.Warnings, "Has neither image nor build, so it can't be created.")
	}

	lower := strings.ToLower(s.Image)
	for _, d := range databaseImages {
		repo := lower
		if i := strings.LastIndex(repo, "/"); i >= 0 {
			repo = repo[i+1:]
		}
		if strings.HasPrefix(repo, d) {
			s.Type = "database"
		}
	}

	// Ports: the container side of the first mapping becomes the resource's port.
	var ports []int
	for _, p := range list(raw["ports"]) {
		if c := containerPort(in(p)); c > 0 {
			ports = append(ports, c)
		}
	}
	for _, p := range list(raw["expose"]) {
		if c, err := strconv.Atoi(strings.Split(in(p), "/")[0]); err == nil && c > 0 {
			ports = append(ports, c)
		}
	}
	if len(ports) > 0 {
		p := ports[0]
		s.Port = &p
		if len(ports) > 1 {
			s.Warnings = append(s.Warnings, fmt.Sprintf("Uses port %d; add the others (%s) under Networking afterwards.", p, joinInts(ports[1:])))
		}
	}
	if len(list(raw["ports"])) > 0 && s.Type != "database" {
		s.Warnings = append(s.Warnings, "Published ports get a free host port on this machine (localhost only); add a domain to make it public.")
	}
	if s.Type == "" {
		if s.Port != nil {
			s.Type = "application"
		} else {
			s.Type = "worker"
		}
	}

	for k, v := range kv(raw["environment"]) {
		s.Env = append(s.Env, EnvVar{Key: k, Value: in(v), Secret: secretRE.MatchString(k)})
	}
	sort.Slice(s.Env, func(i, j int) bool { return s.Env[i].Key < s.Env[j].Key })
	if _, ok := raw["env_file"]; ok {
		s.Warnings = append(s.Warnings, "Uses env_file: paste that file into the .env box so its values are included.")
		for k, v := range vars {
			if !hasEnv(s.Env, k) {
				s.Env = append(s.Env, EnvVar{Key: k, Value: v, Secret: secretRE.MatchString(k)})
			}
		}
	}

	cmd := command(raw["command"], in)
	if ep := command(raw["entrypoint"], in); ep != "" {
		cmd = strings.TrimSpace(ep + " " + cmd)
		s.Warnings = append(s.Warnings, "Its entrypoint is folded into the start command.")
	}
	s.Command = cmd

	for i, v := range list(raw["volumes"]) {
		spec := ""
		switch t := v.(type) {
		case string:
			spec = in(t)
		case map[string]any:
			spec = in(t["source"]) + ":" + in(t["target"])
			if str(t["type"]) == "bind" {
				spec = "./" + spec
			}
		}
		src, dst, ok := strings.Cut(spec, ":")
		if !ok { // anonymous volume
			dst, src = src, fmt.Sprintf("data%d", i+1)
		}
		dst = strings.Split(dst, ":")[0]
		if strings.HasPrefix(src, ".") || strings.HasPrefix(src, "/") || strings.HasPrefix(src, "~") {
			s.Warnings = append(s.Warnings, fmt.Sprintf("Bind mount %s → %s isn't imported: workloads don't see this machine's files. Bake the files into the image or use a volume.", src, dst))
			continue
		}
		if strings.HasPrefix(dst, "/") {
			s.Volumes = append(s.Volumes, Volume{Name: SafeVolume(src), Path: dst})
		}
	}

	switch str(raw["restart"]) {
	case "no":
		s.Restart = "never"
	case "on-failure":
		s.Restart = "on-failure"
	}
	if m := memory(str(raw["mem_limit"])); m > 0 {
		s.MemoryMB = m
	}
	if c, err := strconv.ParseFloat(str(raw["cpus"]), 64); err == nil && c > 0 {
		s.CPUs = c
	}
	if d, ok := raw["deploy"].(map[string]any); ok {
		if res, ok := d["resources"].(map[string]any); ok {
			if lim, ok := res["limits"].(map[string]any); ok {
				if m := memory(str(lim["memory"])); m > 0 {
					s.MemoryMB = m
				}
				if c, err := strconv.ParseFloat(str(lim["cpus"]), 64); err == nil && c > 0 {
					s.CPUs = c
				}
			}
		}
		if r, ok := d["replicas"]; ok && str(r) != "1" {
			s.Warnings = append(s.Warnings, "Replicas aren't supported; it runs one container.")
		}
	}
	if s.Type == "database" && s.MemoryMB < 512 {
		s.MemoryMB = 512
	}
	if s.MemoryMB < 64 {
		s.MemoryMB = 64
	}

	if hc, ok := raw["healthcheck"].(map[string]any); ok && str(hc["disable"]) != "true" {
		switch t := hc["test"].(type) {
		case string:
			s.Health = in(t)
		case []any:
			parts := make([]string, 0, len(t))
			for _, p := range t {
				parts = append(parts, in(p))
			}
			if len(parts) > 1 && (parts[0] == "CMD-SHELL" || parts[0] == "CMD") {
				s.Health = strings.Join(parts[1:], " ")
			}
		}
	}

	switch d := raw["depends_on"].(type) {
	case []any:
		for _, x := range d {
			s.DependsOn = append(s.DependsOn, SafeName(str(x)))
		}
	case map[string]any:
		for k := range d {
			s.DependsOn = append(s.DependsOn, SafeName(k))
		}
	}
	sort.Strings(s.DependsOn)

	if cn := str(raw["container_name"]); cn != "" && cn != name {
		s.Warnings = append(s.Warnings, fmt.Sprintf("container_name %q is ignored; other services reach it as %s.", cn, s.Name))
	}
	if _, ok := raw["networks"]; ok {
		s.Warnings = append(s.Warnings, "Custom networks are replaced by the environment's private network, which every resource of the project joins.")
	}
	var unknown []string
	for k := range raw {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		s.Warnings = append(s.Warnings, "Not imported: "+strings.Join(unknown, ", ")+".")
	}
	return s
}

func hasEnv(list []EnvVar, k string) bool {
	for _, e := range list {
		if e.Key == k {
			return true
		}
	}
	return false
}

// SafeVolume makes a valid volume name.
func SafeVolume(s string) string {
	n := strings.Trim(nameRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(n) > 32 {
		n = strings.Trim(n[:32], "-")
	}
	if n == "" {
		n = "data"
	}
	return n
}

func list(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// kv reads "environment"/"args" in either map or KEY=value list form.
func kv(v any) map[string]any {
	out := map[string]any{}
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			out[k] = x
		}
	case []any:
		for _, x := range t {
			k, val, _ := strings.Cut(str(x), "=")
			out[k] = val
		}
	}
	return out
}

func command(v any, in func(any) string) string {
	switch t := v.(type) {
	case string:
		return in(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, p := range t {
			s := in(p)
			if strings.ContainsAny(s, " \t'\"$") {
				s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func containerPort(p string) int {
	p = strings.Split(p, "/")[0]
	parts := strings.Split(p, ":")
	last := parts[len(parts)-1]
	if a, _, ok := strings.Cut(last, "-"); ok {
		last = a
	}
	n, _ := strconv.Atoi(last)
	return n
}

func memory(s string) int {
	m := sizeRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	switch strings.ToLower(m[2]) {
	case "g":
		f *= 1024
	case "k":
		f /= 1024
	case "":
		f /= 1024 * 1024
	}
	return int(f)
}

func joinInts(list []int) string {
	s := make([]string, len(list))
	for i, n := range list {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// ParseEnvFile reads KEY=value lines (a .env file).
func ParseEnvFile(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}
