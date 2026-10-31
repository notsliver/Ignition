package servers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/hostinfo"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
)

var ErrNoCredentials = errors.New("this server has no credentials: they were revoked. Rotate in new credentials to reconnect")

// Registry hands out a Docker client per server. Remote clients are cached and rebuilt when the
// endpoint, credentials or timeout change. Decrypted keys live only inside the TLS config.
type Registry struct {
	Store   *store.Store
	Secrets *secrets.Box
	Local   *runtime.Docker

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	key    string
	docker *runtime.Docker
}

// KeyContext binds a sealed client key to its server, so a key copied to another row won't open.
func KeyContext(serverID string) string { return "server-key:" + serverID }

func (r *Registry) Docker(srv *store.Server) (*runtime.Docker, error) {
	if srv.IsLocal {
		return r.Local, nil
	}
	if len(srv.ClientKeySealed) == 0 {
		return nil, ErrNoCredentials
	}
	settings := ParseSettings(srv.Settings)
	rotated := ""
	if srv.CredentialsRotatedAt != nil {
		rotated = srv.CredentialsRotatedAt.String()
	}
	key := strings.Join([]string{srv.Endpoint, srv.TLSFingerprint, rotated, strconv.Itoa(settings.ConnectTimeoutSeconds)}, "|")
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[srv.ID]; ok && c.key == key {
		return c.docker, nil
	}
	plain, err := r.Secrets.Open(srv.ClientKeySealed, KeyContext(srv.ID))
	if err != nil {
		return nil, fmt.Errorf("the stored client key can't be decrypted; was IGNITION_SECRET_KEY changed?")
	}
	d, err := RemoteDocker(srv.Endpoint, Credentials{CACert: srv.CACert, ClientCert: srv.ClientCert, ClientKey: plain}, srv.TLSFingerprint, nil, settings.Timeout())
	if err != nil {
		return nil, err
	}
	if r.cache == nil {
		r.cache = map[string]cached{}
	}
	r.cache[srv.ID] = cached{key, d}
	return d, nil
}

// Forget drops a cached client, e.g. after credentials are revoked.
func (r *Registry) Forget(id string) {
	r.mu.Lock()
	delete(r.cache, id)
	r.mu.Unlock()
}

// RemoteDocker builds a verified mutual-TLS client. observed receives the engine's certificate fingerprint.
func RemoteDocker(endpoint string, c Credentials, pinned string, observed *string, timeout time.Duration) (*runtime.Docker, error) {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, err
	}
	cfg, err := TLSConfig(c, host, pinned, observed)
	if err != nil {
		return nil, err
	}
	return runtime.NewDockerTLS(endpoint, cfg, timeout), nil
}

// Observation is the outcome of one connection check.
type Observation struct {
	Reached     bool
	Runtime     string
	Info        runtime.EngineInfo
	HasInfo     bool
	Error       string
	ErrorKind   string // timeout, refused, dns, identity, auth, tls, credentials, socket, engine
	Fingerprint string
	Latency     time.Duration
	Procfs      bool
	HostProbe   bool // the host probe answered on the previous sample
	SkewMS      *int64
}

// Probe checks one server: can Ignition reach it, and is its Docker Engine usable?
func (r *Registry) Probe(ctx context.Context, srv *store.Server) Observation {
	settings := ParseSettings(srv.Settings)
	ctx, cancel := context.WithTimeout(ctx, settings.Timeout()+2*time.Second)
	defer cancel()
	start := time.Now()
	var o Observation
	if srv.IsLocal {
		o.Procfs = hostinfo.Available()
		// Ignition runs on this host, so the host itself is always reachable while the worker reports.
		o.Reached = true
		if sock := r.Local.SocketPath(); sock != "" {
			if _, err := os.Stat(sock); err != nil {
				o.Runtime, o.Error, o.ErrorKind = RuntimeNotInstalled, "No Docker socket at "+sock+".", "socket"
				o.Latency = time.Since(start)
				return o
			}
		}
		info, err := r.Local.EngineInfo(ctx)
		o.Latency = time.Since(start)
		if err != nil {
			o.Runtime, o.Error, o.ErrorKind = RuntimeUnavailable, "Docker didn't respond: "+cleanErr(err), "engine"
			return o
		}
		o.fill(info)
		return o
	}
	d, err := r.Docker(srv)
	if err != nil {
		o.Error, o.ErrorKind = err.Error(), "credentials"
		return o
	}
	// A fresh client per probe would cost a TLS handshake each time; the cached one reuses the
	// connection, and the pinned fingerprint is enforced on every new handshake.
	info, err := d.EngineInfo(ctx)
	o.Latency = time.Since(start)
	o.Fingerprint = srv.TLSFingerprint
	if err != nil {
		o.Error, o.ErrorKind = Classify(err)
		return o
	}
	o.Reached = true
	o.fill(info)
	return o
}

func (o *Observation) fill(info runtime.EngineInfo) {
	o.Info, o.HasInfo = info, true
	if runtime.APICompatible(info.APIVersion) {
		o.Runtime = RuntimeReady
	} else {
		o.Runtime = RuntimeIncompatible
		o.Error = fmt.Sprintf("Docker %s speaks Engine API %s; Ignition needs %s or newer.", info.Version, info.APIVersion, runtime.MinAPIVersion)
	}
	if !info.SystemTime.IsZero() {
		// Half the round trip is the best estimate of when the engine read its clock.
		skew := time.Until(info.SystemTime).Milliseconds() + o.Latency.Milliseconds()/2
		o.SkewMS = &skew
	}
}

// Classify turns a connection error into an operator-readable message and a category.
func Classify(err error) (string, string) {
	var id *IdentityError
	var dnsErr *net.DNSError
	var opErr *net.OpError
	msg := err.Error()
	switch {
	case errors.As(err, &id):
		return "Host identity verification failed: the engine presented a different certificate than the one accepted at enrollment. Verify the host before rotating credentials.", "identity"
	case errors.As(err, &dnsErr):
		return "The hostname doesn't resolve: " + dnsErr.Err + ".", "dns"
	case strings.Contains(msg, "certificate signed by unknown authority"), strings.Contains(msg, "x509:"):
		return "The engine's certificate doesn't match the CA or hostname: " + cleanErr(err), "identity"
	case strings.Contains(msg, "bad certificate"), strings.Contains(msg, "unknown certificate"), strings.Contains(msg, "certificate required"):
		return "The engine rejected Ignition's client certificate. Check it was signed by the CA the daemon trusts (--tlscacert).", "auth"
	case strings.Contains(msg, "tls:"), strings.Contains(msg, "first record does not look like a TLS handshake"):
		return "TLS handshake failed: " + cleanErr(err) + ". Is the daemon started with --tlsverify on this port?", "tls"
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(msg, "timeout"):
		return "Connection timed out. Check the address, port and any firewall between Ignition and the server.", "timeout"
	case errors.As(err, &opErr) && strings.Contains(msg, "refused"):
		return "Connection refused: nothing is listening on that port.", "refused"
	case strings.Contains(msg, "no route"), strings.Contains(msg, "unreachable"):
		return "The network can't reach the server: " + cleanErr(err), "refused"
	}
	return cleanErr(err), "engine"
}

func cleanErr(err error) string {
	s := err.Error()
	s = strings.TrimPrefix(s, "docker unreachable: ")
	if i := strings.LastIndex(s, ": "); i > 0 && strings.HasPrefix(s, "Get \"") {
		s = s[i+2:]
	}
	return s
}
