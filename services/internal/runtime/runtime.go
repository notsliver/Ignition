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
	Ports    []PortMapping
	MemoryMB int
	CPUs     float64
	Labels   map[string]string
	Network  string
	Networks []string          // extra networks to join besides Network
	Aliases  []string          // DNS names on every network, stable across deployments
	Volumes  map[string]string // named volume -> mount path; created on demand, kept across containers
}

type State struct {
	Exists     bool
	Running    bool
	Status     string // created, running, exited, restarting, …
	ExitCode   int
	StartedAt  time.Time
	HostPort   int // first published host port, kept for older callers
	Image      string
	Restarting bool
	OOMKilled  bool
	Ports      []PortMapping // what Docker actually published
	Networks   []Attachment
}

// PortMapping publishes ContainerPort on the host. HostPort 0 lets Docker pick a free port.
type PortMapping struct {
	HostIP        string `json:"hostIp"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

// Attachment is a container's membership of one network, as Docker reports it.
type Attachment struct {
	Network string   `json:"network"`
	IP      string   `json:"ip"`
	Aliases []string `json:"aliases"`
}

type Network struct {
	Name     string            `json:"name"`
	Driver   string            `json:"driver"`
	Scope    string            `json:"scope"`
	Internal bool              `json:"internal"`
	Labels   map[string]string `json:"-"`
}

// HostBinding is a host port some container (Ignition's or not) has published.
type HostBinding struct {
	HostIP    string
	HostPort  int
	Protocol  string
	Container string // name
	Resource  string // ignition.resource label, if any
}

// Probe runs a throwaway command in a small container attached to the given networks.
type Probe struct {
	Networks []string
	Cmd      []string
	Timeout  time.Duration
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
	// PullImage pulls ref. auth is an X-Registry-Auth value for private registries, or empty.
	PullImage(ctx context.Context, ref, auth string, log Logf) error
	RegistryLogin(ctx context.Context, username, password, server string) error
	BuildImage(ctx context.Context, contextTar io.Reader, dockerfile, tag string, log Logf) error
	EnsureNetwork(ctx context.Context, name string) error
	EnsureNetworkExists(ctx context.Context, name string) error
	// EnsureManagedNetwork creates an Ignition-managed bridge network with these labels if it's missing.
	EnsureManagedNetwork(ctx context.Context, name string, labels map[string]string) error
	RemoveNetwork(ctx context.Context, name string) error
	// ConnectNetwork attaches a running container live; it's a no-op when already attached.
	ConnectNetwork(ctx context.Context, network, container string, aliases []string) error
	DisconnectNetwork(ctx context.Context, network, container string) error
	ContainerLabels(ctx context.Context, id string) (map[string]string, error)
	// Instance is this install's compose project; managed networks are labelled with it.
	Instance(ctx context.Context) string
	// FindContainer returns the ID of a running container carrying all these labels, or "".
	FindContainer(ctx context.Context, labels map[string]string) (string, error)
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Restart(ctx context.Context, id string, timeout time.Duration) error
	Remove(ctx context.Context, id string, removeVolumes bool) error
	RemoveVolume(ctx context.Context, name string) error
	Inspect(ctx context.Context, id string) (State, error)
	Logs(ctx context.Context, id string, tail int, since time.Time) ([]LogLine, error)
	Stats(ctx context.Context, id string) (Stats, error)
	Networks(ctx context.Context) ([]Network, error)
	HostBindings(ctx context.Context) ([]HostBinding, error)
	// RunProbe returns the command's exit code and combined output.
	RunProbe(ctx context.Context, p Probe) (int, string, error)
}
