package worker

import "testing"

func TestValidateGitSource(t *testing.T) {
	ok := [][2]string{
		{"https://github.com/traefik/whoami", "master"},
		{"https://gitlab.com/group/sub/project.git", "release/2.1"},
	}
	for _, c := range ok {
		if err := ValidateGitSource(c[0], c[1]); err != nil {
			t.Errorf("%v should be valid: %v", c, err)
		}
	}
	bad := [][2]string{
		{"http://github.com/a/b", "main"},             // plain http
		{"file:///etc/passwd", "main"},                // local path
		{"https://github.com/a/b", "--upload-pack=x"}, // option injection
		{"https://github.com/a/b", "-x"},
		{"https://github.com/../etc", "main"},
		{"https://github.com/a/b; rm -rf /", "main"},
		{"ext::sh -c id", "main"},
		{"https://github.com/a/b", "a..b"},
	}
	for _, c := range bad {
		if err := ValidateGitSource(c[0], c[1]); err == nil {
			t.Errorf("%v should be rejected", c)
		}
	}
}

func TestSafeDockerfile(t *testing.T) {
	for _, p := range []string{"Dockerfile", "docker/Dockerfile.prod", "./Dockerfile"} {
		if _, err := SafeDockerfile(p); err != nil {
			t.Errorf("%q should be allowed: %v", p, err)
		}
	}
	for _, p := range []string{"../Dockerfile", "/etc/passwd", "a/../../b", ".", ".."} {
		if _, err := SafeDockerfile(p); err == nil {
			t.Errorf("%q should be rejected", p)
		}
	}
}
