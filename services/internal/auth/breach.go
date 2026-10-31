package auth

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// BreachURL is the Have I Been Pwned range API. Only the first five hex characters of the
// password's SHA-1 leave the server (k-anonymity); the full hash is compared locally.
var BreachURL = "https://api.pwnedpasswords.com/range/"

var breachClient = &http.Client{Timeout: 4 * time.Second}

// BreachCount returns how often the password appears in public breaches. An error means the
// check couldn't run (offline install, service down); callers decide whether to fail open.
func BreachCount(ctx context.Context, password string) (int, error) {
	sum := sha1.Sum([]byte(password))
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BreachURL+h[:5], nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Add-Padding", "true") // pads the response so its size doesn't hint at the prefix
	req.Header.Set("User-Agent", "Ignition")
	resp, err := breachClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("breach check returned %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		suffix, count, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		if ok && suffix == h[5:] {
			var n int
			fmt.Sscan(count, &n)
			return n, nil
		}
	}
	return 0, sc.Err()
}
