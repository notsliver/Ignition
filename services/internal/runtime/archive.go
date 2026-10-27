package runtime

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// CopyFileTo streams one file into a container (running or stopped) as dir/name. dir must be under
// /tmp; missing directories are created (/tmp itself is left as it is).
func (d *Docker) CopyFileTo(ctx context.Context, container, dir, name string, r io.Reader, size int64) error {
	rel := strings.Trim(strings.TrimPrefix(path.Clean(dir), "/tmp"), "/")
	if !strings.HasPrefix(path.Clean(dir), "/tmp/") || rel == "" {
		return fmt.Errorf("copy target %s must be inside /tmp", dir)
	}
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		now := time.Now()
		var err error
		parts := strings.Split(rel, "/")
		for i := range parts {
			if err == nil {
				err = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: strings.Join(parts[:i+1], "/") + "/", Mode: 0o777, ModTime: now})
			}
		}
		if err == nil {
			err = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: rel + "/" + name, Mode: 0o644, Size: size, ModTime: now})
		}
		if err == nil {
			_, err = io.CopyN(tw, r, size)
		}
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	resp, err := d.do(ctx, "PUT", "/containers/"+url.PathEscape(container)+"/archive", url.Values{"path": {"/tmp"}}, pr, "application/x-tar")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("copy into the container: %s %s", resp.Status, b)
	}
	return nil
}

// CopyBetween copies a path from one container into a directory of another (a tar stream, no temp file).
func (d *Docker) CopyBetween(ctx context.Context, from, src, to, dstDir string) error {
	get, err := d.do(ctx, "GET", "/containers/"+url.PathEscape(from)+"/archive", url.Values{"path": {src}}, nil, "")
	if err != nil {
		return err
	}
	defer get.Body.Close()
	if get.StatusCode != http.StatusOK {
		return fmt.Errorf("read %s from the container: %s", src, get.Status)
	}
	put, err := d.do(ctx, "PUT", "/containers/"+url.PathEscape(to)+"/archive", url.Values{"path": {dstDir}}, get.Body, "application/x-tar")
	if err != nil {
		return err
	}
	defer put.Body.Close()
	if put.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(put.Body, 500))
		return fmt.Errorf("copy into the helper: %s %s", put.Status, b)
	}
	return nil
}
