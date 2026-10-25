// Package runtime abstracts the container engine so deployment logic doesn't depend on Docker details.
package runtime

import (
	"context"
	"io"
	"time"
)

type ContainerSpec struct {
	Name     string
	Image    string
	Env      []string // KEY=value
	Cmd      []string // overrides CMD when non-empty
	Port     int      // container port to publish on a random host port; 0 = none
	MemoryMB int
	CPUs     float64
	Labels   map[string]string
	Network  string
	Aliases  []string          // DNS names on Network, stable across deployments
	Volumes  map[string]string // named volume -> mount path; created on demand, kept across containers
}

type State struct {
	Exists     bool
	Running    bool
	Status     string // created, running, exited, restarting, …
	ExitCode   int
	StartedAt  time.Time
	HostPort   int
	Image      string
	Restarting bool
	OOMKilled  bool
}

type LogLine struct {
	Time    time.Time `json:"time"`
	Stream  string    `json:"stream"` // stdout or stderr
	Message string    `json:"message"`
}

type Stats struct {
	CPUPercent  float64   `json:"cpuPercent"`
	MemoryBytes uint64    `json:"memoryBytes"`
	MemoryLimit uint64    `json:"memoryLimit"`
	NetworkRxB  uint64    `json:"networkRxBytes"`
	NetworkTxB  uint64    `json:"networkTxBytes"`
	CollectedAt time.Time `json:"collectedAt"`
}

// Logf receives progress lines from long operations (pulls, builds).
type Logf func(level, message string)

type Runtime interface {
	Ping(ctx context.Context) error
	PullImage(ctx context.Context, ref string, log Logf) error
	BuildImage(ctx context.Context, contextTar io.Reader, dockerfile, tag string, log Logf) error
	EnsureNetwork(ctx context.Context, name string) error
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Restart(ctx context.Context, id string, timeout time.Duration) error
	Remove(ctx context.Context, id string, removeVolumes bool) error
	RemoveVolume(ctx context.Context, name string) error
	Inspect(ctx context.Context, id string) (State, error)
	Logs(ctx context.Context, id string, tail int, since time.Time) ([]LogLine, error)
	Stats(ctx context.Context, id string) (Stats, error)
}
