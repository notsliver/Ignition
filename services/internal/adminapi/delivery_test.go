package adminapi

import (
	"context"
	"encoding/json"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWebhookDestinationBoundaries(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://localhost/hook", "https://127.0.0.1", "https://10.0.0.1", "https://169.254.169.254/latest", "https://[::1]", "https://user:password@example.com", "https://example.com/#fragment"} {
		if ValidateDestination(u) == nil {
			t.Errorf("accepted %s", u)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.1.1.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "198.18.0.1"} {
		if PublicAddress(net.ParseIP(ip)) {
			t.Errorf("allowed %s", ip)
		}
	}
	if e := ValidateDestination("https://hooks.example.com/ignition"); e != nil {
		t.Fatal(e)
	}
	if Signature("secret", "123", []byte("payload")) == Signature("secret", "124", []byte("payload")) {
		t.Fatal("timestamp was not authenticated")
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDurableSignedDelivery(t *testing.T) {
	dsn := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("IGNITION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, e := database.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if e = database.Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	st := &store.Store{DB: pool}
	box, _ := secrets.New(make([]byte, 32))
	u, e := st.CreateUser(ctx, "delivery-"+time.Now().Format("150405.000000000")+"@test.example", "Delivery", "hash", "admin")
	if e != nil {
		t.Fatal(e)
	}
	var webhook, event, delivery string
	pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&webhook)
	sealed, _ := box.Seal("test-signing-secret", "admin-webhook:"+webhook)
	if _, e = pool.Exec(ctx, `INSERT INTO admin_webhooks(id,created_by,url,events,secret_enc) VALUES($1,$2,'https://hooks.example.com/test',ARRAY['resource.provisioned'],$3)`, webhook, u.ID, sealed); e != nil {
		t.Fatal(e)
	}
	pool.QueryRow(ctx, `INSERT INTO admin_webhook_events(type,payload) VALUES('resource.provisioned','{"resourceId":"test"}') RETURNING id`).Scan(&event)
	pool.QueryRow(ctx, `INSERT INTO admin_webhook_deliveries(webhook_id,event_id) VALUES($1,$2) RETURNING id`, webhook, event).Scan(&delivery)
	original := newDeliveryClient
	defer func() { newDeliveryClient = original }()
	calls := 0
	newDeliveryClient = func() *http.Client {
		return &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			payload, _ := io.ReadAll(r.Body)
			if r.Header.Get("X-Ignition-Event-Id") != event {
				t.Fatal("unstable event id")
			}
			if r.Header.Get("X-Ignition-Signature") != "v1="+Signature("test-signing-secret", r.Header.Get("X-Ignition-Timestamp"), payload) {
				t.Fatal("invalid signature")
			}
			var body map[string]any
			if json.Unmarshal(payload, &body) != nil || body["type"] != "resource.provisioned" {
				t.Fatal(string(payload))
			}
			code := 500
			if calls > 1 {
				code = 204
			}
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("private response body must not be stored")), Header: make(http.Header)}, nil
		})}
	}
	if e = deliverOne(ctx, st, box, webhook); e != nil {
		t.Fatal(e)
	}
	var status, problem string
	var attempts int
	pool.QueryRow(ctx, `SELECT status,last_error,attempts FROM admin_webhook_deliveries WHERE id=$1`, delivery).Scan(&status, &problem, &attempts)
	if status != "pending" || attempts != 1 || strings.Contains(problem, "private response") {
		t.Fatal(status, attempts, problem)
	}
	pool.Exec(ctx, `UPDATE admin_webhook_deliveries SET next_attempt_at=now() WHERE id=$1`, delivery)
	if e = deliverOne(ctx, st, box, webhook); e != nil {
		t.Fatal(e)
	}
	pool.QueryRow(ctx, `SELECT status,attempts FROM admin_webhook_deliveries WHERE id=$1`, delivery).Scan(&status, &attempts)
	if status != "delivered" || attempts != 2 || calls != 2 {
		t.Fatal(status, attempts, calls)
	}
}
