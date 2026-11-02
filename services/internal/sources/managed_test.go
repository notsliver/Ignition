package sources

import (
	"context"
	"strings"
	"testing"
)

func TestManagedGitCredentialsCannotLeaveProviderHost(t *testing.T) {
	c := Credentials{Provider: "github", AuthType: "token", Username: "x-access-token", Secret: "private-token", Managed: true, GitHost: "github.example.test"}
	for _, repo := range []string{"https://attacker.test/app.git", "http://github.example.test/app.git", "https://github.example.test.attacker.test/app.git", "https://user:password@github.example.test/app.git", "git@github.example.test:app.git"} {
		if err := ValidateManagedRepository(c, repo); err == nil {
			t.Fatalf("unsafe managed target accepted: %s", repo)
		}
		if err := LsRemote(context.Background(), c, repo); err == nil {
			t.Fatal("unsafe target reached git")
		}
	}
	if err := ValidateManagedRepository(c, "https://github.example.test/team/app.git"); err != nil {
		t.Fatal(err)
	}
	args, _, cleanup, err := GitAuth(c)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "http.extraHeader=") || !strings.Contains(joined, "http.https://github.example.test/.extraHeader=") || !strings.Contains(joined, "http.followRedirects=false") {
		t.Fatal("managed git authentication was not origin-scoped")
	}
	if apiBase(Credentials{Provider: "github", URL: "https://github.com"}) != "https://api.github.com" {
		t.Fatal("public GitHub incorrectly used the enterprise API")
	}
}
