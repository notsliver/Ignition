package worker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/dbimport"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Data imports into a database resource. The dump (uploaded through the API, or downloaded here from
 * a URL or S3) is placed in the database container's /tmp/ignition-import/<id>/, then restored with
 * the engine's own tools inside that container. Redis-family imports run redis-cli from a helper
 * container on the same private network. Output streams into the import's log.
 */

// ImportDir is where an import's file waits inside the target container.
func ImportDir(id string) string { return "/tmp/ignition-import/" + id }

// ImportSecret is what's sealed with an import: where the data comes from.
type ImportSecret struct {
	URL       string `json:"url,omitempty"` // download URL or live connection URL
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Key       string `json:"key,omitempty"`
	AccessKey string `json:"accessKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	FileName  string `json:"fileName,omitempty"` // uploads: already in the container
}

type importRow struct {
	ID, ResourceID, Source, Label string
	Replace                       bool
	Sealed                        []byte
}

func (w *Worker) dataImport(ctx context.Context, job *jobs.Job) error {
	var p struct {
		ImportID string `json:"importId"`
	}
	json.Unmarshal(job.Payload, &p)
	var row importRow
	if err := w.Store.DB.QueryRow(ctx, `SELECT id, resource_id, source, label, replace, secret_enc FROM data_imports WHERE id = $1`, p.ImportID).
		Scan(&row.ID, &row.ResourceID, &row.Source, &row.Label, &row.Replace, &row.Sealed); err != nil {
		return fmt.Errorf("%w: import record missing", jobs.ErrPermanent)
	}
	w.Store.DB.Exec(ctx, `UPDATE data_imports SET status = 'running' WHERE id = $1`, row.ID)
	log := func(level, msg string) { w.Queue.Log(ctx, job.ID, level, "import", msg) }
	summary, err := w.runImport(ctx, job, row, log)
	status := "succeeded"
	if err != nil {
		status, summary = "failed", strings.TrimPrefix(err.Error(), jobs.ErrPermanent.Error()+": ")
		if errors.Is(ctx.Err(), context.Canceled) {
			status, summary = "cancelled", "Cancelled."
		}
	}
	w.Store.DB.Exec(context.WithoutCancel(ctx), `UPDATE data_imports SET status = $2, summary = $3, finished_at = now() WHERE id = $1`, row.ID, status, truncate(summary, 500))
	if err != nil {
		return fmt.Errorf("%w: %s", jobs.ErrPermanent, summary) // a half-applied import isn't retried blindly
	}
	return nil
}

func (w *Worker) runImport(ctx context.Context, job *jobs.Job, row importRow, log func(string, string)) (string, error) {
	res, err := w.Store.GetResource(ctx, row.ResourceID)
	if err != nil {
		return "", errors.New("the database no longer exists")
	}
	eng := dbimport.EngineByID(res.Engine)
	if eng == nil {
		return "", fmt.Errorf("%s isn't a database Ignition knows how to import into", res.Name)
	}
	if res.ContainerID == "" || res.Status != "running" {
		return "", fmt.Errorf("%s isn't running; start it and import again", res.Name)
	}
	var sec ImportSecret
	if len(row.Sealed) > 0 {
		plain, err := w.Secrets.Open(row.Sealed, "import:"+row.ID)
		if err != nil {
			return "", errors.New("the import's source details can't be decrypted (was IGNITION_SECRET_KEY changed?)")
		}
		json.Unmarshal([]byte(plain), &sec)
	}
	dir := ImportDir(row.ID)
	defer w.Runtime.Exec(context.WithoutCancel(ctx), res.ContainerID, []string{"rm", "-rf", dir}, nil, func(string, string) {})

	// Wait until the database accepts connections (a new one may still be initialising).
	if eng.Ready != "" {
		log("info", "Waiting for "+res.Name+" to accept connections")
		ready := false
		for i := 0; i < 60 && ctx.Err() == nil; i++ {
			code, err := w.Runtime.Exec(ctx, res.ContainerID, []string{"sh", "-c", eng.Ready}, nil, func(string, string) {})
			if err == nil && code == 0 {
				ready = true
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !ready {
			return "", fmt.Errorf("%s didn't start accepting connections within 2 minutes", res.Name)
		}
	}

	// Get the file into the database container.
	fileName := sec.FileName
	if row.Source == "url" || row.Source == "s3" {
		f, name, size, err := w.download(ctx, row.Source, sec, log)
		if err != nil {
			return "", err
		}
		defer os.Remove(f.Name())
		defer f.Close()
		log("info", fmt.Sprintf("Copying %s (%s) into %s", name, humanBytes(size), res.Name))
		if err := w.Runtime.CopyFileTo(ctx, res.ContainerID, dir, name, f, size); err != nil {
			return "", fmt.Errorf("couldn't put the file into the database container: %v", err)
		}
		fileName = name
	}

	env := []string{"REPLACE=" + map[bool]string{true: "1", false: "0"}[row.Replace]}
	script := dbimport.FileScript(eng)
	if row.Source == "live" {
		src, err := dbimport.ParseURL(eng, sec.URL)
		if err != nil {
			return "", err
		}
		env = append(env, src.Env()...)
		script = dbimport.LiveScript(eng)
		log("info", "Source: "+dbimport.Masked(sec.URL))
	} else {
		env = append(env, "F="+path.Join(dir, fileName))
	}

	lines := 0
	last := ""
	logLine := func(level, line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		lines++
		if lines <= 4000 {
			w.Queue.Log(ctx, job.ID, level, "import", line)
		}
		if strings.HasPrefix(line, "==> ") {
			last = strings.TrimPrefix(line, "==> ")
		}
	}
	if eng.Helper {
		return w.helperImport(ctx, res, eng, row, dir, fileName, env, script, logLine)
	}
	code, err := w.Runtime.Exec(ctx, res.ContainerID, []string{"sh", "-c", script}, env, logLine)
	if err != nil {
		return "", fmt.Errorf("couldn't run the import: %v", err)
	}
	if code != 0 {
		return "", fmt.Errorf("the import stopped with exit code %d; the log shows the database's error. %s", code, map[bool]string{true: "Some data may already be in.", false: ""}[!row.Replace])
	}
	return firstNonEmpty(last, "Imported."), nil
}

// helperImport runs redis-cli in a short-lived container on the database's network.
func (w *Worker) helperImport(ctx context.Context, res *store.Resource, eng *dbimport.Engine, row importRow, dir, fileName string, env []string, script string, logLine runtime.Logf) (string, error) {
	vars, err := w.envFor(ctx, res.ID)
	if err != nil {
		return "", err
	}
	auth := ""
	for _, v := range vars {
		k, val, _ := strings.Cut(v, "=")
		if (k == "REDIS_PASSWORD" || k == "DFLY_requirepass") && val != "" {
			auth = val
		}
	}
	if id, _ := w.Runtime.ImageID(ctx, dbimport.HelperImage); id == "" {
		if err := w.Runtime.PullImage(ctx, dbimport.HelperImage, "", func(string, string) {}); err != nil {
			return "", fmt.Errorf("couldn't download the redis-cli helper image: %v", err)
		}
	}
	if fileName != "" {
		env = append(env[:len(env)-1], "F=/tmp/"+fileName) // the helper gets the file in its own /tmp
	}
	name := "ign-import-" + row.ID[:8]
	w.Runtime.Remove(ctx, name, false)
	hid, err := w.Runtime.CreateContainer(ctx, runtime.ContainerSpec{
		Name: name, Image: dbimport.HelperImage, Cmd: []string{"sh", "-c", script}, Network: runtime.EnvNetwork(res.EnvironmentID),
		Env:    append(env, "TARGET="+res.Name, "AUTH=", "REDISCLI_AUTH="+auth), // redis-cli reads the password from REDISCLI_AUTH
		Labels: map[string]string{"ignition.managed": "true", "ignition.probe": "true"}, MemoryMB: 256, CPUs: 1, Limits: runtime.Limits{Restart: "never"},
	})
	if err != nil {
		return "", err
	}
	defer w.Runtime.Remove(context.WithoutCancel(ctx), hid, false)
	if fileName != "" {
		if err := w.Runtime.CopyBetween(ctx, res.ContainerID, path.Join(dir, fileName), hid, "/tmp"); err != nil {
			return "", err
		}
	}
	if err := w.Runtime.Start(ctx, hid); err != nil {
		return "", err
	}
	seen := 0
	for {
		time.Sleep(2 * time.Second)
		lines, _ := w.Runtime.Logs(ctx, hid, 5000, time.Time{})
		for _, l := range lines[min(seen, len(lines)):] {
			logLine("info", l.Message)
		}
		seen = len(lines)
		st, err := w.Runtime.Inspect(ctx, hid)
		if err != nil || !st.Exists {
			return "", errors.New("the helper container disappeared")
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if !st.Running {
			if st.ExitCode != 0 {
				return "", fmt.Errorf("the import stopped with exit code %d; the log shows why", st.ExitCode)
			}
			if len(lines) > 0 {
				return strings.TrimPrefix(lines[len(lines)-1].Message, "==> "), nil
			}
			return "Imported.", nil
		}
	}
}

// download fetches a dump from a URL or S3 into a temp file and returns it opened at the start.
func (w *Worker) download(ctx context.Context, source string, sec ImportSecret, log func(string, string)) (*os.File, string, int64, error) {
	var req *http.Request
	var name string
	switch source {
	case "url":
		u, err := url.Parse(sec.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, "", 0, errors.New("the download URL must be http(s)")
		}
		req, _ = http.NewRequestWithContext(ctx, "GET", sec.URL, nil)
		name = path.Base(u.Path)
		log("info", "Downloading from "+u.Host)
	case "s3":
		var err error
		req, err = s3Request(ctx, sec)
		if err != nil {
			return nil, "", 0, err
		}
		name = path.Base(sec.Key)
		log("info", fmt.Sprintf("Downloading s3://%s/%s", sec.Bucket, sec.Key))
	}
	if name == "" || name == "/" || name == "." {
		name = "dump"
	}
	resp, err := (&http.Client{Timeout: 6 * time.Hour}).Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		msg := strings.TrimSpace(string(b))
		if i := strings.Index(msg, "<Message>"); i >= 0 {
			msg = strings.SplitN(msg[i+9:], "</Message>", 2)[0]
		}
		return nil, "", 0, fmt.Errorf("download failed: %s %s", resp.Status, truncate(msg, 200))
	}
	f, err := os.CreateTemp("", "ignition-import-*")
	if err != nil {
		return nil, "", 0, err
	}
	size, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, "", 0, fmt.Errorf("download interrupted: %v", err)
	}
	log("info", "Downloaded "+humanBytes(size))
	f.Seek(0, 0)
	return f, name, size, nil
}

// s3Request signs a GET for an object (AWS Signature V4, path-style, works with S3-compatible stores).
func s3Request(ctx context.Context, s ImportSecret) (*http.Request, error) {
	endpoint := strings.TrimRight(s.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://s3." + firstNonEmpty(s.Region, "us-east-1") + ".amazonaws.com"
	}
	region := firstNonEmpty(s.Region, "us-east-1")
	u, err := url.Parse(endpoint + "/" + s.Bucket + "/" + strings.TrimLeft(s.Key, "/"))
	if err != nil || u.Host == "" {
		return nil, errors.New("the S3 endpoint isn't a valid URL")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if s.AccessKey == "" {
		return req, nil // a public bucket
	}
	now := time.Now().UTC()
	amzDate, day := now.Format("20060102T150405Z"), now.Format("20060102")
	payload := "UNSIGNED-PAYLOAD"
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payload)
	canonicalURI := u.EscapedPath()
	canonical := strings.Join([]string{"GET", canonicalURI, "", "host:" + u.Host + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + amzDate + "\n",
		"host;x-amz-content-sha256;x-amz-date", payload}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	h := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	mac := func(key []byte, data string) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(data))
		return m.Sum(nil)
	}
	k := mac(mac(mac(mac([]byte("AWS4"+s.SecretKey), day), region), "s3"), "aws4_request")
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=%s",
		s.AccessKey, scope, hex.EncodeToString(mac(k, toSign))))
	return req, nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
