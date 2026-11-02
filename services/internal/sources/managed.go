package sources

import (
	"errors"
	"net/url"
	"strings"
)

// Managed tokens must never authenticate against a caller-selected host or an SSH URL.
func ValidateManagedRepository(c Credentials, repo string) error {
	if !c.Managed {
		return nil
	}
	u, err := url.Parse(repo)
	if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(u.Host, c.GitHost) || !ValidHTTPURL(repo) {
		return errors.New("this OAuth source can only access HTTPS repositories on its configured provider host")
	}
	return nil
}
