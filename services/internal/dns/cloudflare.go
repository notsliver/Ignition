// Package dns talks to DNS providers so a domain's records can be created from Ignition.
// Only Cloudflare is supported; its API token needs Zone:Read and DNS:Edit.
package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var BaseURL = "https://api.cloudflare.com/client/v4"

var client = &http.Client{Timeout: 10 * time.Second}

type Cloudflare struct{ Token string }

type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func (c Cloudflare) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return errors.New("Cloudflare can't be reached")
	}
	defer res.Body.Close()
	var env struct {
		Success bool                       `json:"success"`
		Errors  []struct{ Message string } `json:"errors"`
		Result  json.RawMessage            `json:"result"`
	}
	json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&env)
	if !env.Success {
		msgs := []string{}
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
		}
		if len(msgs) == 0 {
			msgs = append(msgs, res.Status)
		}
		return fmt.Errorf("Cloudflare: %s", strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// Verify checks the token is active.
func (c Cloudflare) Verify(ctx context.Context) error {
	var r struct{ Status string }
	if err := c.do(ctx, "GET", "/user/tokens/verify", nil, &r); err != nil {
		return err
	}
	if r.Status != "active" {
		return fmt.Errorf("the token is %s", r.Status)
	}
	return nil
}

// Zones lists the zones the token can see.
func (c Cloudflare) Zones(ctx context.Context) ([]Zone, error) {
	var out []Zone
	for page := 1; page <= 20; page++ {
		var zs []Zone
		if err := c.do(ctx, "GET", fmt.Sprintf("/zones?per_page=50&page=%d", page), nil, &zs); err != nil {
			return nil, err
		}
		out = append(out, zs...)
		if len(zs) < 50 {
			break
		}
	}
	return out, nil
}

// ZoneFor finds the most specific zone that holds host.
func ZoneFor(zones []Zone, host string) (Zone, bool) {
	best := Zone{}
	for _, z := range zones {
		if (host == z.Name || strings.HasSuffix(host, "."+z.Name)) && len(z.Name) > len(best.Name) {
			best = z
		}
	}
	return best, best.ID != ""
}

// Upsert creates or updates the record of this type and name, DNS only (not proxied by
// Cloudflare, so certificates and routing stay with Ignition).
func (c Cloudflare) Upsert(ctx context.Context, zoneID, typ, name, content string) (Record, error) {
	var existing []Record
	q := url.Values{"type": {typ}, "name": {name}}
	if err := c.do(ctx, "GET", "/zones/"+zoneID+"/dns_records?"+q.Encode(), nil, &existing); err != nil {
		return Record{}, err
	}
	body := map[string]any{"type": typ, "name": name, "content": content, "ttl": 1, "proxied": false, "comment": "Managed by Ignition"}
	var out Record
	if len(existing) > 0 {
		err := c.do(ctx, "PUT", "/zones/"+zoneID+"/dns_records/"+existing[0].ID, body, &out)
		return out, err
	}
	err := c.do(ctx, "POST", "/zones/"+zoneID+"/dns_records", body, &out)
	return out, err
}

func (c Cloudflare) Delete(ctx context.Context, zoneID, recordID string) error {
	err := c.do(ctx, "DELETE", "/zones/"+zoneID+"/dns_records/"+recordID, nil, nil)
	if err != nil && strings.Contains(err.Error(), "not found") {
		return nil
	}
	return err
}
