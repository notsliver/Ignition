package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/notsliver/Ignition/services/internal/adminapi"
	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

func (s *Server) publicWebhooks(w http.ResponseWriter, r *http.Request, u *store.User, t *adminToken, a string) error {
	ctx := r.Context()
	id := r.PathValue("id")
	read := func(id string) (map[string]any, error) {
		var destination string
		var events []string
		var enabled bool
		err := s.Store.SQL(ctx).QueryRow(ctx, `SELECT url,events,enabled FROM admin_webhooks WHERE id=$1`, id).Scan(&destination, &events, &enabled)
		if err != nil {
			return nil, httpx.NotFound("Webhook")
		}
		return map[string]any{"id": id, "url": destination, "events": events, "enabled": enabled}, nil
	}
	if a == "webhooks" {
		rows, e := s.Store.SQL(ctx).Query(ctx, `SELECT id,url,events,enabled FROM admin_webhooks ORDER BY id`)
		if e != nil {
			return e
		}
		list := []map[string]any{}
		for rows.Next() {
			var id, url string
			var events []string
			var enabled bool
			if e = rows.Scan(&id, &url, &events, &enabled); e != nil {
				rows.Close()
				return e
			}
			list = append(list, map[string]any{"id": id, "url": url, "events": events, "enabled": enabled})
		}
		rows.Close()
		return publicList(w, r, list)
	}
	if a == "webhook-create" {
		var in struct {
			URL     string   `json:"url"`
			Events  []string `json:"events"`
			Enabled *bool    `json:"enabled"`
		}
		if e := httpx.Decode(r, &in); e != nil {
			return e
		}
		if e := adminapi.ValidateDestination(in.URL); e != nil {
			return httpx.Invalid(map[string]string{"url": e.Error()})
		}
		if len(in.Events) == 0 {
			return httpx.Invalid(map[string]string{"events": "Select at least one lifecycle event."})
		}
		for _, v := range in.Events {
			if !slices.Contains(adminapi.Events, v) {
				return httpx.Invalid(map[string]string{"events": "Choose a supported lifecycle event."})
			}
		}
		if e := s.Store.SQL(ctx).QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&id); e != nil {
			return e
		}
		raw, _, e := auth.NewToken()
		if e != nil {
			return e
		}
		secret, e := s.Secrets.Seal(raw, "admin-webhook:"+id)
		if e != nil {
			return e
		}
		enabled := in.Enabled == nil || *in.Enabled
		_, e = s.Store.SQL(ctx).Exec(ctx, `INSERT INTO admin_webhooks(id,created_by,url,events,enabled,secret_enc) VALUES($1,$2,$3,$4,$5,$6)`, id, u.ID, in.URL, in.Events, enabled, secret)
		if e != nil {
			return e
		}
		row, e := read(id)
		if e != nil {
			return e
		}
		row["signingSecret"] = raw
		publicCreated(w, row)
		return nil
	}
	row, e := read(id)
	if e != nil {
		return e
	}
	switch a {
	case "webhook":
		publicOK(w, row)
		return nil
	case "webhook-update":
		var in struct {
			URL     *string   `json:"url"`
			Events  *[]string `json:"events"`
			Enabled *bool     `json:"enabled"`
		}
		if e = httpx.Decode(r, &in); e != nil {
			return e
		}
		destination := row["url"].(string)
		events := row["events"].([]string)
		enabled := row["enabled"].(bool)
		if in.URL != nil {
			destination = *in.URL
		}
		if in.Events != nil {
			events = *in.Events
		}
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		if e = adminapi.ValidateDestination(destination); e != nil {
			return httpx.Invalid(map[string]string{"url": e.Error()})
		}
		if len(events) == 0 {
			return httpx.Invalid(map[string]string{"events": "Choose at least one event."})
		}
		for _, v := range events {
			if !slices.Contains(adminapi.Events, v) {
				return httpx.Invalid(map[string]string{"events": "Unknown lifecycle event."})
			}
		}
		_, e = s.Store.SQL(ctx).Exec(ctx, `UPDATE admin_webhooks SET url=$2,events=$3,enabled=$4 WHERE id=$1`, id, destination, events, enabled)
		if e != nil {
			return e
		}
		out, e := read(id)
		if e != nil {
			return e
		}
		publicOK(w, out)
		return nil
	case "webhook-delete":
		if e = publicEmpty(r); e != nil {
			return e
		}
		_, e = s.Store.SQL(ctx).Exec(ctx, `DELETE FROM admin_webhooks WHERE id=$1`, id)
		if e == nil {
			w.WriteHeader(204)
		}
		return e
	case "webhook-test":
		if e = publicEmpty(r); e != nil {
			return e
		}
		if !row["enabled"].(bool) {
			return httpx.Conflict("Enable this subscription before testing.")
		}
		var event, delivery string
		e = s.Store.SQL(ctx).QueryRow(ctx, `INSERT INTO admin_webhook_events(type,payload) VALUES('webhook.test','{}') RETURNING id`).Scan(&event)
		if e != nil {
			return e
		}
		e = s.Store.SQL(ctx).QueryRow(ctx, `INSERT INTO admin_webhook_deliveries(webhook_id,event_id) VALUES($1,$2) RETURNING id`, id, event).Scan(&delivery)
		if e != nil {
			return e
		}
		httpx.JSON(w, 202, map[string]any{"data": map[string]string{"deliveryId": delivery}})
		return nil
	case "deliveries":
		rows, e := s.Store.SQL(ctx).Query(ctx, `SELECT id,event_id,status,attempts,response_status,last_error,created_at FROM admin_webhook_deliveries WHERE webhook_id=$1 ORDER BY created_at,id`, id)
		if e != nil {
			return e
		}
		list := []map[string]any{}
		for rows.Next() {
			var delivery, event, status, problem string
			var attempts int
			var code *int
			var created any
			if e = rows.Scan(&delivery, &event, &status, &attempts, &code, &problem, &created); e != nil {
				rows.Close()
				return e
			}
			list = append(list, map[string]any{"id": delivery, "eventId": event, "status": status, "attempts": attempts, "responseStatus": code, "error": problem, "createdAt": created})
		}
		rows.Close()
		return publicList(w, r, list)
	case "delivery-retry":
		if e = publicEmpty(r); e != nil {
			return e
		}
		tag, e := s.Store.SQL(ctx).Exec(ctx, `UPDATE admin_webhook_deliveries SET status='pending',attempts=0,next_attempt_at=now(),leased_until=NULL WHERE id=$1 AND webhook_id=$2 AND status IN ('failed','delivered') AND (leased_until IS NULL OR leased_until<now())`, r.PathValue("deliveryId"), id)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("Delivery not found or already pending.")
		}
		httpx.JSON(w, 202, map[string]any{"data": map[string]string{"deliveryId": r.PathValue("deliveryId")}})
		return nil
	}
	return httpx.NotFound(strings.TrimPrefix(a, "webhook-"))
}
