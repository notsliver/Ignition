package servers

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Credentials for a remote Docker Engine started with --tlsverify. The CA authenticates the
// engine (host identity); the client certificate authenticates Ignition.
type Credentials struct {
	CACert     string `json:"caCert"`
	ClientCert string `json:"clientCert"`
	ClientKey  string `json:"clientKey"`
}

type CertSummary struct {
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotAfter    time.Time `json:"notAfter"`
	Fingerprint string    `json:"fingerprint"`
}

// Validate parses the PEM material and reports problems per field, without echoing secrets.
func (c Credentials) Validate() (map[string]string, *CertSummary) {
	errs := map[string]string{}
	if _, err := caPool(c.CACert); err != nil {
		errs["caCert"] = err.Error()
	}
	pair, err := tls.X509KeyPair([]byte(c.ClientCert), []byte(c.ClientKey))
	if err != nil {
		if strings.TrimSpace(c.ClientCert) == "" {
			errs["clientCert"] = "Paste the client certificate (cert.pem)."
		} else if strings.TrimSpace(c.ClientKey) == "" {
			errs["clientKey"] = "Paste the client private key (key.pem)."
		} else {
			errs["clientKey"] = "The certificate and key don't form a valid pair."
		}
		return errs, nil
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		errs["clientCert"] = "The client certificate can't be parsed."
		return errs, nil
	}
	if time.Now().After(leaf.NotAfter) {
		errs["clientCert"] = "This certificate expired on " + leaf.NotAfter.Format("2 Jan 2006") + "."
	}
	return errs, summarize(leaf)
}

func summarize(c *x509.Certificate) *CertSummary {
	sum := sha256.Sum256(c.Raw)
	return &CertSummary{Subject: c.Subject.String(), Issuer: c.Issuer.String(), NotAfter: c.NotAfter, Fingerprint: hex.EncodeToString(sum[:])}
}

// ClientCertSummary describes a stored client certificate (public material only).
func ClientCertSummary(certPEM string) *CertSummary {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return summarize(c)
}

func caPool(caPEM string) (*x509.CertPool, error) {
	if strings.TrimSpace(caPEM) == "" {
		return nil, errors.New("Paste the CA certificate (ca.pem) that signed the engine's certificate.")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, errors.New("This isn't a PEM certificate.")
	}
	return pool, nil
}

// TLSConfig verifies the engine's certificate against the CA and the hostname, and, when pinned
// is set, also requires the exact certificate accepted at enrollment. There's no skip-verify path.
// observed receives the SHA-256 fingerprint of the certificate the engine presented.
func TLSConfig(c Credentials, serverName, pinned string, observed *string) (*tls.Config, error) {
	pool, err := caPool(c.CACert)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair([]byte(c.ClientCert), []byte(c.ClientKey))
	if err != nil {
		return nil, errors.New("invalid client certificate or key")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      pool,
		ServerName:   serverName,
		Certificates: []tls.Certificate{pair},
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the engine presented no certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			fp := hex.EncodeToString(sum[:])
			if observed != nil {
				*observed = fp
			}
			if pinned != "" && !strings.EqualFold(fp, pinned) {
				return &IdentityError{Expected: pinned, Got: fp}
			}
			return nil
		},
	}, nil
}

// IdentityError means the engine presented a different certificate than the one accepted at
// enrollment. It's never accepted silently: the operator must re-verify and rotate.
type IdentityError struct{ Expected, Got string }

func (e *IdentityError) Error() string {
	return fmt.Sprintf("host identity changed: expected certificate %s…, got %s…", e.Expected[:16], e.Got[:16])
}

// ParseEndpoint validates host:port for a remote engine. It refuses addresses that are never a
// legitimate remote Docker host (loopback, link-local and cloud metadata, unspecified, multicast),
// so the verification endpoint can't be pointed at Ignition itself or the metadata service.
func ParseEndpoint(raw string) (host string, port int, err error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "tcp://"), "https://")
	if raw == "" {
		return "", 0, errors.New("Enter the engine's address, e.g. docker1.example.com:2376.")
	}
	if strings.ContainsAny(raw, "/?#@ ") {
		return "", 0, errors.New("Use host:port only, without a path.")
	}
	h, p, splitErr := net.SplitHostPort(raw)
	if splitErr != nil {
		h, p = raw, "2376"
	}
	port, err = strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("The port must be between 1 and 65535.")
	}
	if h == "" || len(h) > 253 {
		return "", 0, errors.New("Enter a hostname or IP address.")
	}
	if addr, perr := netip.ParseAddr(h); perr == nil {
		if err := checkAddr(addr); err != nil {
			return "", 0, err
		}
	} else if !validHostname(h) {
		return "", 0, errors.New("That isn't a valid hostname.")
	}
	return strings.ToLower(h), port, nil
}

func checkAddr(a netip.Addr) error {
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return errors.New("Loopback addresses aren't allowed: the local Docker host is already registered as \"This machine\".")
	case a.IsLinkLocalUnicast(), a == netip.MustParseAddr("fd00:ec2::254"):
		return errors.New("Link-local and cloud metadata addresses aren't allowed.")
	case a.IsUnspecified(), a.IsMulticast(), a.IsInterfaceLocalMulticast():
		return errors.New("That address can't be a Docker host.")
	}
	return nil
}

func validHostname(h string) bool {
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// ResolveAllowed resolves a hostname and applies the same address rules to every result, so a
// DNS name can't be used to reach a forbidden address either.
func ResolveAllowed(host string, lookup func(string) ([]netip.Addr, error)) error {
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	addrs, err := lookup(host)
	if err != nil {
		return fmt.Errorf("%s doesn't resolve: %v", host, err)
	}
	for _, a := range addrs {
		if err := checkAddr(a); err != nil {
			return fmt.Errorf("%s resolves to %s. %v", host, a, err)
		}
	}
	return nil
}
