package store

import (
	"context"
	"encoding/json"
	"time"
)

// Product is something an instance offers, in plans. Template is what a service of it deploys:
// for games a game and spec, otherwise the resource settings (image, engine, env...).
type Product struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Kind        string          `json:"kind"`
	Template    json.RawMessage `json:"template"`
	Icon        string          `json:"icon"`      // "" (automatic), "hero:<Name>:<#color>" or a data: image
	Placement   json.RawMessage `json:"placement"` // where and how many can run; see api.productPlacement
	Status      string          `json:"status"`
	Plans       []Plan          `json:"plans"`
	Services    int             `json:"services"` // not terminated
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type Plan struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	CPUs      float64 `json:"cpus"`
	MemoryMB  int     `json:"memoryMb"`
	StorageGB int     `json:"storageGb"`
	BackupGB  int     `json:"backupGb"`
	Ports     int     `json:"ports"`
	Price     string  `json:"price"`
	Currency  string  `json:"currency"`
	Period    string  `json:"period"`
	Services  int     `json:"services"`
}

// Service is a team's provisioned plan of a product.
type Service struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Status          string    `json:"status"`
	SuspendedReason string    `json:"suspendedReason"`
	Note            string    `json:"note"`
	ProductID       string    `json:"productId"`
	ProductName     string    `json:"productName"`
	Kind            string    `json:"kind"`
	Plan            Plan      `json:"plan"`
	TeamID          string    `json:"teamId"`
	TeamName        string    `json:"teamName"`
	TeamSlug        string    `json:"teamSlug"`
	ResourceID      *string   `json:"resourceId"`
	ResourceName    *string   `json:"resourceName"`
	ResourceStatus  *string   `json:"resourceStatus"`
	CreatedBy       *string   `json:"createdBy"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

const planCols = `pl.id, pl.name, pl.cpus::float8, pl.memory_mb, pl.storage_gb, pl.backup_gb, pl.ports, pl.price, pl.currency, pl.period`

func scanPlan(row interface{ Scan(...any) error }, extra ...any) (Plan, error) {
	var p Plan
	err := row.Scan(append([]any{&p.ID, &p.Name, &p.CPUs, &p.MemoryMB, &p.StorageGB, &p.BackupGB, &p.Ports, &p.Price, &p.Currency, &p.Period}, extra...)...)
	return p, err
}

func (s *Store) plans(ctx context.Context, product string) ([]Plan, error) {
	rows, err := s.TrustedSQL(ctx).Query(ctx, `SELECT `+planCols+`, (SELECT count(*) FROM services sv WHERE sv.plan_id = pl.id AND sv.status <> 'terminated')
		FROM product_plans pl WHERE pl.product_id = $1 ORDER BY pl.position, pl.name`, product)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		var n int
		p, err := scanPlan(rows, &n)
		if err != nil {
			return nil, err
		}
		p.Services = n
		out = append(out, p)
	}
	return out, rows.Err()
}

const productCols = `p.id, p.name, p.description, p.category, p.kind, p.template, p.icon, p.placement, p.status, p.created_at, p.updated_at,
	(SELECT count(*) FROM services sv WHERE sv.product_id = p.id AND sv.status <> 'terminated')`

func (s *Store) Products(ctx context.Context) ([]Product, error) {
	rows, err := s.TrustedSQL(ctx).Query(ctx, `SELECT `+productCols+` FROM products p ORDER BY lower(p.category), lower(p.name)`)
	if err != nil {
		return nil, err
	}
	out := []Product{}
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Category, &p.Kind, &p.Template, &p.Icon, &p.Placement, &p.Status, &p.CreatedAt, &p.UpdatedAt, &p.Services); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	for i := range out {
		if out[i].Plans, err = s.plans(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, rows.Err()
}

func (s *Store) Product(ctx context.Context, id string) (*Product, error) {
	var p Product
	err := s.TrustedSQL(ctx).QueryRow(ctx, `SELECT `+productCols+` FROM products p WHERE p.id = $1`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Category, &p.Kind, &p.Template, &p.Icon, &p.Placement, &p.Status, &p.CreatedAt, &p.UpdatedAt, &p.Services)
	if err != nil {
		return nil, noRows(err)
	}
	p.Plans, err = s.plans(ctx, id)
	return &p, err
}

// SaveProduct creates (id "") or replaces a product and its plans. Plans are matched by ID; one
// that's missing is removed, which fails while services use it.
func (s *Store) SaveProduct(ctx context.Context, id string, p Product, userID string) (string, error) {
	tx, err := s.TrustedSQL(ctx).Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if id == "" {
		err = tx.QueryRow(ctx, `INSERT INTO products (name, description, category, kind, template, status, created_by, icon, placement) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			p.Name, p.Description, p.Category, p.Kind, p.Template, p.Status, nullable(userID), p.Icon, placementOrEmpty(p.Placement)).Scan(&id)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `UPDATE products SET name = $2, description = $3, category = $4, kind = $5, template = $6, status = $7, icon = $8, placement = $9, updated_at = now() WHERE id = $1`,
			id, p.Name, p.Description, p.Category, p.Kind, p.Template, p.Status, p.Icon, placementOrEmpty(p.Placement))
		if err == nil && tag.RowsAffected() == 0 {
			return "", ErrNotFound
		}
	}
	if err != nil {
		return "", err
	}
	keep := []string{}
	for i, pl := range p.Plans {
		if pl.ID != "" {
			tag, err := tx.Exec(ctx, `UPDATE product_plans SET name = $3, position = $4, cpus = $5, memory_mb = $6, storage_gb = $7, backup_gb = $8, ports = $9, price = $10, currency = $11, period = $12
				WHERE id = $1 AND product_id = $2`, pl.ID, id, pl.Name, i, pl.CPUs, pl.MemoryMB, pl.StorageGB, pl.BackupGB, pl.Ports, pl.Price, pl.Currency, pl.Period)
			if err != nil {
				return "", err
			}
			if tag.RowsAffected() == 1 {
				keep = append(keep, pl.ID)
				continue
			}
		}
		var newID string
		if err := tx.QueryRow(ctx, `INSERT INTO product_plans (product_id, name, position, cpus, memory_mb, storage_gb, backup_gb, ports, price, currency, period) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
			id, pl.Name, i, pl.CPUs, pl.MemoryMB, pl.StorageGB, pl.BackupGB, pl.Ports, pl.Price, pl.Currency, pl.Period).Scan(&newID); err != nil {
			return "", err
		}
		keep = append(keep, newID)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_plans WHERE product_id = $1 AND NOT (id::text = ANY($2))`, id, keep); err != nil {
		return "", err // a plan that services still use: foreign key violation
	}
	return id, tx.Commit(ctx)
}

func (s *Store) DeleteProduct(ctx context.Context, id string) error {
	tx, err := s.TrustedSQL(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Terminated services are history: they go with the product.
	if _, err := tx.Exec(ctx, `DELETE FROM services WHERE product_id = $1 AND status = 'terminated'`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM products WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

const serviceCols = `sv.id, sv.name, sv.status, sv.suspended_reason, sv.note, p.id, p.name, p.kind, ` + planCols + `,
	t.id, t.name, t.slug, sv.resource_id, r.name, r.status, sv.created_by, sv.created_at, sv.updated_at`
const serviceFrom = ` FROM services sv JOIN products p ON p.id = sv.product_id JOIN product_plans pl ON pl.id = sv.plan_id
	JOIN teams t ON t.id = sv.team_id LEFT JOIN resources r ON r.id = sv.resource_id`

func scanService(row interface{ Scan(...any) error }) (*Service, error) {
	var v Service
	p := &v.Plan
	err := row.Scan(&v.ID, &v.Name, &v.Status, &v.SuspendedReason, &v.Note, &v.ProductID, &v.ProductName, &v.Kind,
		&p.ID, &p.Name, &p.CPUs, &p.MemoryMB, &p.StorageGB, &p.BackupGB, &p.Ports, &p.Price, &p.Currency, &p.Period,
		&v.TeamID, &v.TeamName, &v.TeamSlug, &v.ResourceID, &v.ResourceName, &v.ResourceStatus, &v.CreatedBy, &v.CreatedAt, &v.UpdatedAt)
	return &v, noRows(err)
}

// ServiceFilter narrows the services list; empty fields match everything.
type ServiceFilter struct{ Product, Team, Status, Query string }

func (s *Store) Services(ctx context.Context, f ServiceFilter) ([]Service, error) {
	rows, err := s.TrustedSQL(ctx).Query(ctx, `SELECT `+serviceCols+serviceFrom+`
		WHERE ($1 = '' OR p.id::text = $1) AND ($2 = '' OR t.id::text = $2) AND ($3 = '' OR sv.status = $3) AND ($4 = '' OR sv.name ILIKE '%' || $4 || '%' OR t.name ILIKE '%' || $4 || '%')
		ORDER BY sv.created_at DESC LIMIT 1000`, f.Product, f.Team, f.Status, f.Query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Service{}
	for rows.Next() {
		v, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (s *Store) Service(ctx context.Context, id string) (*Service, error) {
	return scanService(s.TrustedSQL(ctx).QueryRow(ctx, `SELECT `+serviceCols+serviceFrom+` WHERE sv.id = $1`, id))
}

// ServiceForResource is the active or suspended service a resource belongs to, if any.
func (s *Store) ServiceForResource(ctx context.Context, resourceID string) (*Service, error) {
	return scanService(s.TrustedSQL(ctx).QueryRow(ctx, `SELECT `+serviceCols+serviceFrom+` WHERE sv.resource_id = $1 AND sv.status <> 'terminated'`, resourceID))
}

func (s *Store) CreateService(ctx context.Context, product, plan, team, resource, name, userID, server string) (string, error) {
	var id string
	err := s.TrustedSQL(ctx).QueryRow(ctx, `INSERT INTO services (product_id, plan_id, team_id, resource_id, name, created_by, server_id) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		product, plan, team, resource, name, nullable(userID), nullable(server)).Scan(&id)
	return id, err
}

// UpdateService changes a service's fields; nil leaves one as it is.
func (s *Store) UpdateService(ctx context.Context, id string, status, reason, note, name, plan *string) error {
	tag, err := s.TrustedSQL(ctx).Exec(ctx, `UPDATE services SET status = coalesce($2, status), suspended_reason = coalesce($3, suspended_reason), note = coalesce($4, note),
		name = coalesce($5, name), plan_id = coalesce($6::uuid, plan_id), updated_at = now() WHERE id = $1`, id, status, reason, note, name, plan)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func placementOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

// ResourceService is what a team sees of the service a resource belongs to.
type ResourceService struct {
	Status          string `json:"status"`
	SuspendedReason string `json:"suspendedReason,omitempty"`
	Product         string `json:"product"`
	Plan            string `json:"plan"`
	Icon            string `json:"icon,omitempty"`
}

// AttachServices fills in each resource's service (if any) and its server's location.
func (s *Store) AttachServices(ctx context.Context, list []Resource) {
	if len(list) == 0 {
		return
	}
	ids := make([]string, len(list))
	at := map[string]int{}
	for i, r := range list {
		ids[i], at[r.ID] = r.ID, i
	}
	rows, err := s.TrustedSQL(ctx).Query(ctx, `SELECT r.id, coalesce(srv.location, ''), coalesce(g.spec->>'edition', ''), sv.status, sv.suspended_reason, p.name, pl.name, p.icon
		FROM resources r LEFT JOIN servers srv ON srv.id = r.server_id LEFT JOIN game_servers g ON g.resource_id = r.id
		LEFT JOIN services sv ON sv.resource_id = r.id AND sv.status <> 'terminated'
		LEFT JOIN products p ON p.id = sv.product_id LEFT JOIN product_plans pl ON pl.id = sv.plan_id
		WHERE r.id::text = ANY($1)`, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, loc, edition string
		var status, reason, product, plan, icon *string
		if rows.Scan(&id, &loc, &edition, &status, &reason, &product, &plan, &icon) != nil {
			continue
		}
		r := &list[at[id]]
		r.Location, r.Edition = loc, edition
		if status != nil {
			r.Service = &ResourceService{Status: *status, SuspendedReason: *reason, Product: *product, Plan: *plan, Icon: *icon}
		}
	}
}
