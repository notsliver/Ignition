package sources

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"sort"
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

// Branches lists the remote's branches with their latest commits, using git ls-remote (no clone).
func Branches(ctx context.Context, c Credentials, repo string) ([]Branch, error) {
	args, env, cleanup, err := GitAuth(c)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, "git", append(args, "ls-remote", "--heads", "--", RepoURLFor(c, repo))...)
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
	list := []Branch{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		sha, ref, ok := strings.Cut(line, "\t")
		if ok && strings.HasPrefix(ref, "refs/heads/") {
			list = append(list, Branch{Name: strings.TrimPrefix(ref, "refs/heads/"), Commit: sha})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func authorizedKey(k ssh.PublicKey, comment string) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
	if comment != "" {
		line += " " + comment
	}
	return line
}
