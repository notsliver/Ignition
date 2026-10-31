package runtime

import (
	"context"
	"net/url"
	"strings"
)

// NetworkOptions shape a managed network at creation. Docker can't change them afterwards.
type NetworkOptions struct {
	Subnet   string
	Gateway  string
	IPv6     bool
	Subnet6  string
	Internal bool
}

// Address is a fixed address for a container on one network ("" = assigned by Docker).
type Address struct{ IPv4, IPv6 string }

func (o NetworkOptions) body(name string, labels map[string]string) map[string]any {
	body := map[string]any{"Name": name, "Driver": "bridge", "Labels": labels, "Internal": o.Internal}
	var cfg []map[string]string
	if o.Subnet != "" {
		c := map[string]string{"Subnet": o.Subnet}
		if o.Gateway != "" {
			c["Gateway"] = o.Gateway
		}
		cfg = append(cfg, c)
	}
	if o.IPv6 {
		body["EnableIPv6"] = true
		if o.Subnet6 != "" {
			cfg = append(cfg, map[string]string{"Subnet": o.Subnet6})
		}
	}
	if len(cfg) > 0 {
		body["IPAM"] = map[string]any{"Driver": "default", "Config": cfg}
	}
	return body
}

// EnsureManagedNetworkWith is EnsureManagedNetwork with subnets, IPv6 and isolation.
func (d *Docker) EnsureManagedNetworkWith(ctx context.Context, name string, labels map[string]string, o NetworkOptions) error {
	err := d.doJSON(ctx, "GET", "/networks/"+url.PathEscape(name), nil, nil, nil)
	if err == nil || !IsNotFound(err) {
		return err
	}
	all := map[string]string{"ignition.managed": "true", "ignition.instance": d.Instance(ctx)}
	for k, v := range labels {
		all[k] = v
	}
	err = d.doJSON(ctx, "POST", "/networks/create", nil, o.body(name, all), nil)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// endpoint is an EndpointsConfig entry: aliases plus fixed addresses when set.
func endpoint(aliases []string, a Address) map[string]any {
	e := map[string]any{"Aliases": aliases}
	if a.IPv4 != "" || a.IPv6 != "" {
		ipam := map[string]string{}
		if a.IPv4 != "" {
			ipam["IPv4Address"] = a.IPv4
		}
		if a.IPv6 != "" {
			ipam["IPv6Address"] = a.IPv6
		}
		e["IPAMConfig"] = ipam
	}
	return e
}

// ConnectNetworkAt attaches a running container with fixed addresses.
func (d *Docker) ConnectNetworkAt(ctx context.Context, network, container string, aliases []string, a Address) error {
	err := d.doJSON(ctx, "POST", "/networks/"+url.PathEscape(network)+"/connect", nil,
		map[string]any{"Container": container, "EndpointConfig": endpoint(aliases, a)}, nil)
	if err != nil && (strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "already attached")) {
		return nil
	}
	return err
}

// NetworkEndpoint is one container's addresses on a network.
type NetworkEndpoint struct {
	Container string `json:"container"`
	IPv4      string `json:"ipv4"`
	IPv6      string `json:"ipv6"`
	MAC       string `json:"mac"`
}

// NetworkEndpoints lists the containers on a network with their addresses (IPAM view).
func (d *Docker) NetworkEndpoints(ctx context.Context, name string) ([]NetworkEndpoint, error) {
	var raw struct {
		Containers map[string]struct{ Name, IPv4Address, IPv6Address, MacAddress string }
	}
	if err := d.doJSON(ctx, "GET", "/networks/"+url.PathEscape(name), nil, nil, &raw); err != nil {
		return nil, err
	}
	out := []NetworkEndpoint{}
	for _, c := range raw.Containers {
		out = append(out, NetworkEndpoint{Container: c.Name, IPv4: strings.Split(c.IPv4Address, "/")[0], IPv6: strings.Split(c.IPv6Address, "/")[0], MAC: c.MacAddress})
	}
	return out, nil
}
