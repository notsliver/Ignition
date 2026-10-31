package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZoneFor(t *testing.T) {
	zones := []Zone{{"1", "example.com"}, {"2", "eu.example.com"}, {"3", "other.org"}}
	if z, ok := ZoneFor(zones, "api.eu.example.com"); !ok || z.ID != "2" {
		t.Fatalf("most specific zone: %+v", z)
	}
	if z, ok := ZoneFor(zones, "example.com"); !ok || z.ID != "1" {
		t.Fatalf("apex: %+v", z)
	}
	if _, ok := ZoneFor(zones, "badexample.com"); ok {
		t.Fatal("suffix must be a whole label")
	}
}

func TestUpsertCreatesThenUpdates(t *testing.T) {
	records := map[string]Record{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]string{"message": "Invalid API Token"}}})
			return
		}
		var result any
		switch {
		case r.Method == "GET":
			list := []Record{}
			for _, rec := range records {
				if rec.Name == r.URL.Query().Get("name") && rec.Type == r.URL.Query().Get("type") {
					list = append(list, rec)
				}
			}
			result = list
		case r.Method == "POST" || r.Method == "PUT":
			var in Record
			json.NewDecoder(r.Body).Decode(&in)
			if in.Proxied {
				t.Error("records must not be proxied by Cloudflare")
			}
			in.ID = "rec1"
			records[in.ID] = in
			result = in
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	defer srv.Close()
	BaseURL = srv.URL
	c := Cloudflare{Token: "tok"}
	r1, err := c.Upsert(context.Background(), "z", "A", "app.example.com", "203.0.113.7")
	if err != nil || r1.ID != "rec1" {
		t.Fatal(r1, err)
	}
	r2, err := c.Upsert(context.Background(), "z", "A", "app.example.com", "203.0.113.8")
	if err != nil || r2.Content != "203.0.113.8" || len(records) != 1 {
		t.Fatal(r2, err, records)
	}
	if _, err := (Cloudflare{Token: "bad"}).Upsert(context.Background(), "z", "A", "x", "1.1.1.1"); err == nil || !strings.Contains(err.Error(), "Invalid API Token") {
		t.Fatal("errors carry Cloudflare's message:", err)
	}
}
