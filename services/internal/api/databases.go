package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/dbimport"
	"github.com/notsliver/Ignition/services/internal/functions"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * Databases: importing data (a dump file, a URL, S3, or a live database), registering an external
 * database, and functions. The heavy lifting runs in the worker; uploads are streamed straight into
 * the database container, so the API never holds a whole dump in memory.
 */

const maxUpload = 20 << 30 // 20 GB

func (s *Server) databaseEngines(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	httpx.JSON(w, 200, map[string]any{"engines": dbimport.Engines, "runtimes": functions.Runtimes})
	return nil
}

// importTarget loads a database resource that can receive an import.
func (s *Server) importTarget(ctx context.Context, id string) (*store.Resource, *dbimport.Engine, error) {
	res, err := s.Store.GetResource(ctx, id)
	if err != nil {
		return nil, nil, notFound(err, "Resource")
	}
	if res.SourceType == "external" {
		return nil, nil, httpx.Conflict(res.Name + " is an external database; import into it with its own tools.")
	}
	engine := res.Engine
	if engine == "" {
		engine = engineOf(res.Image)
	}
	eng := dbimport.EngineByID(engine)
	if eng == nil {
		return nil, nil, httpx.Conflict(res.Name + " isn't a database IGNITION can import into (PostgreSQL, MySQL, MariaDB, MongoDB, Redis, Dragonfly or ClickHouse).")
	}
	if res.ContainerID == "" || (res.Status != "running" && res.Status != "deploying") {
		return nil, nil, httpx.Conflict(res.Name + " isn't running. Start it, then import.")
	}
	return res, eng, nil
}

type importInput struct {
	Source  string `json:"source"` // url, s3, live
	URL     string `json:"url"`
	Replace bool   `json:"replace"`
	S3      struct {
		Endpoint  string `json:"endpoint"`
		Region    string `json:"region"`
		Bucket    string `json:"bucket"`
		Key       string `json:"key"`
		AccessKey string `json:"accessKey"`
		SecretKey string `json:"secretKey"`
	} `json:"s3"`
}

var safeFile = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// createImport starts an import: multipart (an uploaded file) or JSON (url, s3, live).
func (s *Server) createImport(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	ctx := r.Context()
	res, eng, err := s.importTarget(ctx, id)
	if err != nil {
		return err
	}
	var row struct {
		source, label string
		size          *int64
		replace       bool
		secret        worker.ImportSecret
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		row.source = "upload"
		mr, err := r.MultipartReader()
		if err != nil {
			return httpx.BadRequest("Send the file as multipart/form-data.")
		}
		var tmp *os.File
		defer func() {
			if tmp != nil {
				tmp.Close()
				os.Remove(tmp.Name())
			}
		}()
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return httpx.BadRequest("The upload was interrupted: " + err.Error())
			}
			switch part.FormName() {
			case "replace":
				v, _ := io.ReadAll(io.LimitReader(part, 10))
				row.replace = string(v) == "true"
			case "file":
				name := safeFile.ReplaceAllString(path.Base(part.FileName()), "_")
				if name == "" || name == "." {
					name = "dump"
				}
				tmp, err = os.CreateTemp("", "ignition-upload-*")
				if err != nil {
					return err
				}
				n, err := io.Copy(tmp, io.LimitReader(part, maxUpload+1))
				if err != nil {
					return httpx.BadRequest("The upload was interrupted: " + err.Error())
				}
				if n > maxUpload {
					return httpx.Invalid(map[string]string{"file": "Files up to 20 GB can be uploaded. Use a URL or S3 for bigger dumps."})
				}
				if n == 0 {
					return httpx.Invalid(map[string]string{"file": "That file is empty."})
				}
				row.label, row.size, row.secret.FileName = name, &n, name
			}
		}
		if tmp == nil {
			return httpx.Invalid(map[string]string{"file": "Choose a dump file."})
		}
		row.secret.FileName = row.label
		// Create the record first so the file goes into its own directory.
		importID, err := s.insertImport(ctx, res.ID, row.source, row.label, row.size, row.replace, nil, u.ID)
		if err != nil {
			return err
		}
		tmp.Seek(0, 0)
		if err := s.Runtime.CopyFileTo(ctx, res.ContainerID, worker.ImportDir(importID), row.label, tmp, *row.size); err != nil {
			s.Store.DB.Exec(ctx, `UPDATE data_imports SET status = 'failed', summary = $2, finished_at = now() WHERE id = $1`, importID, "Couldn't put the file into the database container: "+err.Error())
			return httpx.Unavailable("Couldn't put the file into " + res.Name + ": " + err.Error())
		}
		return s.queueImport(w, r, u, res, importID, row.secret)
	}

	var in importInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	row.source, row.replace = in.Source, in.Replace
	switch in.Source {
	case "url":
		pu, err := url.Parse(strings.TrimSpace(in.URL))
		if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" {
			return httpx.Invalid(map[string]string{"url": "Enter the dump's http(s) URL (a presigned link works)."})
		}
		row.label, row.secret.URL = pu.Host+pu.Path, strings.TrimSpace(in.URL)
	case "s3":
		b := in.S3
		f := map[string]string{}
		if strings.TrimSpace(b.Bucket) == "" {
			f["bucket"] = "Enter the bucket."
		}
		if strings.TrimSpace(b.Key) == "" {
			f["key"] = "Enter the object key (path of the dump in the bucket)."
		}
		if b.Endpoint != "" {
			if eu, err := url.Parse(b.Endpoint); err != nil || eu.Host == "" || (eu.Scheme != "https" && eu.Scheme != "http") {
				f["endpoint"] = "Enter the endpoint URL, e.g. https://s3.eu-central-1.amazonaws.com or your R2/MinIO URL."
			}
		}
		if (b.AccessKey == "") != (b.SecretKey == "") {
			f["secretKey"] = "Give both the access key and the secret key, or neither for a public bucket."
		}
		if len(f) > 0 {
			return httpx.Invalid(f)
		}
		row.label = "s3://" + strings.TrimSpace(b.Bucket) + "/" + strings.TrimLeft(strings.TrimSpace(b.Key), "/")
		row.secret = worker.ImportSecret{Endpoint: strings.TrimSpace(b.Endpoint), Region: strings.TrimSpace(b.Region), Bucket: strings.TrimSpace(b.Bucket),
			Key: strings.TrimSpace(b.Key), AccessKey: strings.TrimSpace(b.AccessKey), SecretKey: b.SecretKey}
	case "live":
		src, err := dbimport.ParseURL(eng, in.URL)
		if err != nil {
			return httpx.Invalid(map[string]string{"url": capitalize(err.Error()) + "."})
		}
		row.label, row.secret.URL = dbimport.Masked(src.URL), src.URL
	default:
		return httpx.BadRequest("Source is upload, url, s3 or live.")
	}
	importID, err := s.insertImport(ctx, res.ID, row.source, row.label, nil, row.replace, nil, u.ID)
	if err != nil {
		return err
	}
	return s.queueImport(w, r, u, res, importID, row.secret)
}

func (s *Server) insertImport(ctx context.Context, resourceID, source, label string, size *int64, replace bool, sealed []byte, userID string) (string, error) {
	var id string
	err := s.Store.DB.QueryRow(ctx, `INSERT INTO data_imports (resource_id, source, label, size_bytes, replace, secret_enc, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		resourceID, source, label, size, replace, sealed, optionalID(userID)).Scan(&id)
	return id, err
}

func (s *Server) queueImport(w http.ResponseWriter, r *http.Request, u *store.User, res *store.Resource, importID string, sec worker.ImportSecret) error {
	ctx := r.Context()
	b, _ := json.Marshal(sec)
	sealed, err := s.Secrets.Seal(string(b), "import:"+importID)
	if err != nil {
		return err
	}
	jobID, err := jobs.Enqueue(ctx, s.Store.DB, jobs.NewJob{Kind: jobs.KindImport, ResourceID: res.ID, CreatedBy: u.ID, Payload: map[string]string{"importId": importID}, MaxAttempts: 1})
	if err != nil {
		return err
	}
	if _, err := s.Store.DB.Exec(ctx, `UPDATE data_imports SET job_id = $2, secret_enc = $3 WHERE id = $1`, importID, jobID, sealed); err != nil {
		return err
	}
	s.Store.Audit(ctx, u, "resource.import", "resource", res.ID, map[string]any{"import": importID}, clientIP(r))
	imp, err := s.getImportRow(ctx, importID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 202, map[string]any{"import": imp})
	return nil
}

type dataImport struct {
	ID         string     `json:"id"`
	ResourceID string     `json:"resourceId"`
	JobID      *string    `json:"jobId"`
	Source     string     `json:"source"`
	Label      string     `json:"label"`
	Size       *int64     `json:"sizeBytes"`
	Replace    bool       `json:"replace"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary"`
	By         string     `json:"by"`
	CreatedAt  time.Time  `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

const importSelect = `SELECT i.id, i.resource_id, i.job_id, i.source, i.label, i.size_bytes, i.replace, i.status, i.summary, COALESCE(u.email, ''), i.created_at, i.finished_at
	FROM data_imports i LEFT JOIN users u ON u.id = i.created_by`

func scanImport(row interface{ Scan(...any) error }) (*dataImport, error) {
	var d dataImport
	err := row.Scan(&d.ID, &d.ResourceID, &d.JobID, &d.Source, &d.Label, &d.Size, &d.Replace, &d.Status, &d.Summary, &d.By, &d.CreatedAt, &d.FinishedAt)
	return &d, err
}

func (s *Server) getImportRow(ctx context.Context, id string) (*dataImport, error) {
	return scanImport(s.Store.DB.QueryRow(ctx, importSelect+` WHERE i.id = $1`, id))
}

func (s *Server) listImports(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	rows, err := s.Store.DB.Query(r.Context(), importSelect+` WHERE i.resource_id = $1 ORDER BY i.created_at DESC LIMIT 20`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []dataImport{}
	for rows.Next() {
		d, err := scanImport(rows)
		if err != nil {
			return err
		}
		out = append(out, *d)
	}
	httpx.JSON(w, 200, map[string]any{"imports": out})
	return nil
}

func (s *Server) getImport(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Import")
	if err != nil {
		return err
	}
	imp, err := s.getImportRow(r.Context(), id)
	if err != nil {
		return httpx.NotFound("Import")
	}
	logs := []store.JobLog{}
	if imp.JobID != nil {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if logs, err = s.Store.JobLogs(r.Context(), *imp.JobID, after); err != nil {
			return err
		}
	}
	httpx.JSON(w, 200, map[string]any{"import": imp, "logs": logs})
	return nil
}

func (s *Server) cancelImport(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Import")
	if err != nil {
		return err
	}
	imp, err := s.getImportRow(r.Context(), id)
	if err != nil {
		return httpx.NotFound("Import")
	}
	if imp.JobID == nil || (imp.Status != "queued" && imp.Status != "running") {
		return httpx.Conflict("This import already finished.")
	}
	status, err := jobs.RequestCancel(r.Context(), s.Store.DB, *imp.JobID)
	if err != nil {
		return err
	}
	if status == "cancelled" {
		s.Store.DB.Exec(r.Context(), `UPDATE data_imports SET status = 'cancelled', summary = 'Cancelled before it started.', finished_at = now() WHERE id = $1`, id)
	}
	httpx.JSON(w, 202, map[string]string{"status": status})
	return nil
}

// checkSource validates a live connection URL and checks the server answers (credentials are
// checked when the import runs, from the database's own network).
func (s *Server) checkSource(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	var in struct {
		Engine string `json:"engine"`
		URL    string `json:"url"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	eng := dbimport.EngineByID(in.Engine)
	if eng == nil {
		return httpx.BadRequest("Unknown engine.")
	}
	src, err := dbimport.ParseURL(eng, in.URL)
	if err != nil {
		httpx.JSON(w, 200, map[string]any{"ok": false, "error": capitalize(err.Error()) + "."})
		return nil
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", src.Address(), 5*time.Second)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "no such host"):
			msg = "The host name doesn't resolve from this machine."
		case strings.Contains(msg, "refused"):
			msg = "Nothing is listening on " + src.Address() + "."
		case strings.Contains(msg, "timeout"):
			msg = src.Address() + " didn't answer within 5 seconds (firewall or wrong address?)."
		}
		httpx.JSON(w, 200, map[string]any{"ok": false, "address": src.Address(), "error": msg})
		return nil
	}
	conn.Close()
	httpx.JSON(w, 200, map[string]any{"ok": true, "address": src.Address(), "latencyMs": time.Since(start).Milliseconds(), "user": src.User, "database": src.Database, "tls": src.TLS})
	return nil
}

/* ------------------------------ External databases ------------------------------ */

type externalInput struct {
	EnvironmentID string `json:"environmentId"`
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	URL           string `json:"url"` // either a URL, or the fields below
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Database      string `json:"database"`
	User          string `json:"user"`
	Password      string `json:"password"`
	TLS           bool   `json:"tls"`
}

// connectionURL builds the URL apps use, from the parts.
func connectionURL(eng *dbimport.Engine, host string, port int, user, password, database string, tls bool) string {
	scheme := eng.Scheme[0]
	if tls && (eng.ID == "redis" || eng.ID == "dragonfly") {
		scheme = "rediss"
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	if user != "" || password != "" {
		u.User = url.UserPassword(user, password)
	}
	if database != "" {
		u.Path = "/" + database
	}
	if tls {
		switch eng.ID {
		case "postgresql":
			u.RawQuery = "sslmode=require"
		case "mongodb":
			u.RawQuery = "tls=true"
		case "mysql", "mariadb":
			u.RawQuery = "ssl-mode=REQUIRED"
		}
	}
	return u.String()
}

func (s *Server) createExternal(w http.ResponseWriter, r *http.Request, u *store.User) error {
	projectID, err := pathID(r, "Project")
	if err != nil {
		return err
	}
	ctx := r.Context()
	project, err := s.Store.GetProject(ctx, u.ID, projectID)
	if err != nil {
		return notFound(err, "Project")
	}
	var in externalInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	eng := dbimport.EngineByID(in.Engine)
	if eng == nil {
		return httpx.Invalid(map[string]string{"engine": "Choose the database engine."})
	}
	if strings.TrimSpace(in.URL) != "" {
		src, err := dbimport.ParseURL(&dbimport.Engine{ID: eng.ID, Label: eng.Label, DefaultPort: eng.DefaultPort, Scheme: eng.Scheme}, in.URL)
		if err != nil && !strings.Contains(err.Error(), "localhost") {
			return httpx.Invalid(map[string]string{"url": capitalize(err.Error()) + "."})
		}
		if src != nil {
			in.Host, in.Port, in.User, in.Password, in.Database, in.TLS = src.Host, src.Port, src.User, src.Password, src.Database, in.TLS || src.TLS
		}
	}
	in.Name, in.Host, in.User, in.Database = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.User), strings.TrimSpace(in.Database)
	if in.Port == 0 {
		in.Port = eng.DefaultPort
	}
	if !nameRE.MatchString(in.Name) {
		f["name"] = "Use lowercase letters, numbers and dashes (up to 50 characters)."
	}
	if in.Host == "" || strings.ContainsAny(in.Host, " /") {
		f["host"] = "Enter the host name or IP address."
	} else if in.Host == "localhost" || in.Host == "127.0.0.1" {
		f["host"] = "localhost would mean each app's own container. Use host.docker.internal for a database on this machine, or its network address."
	}
	if in.Port < 1 || in.Port > 65535 {
		f["port"] = "Ports are numbers from 1 to 65535."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	envID := in.EnvironmentID
	if envID == "" && len(project.Environments) > 0 {
		envID = project.Environments[0].ID
	}
	connURL := connectionURL(eng, in.Host, in.Port, in.User, in.Password, in.Database, in.TLS)
	ri := resourceInput{Name: in.Name, Type: "database", SourceType: "external", Env: []envInput{
		{Key: "DB_HOST", Value: in.Host}, {Key: "DB_PORT", Value: strconv.Itoa(in.Port)}, {Key: "DB_NAME", Value: in.Database},
		{Key: "DB_USER", Value: in.User}, {Key: "DB_PASSWORD", Value: in.Password, Secret: true}, {Key: "DATABASE_URL", Value: connURL, Secret: true},
	}}
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id, _, err := s.provision(ctx, tx, u, projectID, envID, ri, provisionExtras{Engine: eng.ID, NoDeploy: true})
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": "A resource with this name already exists in this environment."})
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.Store.Audit(ctx, u, "resource.create", "resource", id, map[string]any{"name": in.Name, "external": eng.ID, "host": in.Host}, clientIP(r))
	res, _ := s.Store.GetResource(ctx, id)
	httpx.JSON(w, 201, map[string]any{"resource": res})
	return nil
}

// connection shows how apps connect to a database: its private address (or the external one) and
// the URL, without the password unless the caller may reveal secrets.
func (s *Server) connection(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	ctx := r.Context()
	res, err := s.Store.GetResource(ctx, id)
	if err != nil {
		return notFound(err, "Resource")
	}
	eng := dbimport.EngineByID(firstNonBlank(res.Engine, engineOf(res.Image)))
	if eng == nil {
		return httpx.Conflict(res.Name + " isn't a database.")
	}
	vars, err := s.Store.ListEnv(ctx, id)
	if err != nil {
		return err
	}
	val := map[string]string{}
	for _, v := range vars {
		if plain, err := s.Secrets.Open(v.Sealed, id+":"+v.Key); err == nil {
			val[v.Key] = plain
		}
	}
	host, port, user, pass, db, tls := res.Name, eng.DefaultPort, "", "", "", false
	if eng.ID == "clickhouse" {
		port = 8123
	}
	if res.SourceType == "external" {
		host, user, pass, db = val["DB_HOST"], val["DB_USER"], val["DB_PASSWORD"], val["DB_NAME"]
		port, _ = strconv.Atoi(val["DB_PORT"])
		tls = strings.Contains(val["DATABASE_URL"], "ssl") || strings.Contains(val["DATABASE_URL"], "tls=true") || strings.HasPrefix(val["DATABASE_URL"], "rediss")
	} else {
		pick := func(keys ...string) string {
			for _, k := range keys {
				if val[k] != "" {
					return val[k]
				}
			}
			return ""
		}
		user = pick("POSTGRES_USER", "MARIADB_USER", "MYSQL_USER", "MONGO_INITDB_ROOT_USERNAME", "CLICKHOUSE_USER")
		pass = pick("POSTGRES_PASSWORD", "MARIADB_PASSWORD", "MYSQL_PASSWORD", "MONGO_INITDB_ROOT_PASSWORD", "CLICKHOUSE_PASSWORD", "REDIS_PASSWORD", "DFLY_requirepass")
		db = pick("POSTGRES_DB", "MARIADB_DATABASE", "MYSQL_DATABASE", "CLICKHOUSE_DB")
		if eng.ID == "postgresql" && user == "" {
			user = "postgres"
		}
	}
	canReveal := roleRank[u.Role] >= roleRank["admin"]
	full := connectionURL(eng, host, port, user, pass, db, tls)
	if eng.ID == "mongodb" && res.SourceType != "external" {
		full += "?authSource=admin"
	}
	masked := dbimport.Masked(full)
	out := map[string]any{"engine": eng.ID, "label": eng.Label, "host": host, "port": port, "user": user, "database": db, "tls": tls,
		"external": res.SourceType == "external", "maskedUrl": masked, "hasPassword": pass != "", "status": res.Status}
	if canReveal {
		out["url"] = full
	}
	httpx.JSON(w, 200, out)
	return nil
}

// shareConnection puts a database's connection URL into another resource's variables.
func (s *Server) shareConnection(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	var in struct {
		TargetID string `json:"targetId"`
		Key      string `json:"key"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Key = strings.TrimSpace(in.Key)
	if !envKeyRE.MatchString(in.Key) {
		return httpx.Invalid(map[string]string{"key": "Use letters, numbers and underscores, e.g. DATABASE_URL."})
	}
	ctx := r.Context()
	src, err := s.Store.GetResource(ctx, id)
	if err != nil {
		return notFound(err, "Resource")
	}
	target, err := s.Store.GetResource(ctx, in.TargetID)
	if err != nil || target.ProjectID != src.ProjectID {
		return httpx.Invalid(map[string]string{"targetId": "Pick a resource of this project."})
	}
	// Reuse connection() to build the URL with the password.
	rec := &captureWriter{header: http.Header{}}
	admin := *u
	admin.Role = "owner"
	if err := s.connection(rec, r, &admin); err != nil {
		return err
	}
	var conn map[string]any
	json.Unmarshal(rec.body, &conn)
	urlStr, _ := conn["url"].(string)
	if urlStr == "" {
		return errors.New("couldn't build the connection URL")
	}
	if src.EnvironmentID != target.EnvironmentID && src.SourceType != "external" {
		return httpx.Invalid(map[string]string{"targetId": "Databases are reached by name inside their own environment; pick a resource in the same environment."})
	}
	sealed, err := s.Secrets.Seal(urlStr, target.ID+":"+in.Key)
	if err != nil {
		return err
	}
	if _, err := s.Store.DB.Exec(ctx, `INSERT INTO env_vars (resource_id, key, value_enc, secret, updated_by) VALUES ($1, $2, $3, true, $4)
		ON CONFLICT (resource_id, key) DO UPDATE SET value_enc = EXCLUDED.value_enc, secret = true, updated_by = EXCLUDED.updated_by, updated_at = now()`, target.ID, in.Key, sealed, u.ID); err != nil {
		return err
	}
	s.Store.Audit(ctx, u, "resource.env.update", "resource", target.ID, map[string]any{"key": in.Key, "from": src.Name}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"target": target.Name, "key": in.Key, "requiresRedeploy": target.ContainerID != ""})
	return nil
}

type captureWriter struct {
	header http.Header
	body   []byte
	status int
}

func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return len(b), nil
}
func (c *captureWriter) WriteHeader(s int) { c.status = s }

// testConnection checks an external database answers from this machine.
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	vars, err := s.Store.ListEnv(r.Context(), id)
	if err != nil {
		return err
	}
	host, port := "", ""
	for _, v := range vars {
		if v.Key == "DB_HOST" || v.Key == "DB_PORT" {
			p, _ := s.Secrets.Open(v.Sealed, id+":"+v.Key)
			if v.Key == "DB_HOST" {
				host = p
			} else {
				port = p
			}
		}
	}
	if host == "" {
		return httpx.Conflict("Only external databases have an address to test.")
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second)
	status := "running"
	result := map[string]any{"ok": err == nil, "address": net.JoinHostPort(host, port), "latencyMs": time.Since(start).Milliseconds()}
	if err != nil {
		status, result["error"] = "failed", fmt.Sprintf("%s didn't accept a connection: %v", net.JoinHostPort(host, port), err)
	} else {
		conn.Close()
	}
	s.Store.SetStatus(r.Context(), id, status)
	httpx.JSON(w, 200, result)
	return nil
}

/* --------------------------------- Functions --------------------------------- */

func (s *Server) getFunction(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil || res.Type != "function" {
		return httpx.NotFound("Function")
	}
	code := ""
	vars, _ := s.Store.ListEnv(r.Context(), id)
	for _, v := range vars {
		if v.Key == functions.CodeVar {
			code, _ = s.Secrets.Open(v.Sealed, id+":"+v.Key)
		}
	}
	rt := functions.RuntimeByID(res.Engine)
	httpx.JSON(w, 200, map[string]any{"runtime": res.Engine, "runtimeLabel": rt.Label, "file": rt.File, "code": code, "template": rt.Template, "maxBytes": functions.MaxCodeSize,
		"status": res.Status, "url": res.Domain})
	return nil
}

// putFunction saves the handler (and refreshes the built-in server) and redeploys.
func (s *Server) putFunction(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	ctx := r.Context()
	res, err := s.Store.GetResource(ctx, id)
	if err != nil || res.Type != "function" {
		return httpx.NotFound("Function")
	}
	var in struct {
		Code   string `json:"code"`
		Deploy bool   `json:"deploy"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	fn, err := functions.Prepare(res.Engine, in.Code)
	if err != nil {
		return httpx.Invalid(map[string]string{"code": capitalize(err.Error()) + "."})
	}
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, v := range fn.Vars {
		sealed, err := s.Secrets.Seal(v[1], id+":"+v[0])
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO env_vars (resource_id, key, value_enc, secret, updated_by) VALUES ($1, $2, $3, false, $4)
			ON CONFLICT (resource_id, key) DO UPDATE SET value_enc = EXCLUDED.value_enc, updated_by = EXCLUDED.updated_by, updated_at = now()`, id, v[0], sealed, u.ID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE resources SET command = $2, image = $3, updated_at = now() WHERE id = $1`, id, fn.Command, fn.Image); err != nil {
		return err
	}
	depID := ""
	if in.Deploy {
		if depID, err = s.Store.CreateDeployment(ctx, tx, id, "manual", "", "", "Function code updated", u.ID); err != nil {
			return err
		}
		if _, err := enqueueDeploy(ctx, tx, id, depID, u.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.Store.Audit(ctx, u, "function.update", "resource", id, map[string]any{"bytes": len(in.Code), "deploy": in.Deploy}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"deploymentId": depID})
	return nil
}

// invokeFunction sends one request to the function from inside its network and returns the answer.
func (s *Server) invokeFunction(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	var in worker.InvokeRequest
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in.Body) > 1<<20 {
		return httpx.Invalid(map[string]string{"body": "Up to 1 MB."})
	}
	if !s.limiter.allow("invoke:" + id) {
		return httpx.TooMany("That's a lot of calls in a minute. Try again shortly.")
	}
	jobID, err := jobs.Enqueue(r.Context(), s.Store.DB, jobs.NewJob{Kind: jobs.KindInvoke, ResourceID: id, CreatedBy: u.ID, Payload: in, MaxAttempts: 1})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	for {
		var status, progress, lastErr string
		if err := s.Store.DB.QueryRow(ctx, `SELECT status, progress, last_error FROM jobs WHERE id = $1`, jobID).Scan(&status, &progress, &lastErr); err != nil {
			return err
		}
		switch status {
		case "succeeded":
			var out map[string]any
			json.Unmarshal([]byte(progress), &out)
			httpx.JSON(w, 200, map[string]any{"result": out})
			return nil
		case "failed", "cancelled":
			return httpx.Unavailable("The call couldn't be made: " + lastErr)
		}
		select {
		case <-ctx.Done():
			return httpx.Unavailable("The worker didn't pick up the call. Is it busy with a long build?")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
