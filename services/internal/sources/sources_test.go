package sources

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestURLValidation(t *testing.T) {
	for _, u := range []string{"https://github.com/acme/app", "https://git.example.com:8443/team/app.git", "https://gitlab.example.com"} {
		if !ValidHTTPURL(u) {
			t.Errorf("%q should be valid", u)
		}
	}
	for _, u := range []string{"http://github.com/a/b", "https://user:pass@github.com/a/b", "file:///etc", "https://a/../b", "ext::sh -c id"} {
		if ValidHTTPURL(u) {
			t.Errorf("%q should be rejected", u)
		}
	}
	for _, u := range []string{"git@github.com:acme/app.git", "ssh://git@git.example.com:2222/team/app.git"} {
		if !ValidSSHRepo(u) {
			t.Errorf("%q should be valid", u)
		}
	}
	for _, u := range []string{"-oProxyCommand=id@x:y", "git@host:../../etc", "github.com/acme/app"} {
		if ValidSSHRepo(u) {
			t.Errorf("%q should be rejected", u)
		}
	}
	if !ValidRegistryHost("registry.example.com:5000") || ValidRegistryHost("https://registry.example.com") {
		t.Error("registry hosts are host[:port] without a scheme")
	}
}

func TestTokenAuthUsesAHeaderNotTheURL(t *testing.T) {
	args, env, cleanup, err := GitAuth(Credentials{Provider: "github", AuthType: "token", Secret: "ghp_secret"})
	defer cleanup()
	if err != nil || len(env) != 0 || len(args) != 2 {
		t.Fatalf("unexpected: %v %v %v", args, env, err)
	}
	header := strings.TrimPrefix(args[1], "http.extraHeader=Authorization: Basic ")
	decoded, _ := base64.StdEncoding.DecodeString(header)
	if string(decoded) != "x-access-token:ghp_secret" {
		t.Fatalf("got %q", decoded)
	}
}

func TestSSHKeyIsWrittenPrivatelyAndCleanedUp(t *testing.T) {
	_, env, cleanup, err := GitAuth(Credentials{AuthType: "ssh", Secret: "-----BEGIN OPENSSH PRIVATE KEY-----\r\nabc\r\n-----END OPENSSH PRIVATE KEY-----"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := strings.TrimPrefix(env[0], "GIT_SSH_COMMAND=")
	key := strings.Fields(cmd)[2]
	info, err := os.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file must be 0600, got %v", info.Mode().Perm())
	}
	body, _ := os.ReadFile(key)
	if strings.Contains(string(body), "\r") || !strings.HasSuffix(string(body), "\n") {
		t.Fatal("key must use LF line endings and end with a newline")
	}
	if !strings.Contains(cmd, "BatchMode=yes") {
		t.Fatal("ssh must never prompt")
	}
	cleanup()
	if _, err := os.Stat(key); !os.IsNotExist(err) {
		t.Fatal("key file must be removed by cleanup")
	}
}

func TestRegistryAddresses(t *testing.T) {
	cases := map[string][2]string{
		"dockerhub":       {"https://index.docker.io/v1/", "docker.io"},
		"ghcr":            {"ghcr.io", "ghcr.io"},
		"gitlab-registry": {"registry.gitlab.com", "registry.gitlab.com"},
	}
	for p, want := range cases {
		c := Credentials{Provider: p}
		if RegistryAddress(c) != want[0] || RegistryHost(c) != want[1] {
			t.Errorf("%s: got %s / %s", p, RegistryAddress(c), RegistryHost(c))
		}
	}
	c := Credentials{Provider: "registry", URL: "registry.example.com:5000", Username: "u", Secret: "p"}
	var auth map[string]string
	raw, _ := base64.URLEncoding.DecodeString(RegistryAuthHeader(c))
	json.Unmarshal(raw, &auth)
	if auth["serveraddress"] != "registry.example.com:5000" || auth["password"] != "p" {
		t.Fatalf("bad X-Registry-Auth: %v", auth)
	}
}

func TestGeneratedKeyRoundTrips(t *testing.T) {
	priv, pub, err := GenerateSSHKey("ignition-test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasSuffix(pub, " ignition-test") || !strings.Contains(priv, "OPENSSH PRIVATE KEY") {
		t.Fatalf("unexpected key format: %q", pub)
	}
	derived, fp, err := SSHPublicKey(priv, "ignition-test")
	if err != nil || derived != pub || !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("derived %q %q %v", derived, fp, err)
	}
	if _, _, err := SSHPublicKey("-----BEGIN OPENSSH PRIVATE KEY-----\nnope\n-----END OPENSSH PRIVATE KEY-----", ""); err == nil {
		t.Fatal("garbage must be rejected")
	}
}

func TestSSHKeysUseSSHURLs(t *testing.T) {
	ssh := Credentials{AuthType: "ssh"}
	cases := map[string]string{
		"https://github.com/acme/app":             "git@github.com:acme/app.git",
		"https://gitlab.example.com:8443/t/a.git": "git@gitlab.example.com:t/a.git",
		"git@github.com:acme/app.git":             "git@github.com:acme/app.git",
	}
	for in, want := range cases {
		if got := RepoURLFor(ssh, in); got != want || !ValidSSHRepo(got) {
			t.Errorf("%s: got %s", in, got)
		}
	}
	if got := RepoURLFor(Credentials{AuthType: "token"}, "https://github.com/acme/app"); got != "https://github.com/acme/app" {
		t.Errorf("tokens keep https, got %s", got)
	}
}

func TestTagOrder(t *testing.T) {
	mk := func(name string) Tag {
		m := semverRE.FindStringSubmatch(name)
		tag := Tag{Name: name, pre: m[4], Prerelease: m[4] != ""}
		for i := 0; i < 3; i++ {
			fmt.Sscan(m[i+1], &tag.semver[i])
		}
		return tag
	}
	order := []string{"v1.10.0", "v1.10.0-rc.2", "v1.10.0-beta.1", "v1.9.3", "v1.2.0", "0.9.0"}
	for i := 0; i+1 < len(order); i++ {
		if !newer(mk(order[i]), mk(order[i+1])) {
			t.Errorf("%s should be newer than %s", order[i], order[i+1])
		}
	}
	if semverRE.MatchString("latest") || !semverRE.MatchString("v2.0.0-beta.3") {
		t.Error("only semantic versions are release tags")
	}
}
