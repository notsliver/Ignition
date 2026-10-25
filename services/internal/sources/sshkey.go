package sources

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// GenerateSSHKey creates an Ed25519 key pair. It returns the private key in OpenSSH format and the
// public key as an authorized_keys line, ready to paste as a deploy key.
func GenerateSSHKey(comment string) (private, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(block)), authorizedKey(sshPub, comment), nil
}

// SSHPublicKey derives the public key and its fingerprint from a private key.
func SSHPublicKey(private, comment string) (public, fingerprint string, err error) {
	signer, err := ssh.ParsePrivateKey([]byte(strings.ReplaceAll(strings.TrimSpace(private), "\r\n", "\n") + "\n"))
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return "", "", errors.New("Keys with a passphrase aren't supported. Use a key without one, or let Ignition generate one.")
	}
	if err != nil {
		return "", "", errors.New("That private key can't be read.")
	}
	return authorizedKey(signer.PublicKey(), comment), ssh.FingerprintSHA256(signer.PublicKey()), nil
}

// RepoURLFor returns the URL git should use with these credentials. SSH keys only work over SSH, so an
// https URL (as the GitHub and GitLab forms produce) becomes git@host:owner/repo.git.
func RepoURLFor(c Credentials, repo string) string {
	if c.AuthType != "ssh" || !strings.HasPrefix(repo, "https://") {
		return repo
	}
	host, path, ok := strings.Cut(strings.TrimPrefix(repo, "https://"), "/")
	if !ok || path == "" {
		return repo
	}
	if h, _, hasPort := strings.Cut(host, ":"); hasPort {
		host = h // the https port says nothing about the SSH port; use the default
	}
	return "git@" + host + ":" + strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git") + ".git"
}

// Branch is one head reported by the remote.
type Branch struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

// lsRemote runs git ls-remote with the given flags (no clone) and returns ref → commit.
func lsRemote(ctx context.Context, c Credentials, repo string, flags ...string) (map[string]string, error) {
	args, env, cleanup, err := GitAuth(c)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, "git", append(append(append(args, "ls-remote"), flags...), "--", RepoURLFor(c, repo))...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "could not read Username") {
			return nil, errors.New("the repository is private or doesn't exist. Choose credentials that can read it")
		}
		if c.AuthType == "ssh" && strings.Contains(msg, "Permission denied") {
			return nil, errors.New("the server refused the SSH key. Add its public key to the repository as a deploy key")
		}
		return nil, errors.New(gitReason(msg))
	}
	refs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if sha, ref, ok := strings.Cut(line, "\t"); ok {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// Branches lists the remote's branches with their latest commits.
func Branches(ctx context.Context, c Credentials, repo string) ([]Branch, error) {
	refs, err := lsRemote(ctx, c, repo, "--heads")
	if err != nil {
		return nil, err
	}
	list := []Branch{}
	for ref, sha := range refs {
		if strings.HasPrefix(ref, "refs/heads/") {
			list = append(list, Branch{Name: strings.TrimPrefix(ref, "refs/heads/"), Commit: sha})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// Tag is a release tag: a semantic version like v1.2.3 or v1.3.0-beta.1.
type Tag struct {
	Name       string `json:"name"`
	Commit     string `json:"commit"` // the commit the tag points at (annotated tags are peeled)
	Prerelease bool   `json:"prerelease"`
	semver     [3]int
	pre        string
}

var semverRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

// Tags lists the remote's semantic-version tags, newest first.
func Tags(ctx context.Context, c Credentials, repo string) ([]Tag, error) {
	refs, err := lsRemote(ctx, c, repo, "--tags")
	if err != nil {
		return nil, err
	}
	var list []Tag
	for ref, sha := range refs {
		name, ok := strings.CutPrefix(ref, "refs/tags/")
		if !ok || strings.HasSuffix(name, "^{}") {
			continue
		}
		m := semverRE.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if peeled, ok := refs[ref+"^{}"]; ok {
			sha = peeled
		}
		t := Tag{Name: name, Commit: sha, Prerelease: m[4] != "", pre: m[4]}
		for i := 0; i < 3; i++ {
			t.semver[i], _ = strconv.Atoi(m[i+1])
		}
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return newer(list[i], list[j]) })
	return list, nil
}

// newer reports whether a is a later version than b (a release outranks its own prereleases).
func newer(a, b Tag) bool {
	for i := 0; i < 3; i++ {
		if a.semver[i] != b.semver[i] {
			return a.semver[i] > b.semver[i]
		}
	}
	if (a.pre == "") != (b.pre == "") {
		return a.pre == ""
	}
	return a.pre > b.pre
}

func authorizedKey(k ssh.PublicKey, comment string) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
	if comment != "" {
		line += " " + comment
	}
	return line
}
