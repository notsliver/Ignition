// Package runtime abstracts the container engine so deployment logic doesn't depend on Docker details.
package runtime

import (
	"context"
	"io"
	"time"
)

type ContainerSpec struct {
	Name         string
	Image        string
	Env          []string // KEY=value
	Cmd          []string // overrides CMD when non-empty
	Ports        []PortMapping
	MemoryMB     int
	CPUs         float64
	Limits       Limits // reservation, PIDs, swap, restart policy
	Labels       map[string]string
	Network      string
	Networks     []string          // extra networks to join besides Network
	Aliases      []string          // DNS names on every network, stable across deployments
	Volumes      map[string]string // named volume -> mount path; created on demand, kept across containers
	VolumeLabels map[string]string // labels for volumes created on demand
	Binds        []string          // host:container[:ro] bind mounts (only Ignition's own helpers use these)
	AutoRemove   bool
	Entrypoint   []string // overrides the image's ENTRYPOINT when non-empty
	WorkingDir   string
	User         string
	ReadOnlyRoot bool // root filesystem read-only; /tmp stays writable (tmpfs)
	Init         bool // run an init process as PID 1 (reaps zombies, forwards signals)
	Privileged   bool
}

// ContainerConfig is what a container was actually created with, for drift detection.
type ContainerConfig struct {
	Entrypoint   []string          `json:"entrypoint"`
	Cmd          []string          `json:"cmd"`
	User         string            `json:"user"`
	WorkingDir   string            `json:"workingDir"`
	Labels       map[string]string `json:"labels"`
	ReadOnlyRoot bool              `json:"readOnlyRoot"`
	Init         bool              `json:"init"`
	Privileged   bool              `json:"privileged"`
	MemoryBytes  int64             `json:"memoryBytes"`
	NanoCPUs     int64             `json:"nanoCpus"`
	Restart      string            `json:"restart"`
}

// Image is a local image.
type Image struct {
	ID      string    `json:"id"`
	Tags    []string  `json:"tags"`
	Size    int64     `json:"size"` // bytes only this image uses (shared layers excluded)
	Created time.Time `json:"created"`
}

type State struct {
	Exists       bool
	Running      bool
	Status       string // created, running, exited, restarting, …
	ExitCode     int
	StartedAt    time.Time
	HostPort     int // first published host port, kept for older callers
	Image        string
	ImageID      string // sha256:… of the image the container runs
	Restarting   bool
	Paused       bool
	OOMKilled    bool
	RestartCount int
	FinishedAt   time.Time
	Mounts       []Mount
	Ports        []PortMapping // what Docker actually published
	Networks     []Attachment
	Config       ContainerConfig
}

type TTYOptions struct {
	Cmd        []string
	Env        []string
	User       string // "" = the image's user; "0" = root
	Cols, Rows int
}

// TTYSession is a hijacked exec stream: read the terminal's output, write keystrokes.
type TTYSession struct {
	ID string
	io.Reader
	io.Writer
	Close func() error
}

// Limits are applied at create time and can be changed live with UpdateLimits.
type Limits struct {
	MemoryMB            int
	CPUs                float64
	MemoryReservationMB int  // 0 = none
	PidsLimit           int  // 0 = unlimited
	NoSwap              bool // true: memory+swap = memory
	Restart             string
	MaxRestarts         int
}

type Mount struct {
	Volume      string `json:"volume"`
	Destination string `json:"destination"`
}

type VolumeInfo struct {
	Name      string
	SizeBytes int64 // -1 when Docker doesn't know yet
	InUse     bool
	Labels    map[string]string
}

type EngineVersion struct {
	Docker string
	OS     string
}

type HostInfo struct {
	CPUs        int
	MemoryBytes int64
}

type BuildOptions struct {
	Dockerfile string // relative to the context
	Tag        string
	Target     string
	Args       map[string]string
	NoCache    bool
	Labels     map[string]string
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
	BuildImage(ctx context.Context, contextTar io.Reader, opts BuildOptions, log Logf) error
	// Exec runs cmd inside a running container and returns its exit code; output goes to log.
	Exec(ctx context.Context, container string, cmd []string, env []string, log Logf) (int, error)
	// ExecTTY starts an interactive command with a pseudo-terminal and returns its raw stream.
	ExecTTY(ctx context.Context, container string, opts TTYOptions) (*TTYSession, error)
	ResizeExec(ctx context.Context, execID string, cols, rows int) error
	// ExecExitCode is the exit code once the exec has finished; running is true while it still runs.
	ExecExitCode(ctx context.Context, execID string) (code int, running bool, err error)
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
	// ResourceContainers lists every container (running or not) labelled with this resource.
	ResourceContainers(ctx context.Context, resourceID string) ([]string, error)
	// StaleHelpers lists probe and backup helper containers created before the cutoff.
	StaleHelpers(ctx context.Context, before time.Time) ([]string, error)
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Restart(ctx context.Context, id string, timeout time.Duration) error
	Pause(ctx context.Context, id string) error
	Unpause(ctx context.Context, id string) error
	// ImageID returns the local image's ID, or "" when it isn't on this machine.
	ImageID(ctx context.Context, ref string) (string, error)
	// Images lists local images whose repository is repo (e.g. ignition/1a2b3c4d) or that carry the
	// label (key=value), including ones a later build of the same tag left untagged.
	Images(ctx context.Context, repo, label string) ([]Image, error)
	RemoveImage(ctx context.Context, id string) error
	Remove(ctx context.Context, id string, removeVolumes bool) error
	RemoveVolume(ctx context.Context, name string) error
	Inspect(ctx context.Context, id string) (State, error)
	Logs(ctx context.Context, id string, tail int, since time.Time) ([]LogLine, error)
	Stats(ctx context.Context, id string) (Stats, error)
	UpdateLimits(ctx context.Context, id string, l Limits) error
	// Volumes lists volumes whose names start with prefix, with their size on disk.
	Volumes(ctx context.Context, prefix string) ([]VolumeInfo, error)
	// ExportVolume streams a tar of the volume's contents. Close the reader when done.
	ExportVolume(ctx context.Context, name string) (io.ReadCloser, error)
	Host(ctx context.Context) (HostInfo, error)
	Version(ctx context.Context) (EngineVersion, error)
	Networks(ctx context.Context) ([]Network, error)
	HostBindings(ctx context.Context) ([]HostBinding, error)
	// RunProbe returns the command's exit code and combined output.
	RunProbe(ctx context.Context, p Probe) (int, string, error)
}
