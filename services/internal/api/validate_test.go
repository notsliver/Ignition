package api

import "testing"

func TestResourceValidation(t *testing.T) {
	port := 80
	good := resourceInput{Name: "web", SourceType: "image", Image: "nginx:1.27-alpine", Port: &port}
	if f := good.validate(); len(f) > 0 {
		t.Fatalf("expected valid, got %v", f)
	}
	if good.MemoryMB != 512 || good.CPUs != 1 || good.Type != "application" {
		t.Fatalf("defaults not applied: %+v", good)
	}

	cases := map[string]resourceInput{
		"name":       {Name: "Web App", SourceType: "image", Image: "nginx"},
		"image":      {Name: "web", SourceType: "image", Image: "nginx; rm -rf /"},
		"repository": {Name: "web", SourceType: "git", RepoURL: "git@github.com:a/b.git"},
		"dockerfile": {Name: "web", SourceType: "git", RepoURL: "https://github.com/a/b", Dockerfile: "../x"},
		"port":       {Name: "web", SourceType: "image", Image: "nginx", Port: intp(70000)},
		"memoryMb":   {Name: "web", SourceType: "image", Image: "nginx", MemoryMB: 10},
		"env":        {Name: "web", SourceType: "image", Image: "nginx", Env: []envInput{{Key: "1BAD"}}},
		"sourceType": {Name: "web", SourceType: "ftp"},
	}
	for field, in := range cases {
		if f := in.validate(); f[field] == "" {
			t.Errorf("expected an error on %q, got %v", field, f)
		}
	}
}

func intp(v int) *int { return &v }

func TestImageReferences(t *testing.T) {
	for _, ref := range []string{"nginx", "nginx:1.27", "traefik/whoami:v1.10", "ghcr.io/owner/app:sha-abc", "localhost:5000/team/app:1"} {
		if !imageRE.MatchString(ref) {
			t.Errorf("%q should be accepted", ref)
		}
	}
	for _, ref := range []string{"NGINX", "nginx:", "-rm", "a b", "nginx:tag;id"} {
		if imageRE.MatchString(ref) {
			t.Errorf("%q should be rejected", ref)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2, 60e9)
	if !l.allow("ip") || !l.allow("ip") {
		t.Fatal("first two requests should pass")
	}
	if l.allow("ip") {
		t.Fatal("third request should be limited")
	}
	if !l.allow("other-ip") {
		t.Fatal("limits are per key")
	}
}
