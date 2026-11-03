// Package adminapi maintains durable public operation outcomes and signed webhook delivery.
package adminapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
)

var Events = []string{"resource.provisioned", "resource.provisioning_failed", "resource.suspended", "resource.resumed", "resource.plan_changed", "resource.terminated", "operation.failed"}

func PublicAddress(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return false
	}
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return true
}
func ValidateDestination(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(raw) > 2048 {
		return errors.New("use an HTTPS URL without credentials or fragments")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if (net.ParseIP(host) == nil && !strings.Contains(host, ".")) || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("webhooks require a public hostname")
	}
	if ip := net.ParseIP(host); ip != nil && !PublicAddress(ip) {
		return errors.New("webhooks cannot target private or reserved addresses")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return errors.New("invalid HTTPS port")
		}
	}
	return nil
}
func deliveryClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are not allowed") }, Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		if len(ips) == 0 {
			return nil, errors.New("no destination addresses")
		}
		for _, ip := range ips {
			if !PublicAddress(ip.IP) {
				return nil, errors.New("destination resolved to a private or reserved address")
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}}}
}
func Signature(secret, timestamp string, payload []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(timestamp + "."))
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}
func Run(ctx context.Context, st *store.Store, box *secrets.Box) {
	ticks := 0
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ticks++
			if ticks%60 == 0 {
				st.DB.Exec(ctx, `DELETE FROM admin_idempotency WHERE expires_at<now()`)
				st.DB.Exec(ctx, `DELETE FROM admin_setup_links WHERE expires_at<now()-interval '1 day'`)
			}
			if e := Reconcile(ctx, st); e != nil {
				slog.Error("admin operations reconciliation failed", "error", e)
			}
			if e := DeliverOne(ctx, st, box); e != nil {
				slog.Error("admin webhook delivery failed", "error", e)
			}
		}
	}
}
func Reconcile(ctx context.Context, st *store.Store) error {
	tx, e := st.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT o.id,o.resource_id,o.type,o.details,coalesce(j.status,CASE WHEN o.job_id IS NULL THEN 'succeeded' WHEN o.type='resource.delete' AND NOT EXISTS(SELECT 1 FROM resources WHERE id=o.resource_id) THEN 'succeeded' ELSE 'failed' END),j.last_error
 FROM admin_operations o LEFT JOIN jobs j ON j.id=o.job_id WHERE o.status IN ('queued','running') ORDER BY o.created_at LIMIT 50 FOR UPDATE OF o SKIP LOCKED`)
	if e != nil {
		return e
	}
	type op struct {
		ID, Resource, Kind, State string
		Details                   []byte
		Error                     *string
	}
	list := []op{}
	for rows.Next() {
		var o op
		if e = rows.Scan(&o.ID, &o.Resource, &o.Kind, &o.Details, &o.State, &o.Error); e != nil {
			rows.Close()
			return e
		}
		list = append(list, o)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, o := range list {
		if o.State == "pending" || o.State == "running" {
			if _, e = tx.Exec(ctx, `UPDATE admin_operations SET status=$2 WHERE id=$1`, o.ID, map[bool]string{true: "running", false: "queued"}[o.State == "running"]); e != nil {
				return e
			}
			continue
		}
		status := o.State
		if status != "succeeded" && status != "cancelled" {
			status = "failed"
		}
		// Completion means the observed lifecycle outcome, not merely successful queue insertion.
		if status == "succeeded" && (o.Kind == "resource.resume" || o.Kind == "resource.start" || o.Kind == "resource.restart" || o.Kind == "resource.redeploy" || o.Kind == "resource.provision" || o.Kind == "resource.change-plan") {
			var state string
			if e := tx.QueryRow(ctx, `SELECT status FROM resources WHERE id=$1`, o.Resource).Scan(&state); e != nil || state != "running" {
				status = "failed"
			}
		}

		if status == "succeeded" && o.Kind == "resource.suspend" {
			var running bool
			tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM resources WHERE id=$1 AND status='running')`, o.Resource).Scan(&running)
			if running {
				status = "failed"
			}
		}
		if status == "failed" || status == "cancelled" {
			message := "The operation did not complete successfully. Inspect its deployment or resource with the corresponding permission."
			o.Error = &message
		}
		if _, e = tx.Exec(ctx, `UPDATE admin_operations SET status=$2,error=$3,completed_at=now() WHERE id=$1`, o.ID, status, o.Error); e != nil {
			return e
		}
		if status == "succeeded" && o.Kind == "resource.delete" {
			if _, e = tx.Exec(ctx, `UPDATE services SET status='terminated',updated_at=now() WHERE original_resource_id=$1`, o.Resource); e != nil {
				return e
			}
		}
		event := ""
		if status == "failed" || status == "cancelled" {
			event = "operation.failed"
			if o.Kind == "resource.provision" {
				event = "resource.provisioning_failed"
			}
		} else {
			event = map[string]string{"resource.provision": "resource.provisioned", "resource.suspend": "resource.suspended", "resource.resume": "resource.resumed", "resource.change-plan": "resource.plan_changed", "resource.delete": "resource.terminated"}[o.Kind]
		}
		if event != "" {
			payload, _ := json.Marshal(map[string]any{"operationId": o.ID, "resourceId": o.Resource, "status": status})
			var eventID string
			e = tx.QueryRow(ctx, `INSERT INTO admin_webhook_events(operation_id,type,payload) VALUES($1,$2,$3) ON CONFLICT(operation_id) DO UPDATE SET operation_id=excluded.operation_id RETURNING id`, o.ID, event, payload).Scan(&eventID)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `INSERT INTO admin_webhook_deliveries(webhook_id,event_id) SELECT id,$1 FROM admin_webhooks WHERE enabled AND $2=ANY(events) ON CONFLICT DO NOTHING`, eventID, event); e != nil {
				return e
			}
		}
	}
	return tx.Commit(ctx)
}

var newDeliveryClient = deliveryClient

func DeliverOne(ctx context.Context, st *store.Store, box *secrets.Box) error {
	return deliverOne(ctx, st, box, "")
}
func deliverOne(ctx context.Context, st *store.Store, box *secrets.Box, onlyWebhook string) error {
	var id, webhook, event, kind, destination string
	var secret, raw []byte
	var attempts int
	var created time.Time
	e := st.DB.QueryRow(ctx, `UPDATE admin_webhook_deliveries SET leased_until=now()+interval '45 seconds',attempts=attempts+1 WHERE id=(
 SELECT d.id FROM admin_webhook_deliveries d JOIN admin_webhooks w ON w.id=d.webhook_id JOIN users u ON u.id=w.created_by WHERE ($1='' OR w.id::text=$1) AND d.status='pending' AND d.next_attempt_at<=now() AND (d.leased_until IS NULL OR d.leased_until<now()) AND w.enabled AND u.status='active' AND u.role IN ('owner','admin') AND NOT(coalesce(u.permissions->'deny','[]') ? 'webhooks.manage') ORDER BY d.created_at LIMIT 1 FOR UPDATE OF d SKIP LOCKED) RETURNING id,webhook_id,event_id,attempts`, onlyWebhook).Scan(&id, &webhook, &event, &attempts)
	if e != nil {
		if strings.Contains(e.Error(), "no rows") {
			return nil
		}
		return e
	}
	e = st.DB.QueryRow(ctx, `SELECT w.url,w.secret_enc,e.type,e.payload,e.created_at FROM admin_webhooks w JOIN admin_webhook_events e ON e.id=$2 WHERE w.id=$1`, webhook, event).Scan(&destination, &secret, &kind, &raw, &created)
	if e != nil {
		return e
	}
	payload, _ := json.Marshal(map[string]any{"id": event, "type": kind, "createdAt": created, "data": json.RawMessage(raw)})
	code := 0
	problem := ""
	plain, e := box.Open(secret, "admin-webhook:"+webhook)
	if e == nil {
		e = ValidateDestination(destination)
	}
	if e == nil {
		req, _ := http.NewRequestWithContext(ctx, "POST", destination, strings.NewReader(string(payload)))
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Ignition-Webhooks/1")
		req.Header.Set("X-Ignition-Event-Id", event)
		req.Header.Set("X-Ignition-Timestamp", ts)
		req.Header.Set("X-Ignition-Signature", "v1="+Signature(plain, ts, payload))
		client := newDeliveryClient()
		resp, err := client.Do(req)
		client.CloseIdleConnections()
		if err == nil {
			code = resp.StatusCode
			io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
			resp.Body.Close()
			if code < 200 || code >= 300 {
				e = fmt.Errorf("destination returned HTTP %d", code)
			}
		} else {
			e = errors.New("destination unavailable or blocked")
		}
	}
	status := "delivered"
	delay := time.Minute * time.Duration(1<<min(attempts, 8))
	if e != nil {
		problem = "Delivery failed; check the public HTTPS destination and signing configuration."
		status = "pending"
		if attempts >= 8 {
			status = "failed"
		}
	}
	_, e = st.DB.Exec(ctx, `UPDATE admin_webhook_deliveries SET status=$2,last_error=$3,response_status=$4,next_attempt_at=now()+$5::interval,leased_until=NULL WHERE id=$1`, id, status, problem, code, fmt.Sprintf("%d seconds", int(delay.Seconds())))
	return e
}
