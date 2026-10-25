package worker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ignition/ignition/internal/jobs"
	"github.com/ignition/ignition/internal/sources"
)

var (
	repoRE   = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~/-]+$`)
	branchRE = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

// ValidateGitSource rejects anything that could be read as a git option or a local path.
func ValidateGitSource(repo, branch string) error {
	if !repoRE.MatchString(repo) || strings.Contains(repo, "..") {
		return fmt.Errorf("%w: repository must be a public https URL, e.g. https://github.com/owner/repo", jobs.ErrPermanent)
	}
	if !branchRE.MatchString(branch) || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") {
		return fmt.Errorf("%w: invalid branch name %q", jobs.ErrPermanent, branch)
	}
	return nil
}

// ValidateGitSourceFor is ValidateGitSource, but also accepts SSH repository URLs when an SSH key is used.
func ValidateGitSourceFor(repo, branch string, ssh bool) error {
	if ssh && sources.ValidSSHRepo(repo) {
		return ValidateGitSource("https://placeholder/x", branch)
	}
	return ValidateGitSource(repo, branch)
}

// cloneRepo does a shallow clone with git invoked directly (no shell) and returns the commit SHA.
// creds is nil for public repositories.
func cloneRepo(ctx context.Context, repo, branch, dir string, creds *sources.Credentials) (string, error) {
	if err := ValidateGitSourceFor(repo, branch, creds != nil && creds.AuthType == "ssh"); err != nil {
		return "", err
	}
	var authArgs, authEnv []string
	if creds != nil {
		var cleanup func()
		var err error
		if authArgs, authEnv, cleanup, err = sources.GitAuth(*creds); err != nil {
			return "", err
		}
		defer cleanup()
	}
	if creds != nil {
		repo = sources.RepoURLFor(*creds, repo)
	}
	cmd := exec.CommandContext(ctx, "git", append(authArgs, "clone", "--depth", "1", "--single-branch", "--branch", branch, "--", repo, dir)...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), authEnv...) // fail instead of prompting
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "not found") || strings.Contains(msg, "could not read Username") || strings.Contains(msg, "Remote branch") || strings.Contains(msg, "Authentication failed") || strings.Contains(msg, "Permission denied") {
			return "", fmt.Errorf("%w: git clone failed: %s", jobs.ErrPermanent, msg)
		}
		return "", fmt.Errorf("git clone failed: %s", msg)
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--short=7", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// SafeDockerfile keeps the Dockerfile path inside the build context.
func SafeDockerfile(p string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || filepath.IsAbs(p) || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("%w: Dockerfile path must be inside the repository", jobs.ErrPermanent)
	}
	return clean, nil
}

// tarDir packs the build context, skipping .git and refusing symlinks that escape the directory.
func tarDir(dir string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
			target := filepath.Join(filepath.Dir(path), link)
			if r, _ := filepath.Rel(dir, target); strings.HasPrefix(r, "..") {
				return nil // skip links pointing outside the context
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > 500<<20 {
		return nil, errors.New("build context is larger than 500 MB")
	}
	return &buf, nil
}
