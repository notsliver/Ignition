package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Terminal: an interactive shell in the resource's running container over a WebSocket.
 * Binary frames carry terminal bytes both ways; text frames carry JSON control messages:
 *   client → {"type":"resize","cols":120,"rows":32}
 *   server → {"type":"ready","shell":"bash","user":"node"} and {"type":"exit","code":0,"reason":"..."}
 * Sessions are audited (start and end, not keystrokes), close after 15 idle minutes, and are capped per resource.
 */

const (
	terminalIdle       = 15 * time.Minute
	terminalMaxAge     = 8 * time.Hour
	terminalPerResouce = 4
)

// Uses bash when the image has it, otherwise sh.
var shellCmd = []string{"/bin/sh", "-c", `if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi`}

var (
	terminalMu       sync.Mutex
	terminalSessions = map[string]int{}
)

func (s *Server) terminal(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	if res.ContainerID == "" || res.Status != "running" {
		return httpx.Conflict(res.Name + " isn't running, so there's no container to open a shell in.")
	}
	asRoot := r.URL.Query().Get("root") == "1"
	if asRoot && u.Role != "admin" && u.Role != "owner" {
		return httpx.Forbidden("Only admins can open a root shell.")
	}
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	cols, rows = clamp(cols, 20, 500, 120), clamp(rows, 5, 200, 32)

	terminalMu.Lock()
	if terminalSessions[id] >= terminalPerResouce {
		terminalMu.Unlock()
		return httpx.TooMany(fmt.Sprintf("%s already has %d open terminals. Close one first.", res.Name, terminalPerResouce))
	}
	terminalSessions[id]++
	terminalMu.Unlock()
	defer func() { terminalMu.Lock(); terminalSessions[id]--; terminalMu.Unlock() }()

	// WebSockets skip the CSRF middleware (they're GETs), so the origin is checked here: only the dashboard may connect.
	patterns := []string{r.Host}
	if pu, err := url.Parse(s.publicURL()); err == nil && pu.Host != "" {
		patterns = append(patterns, pu.Host)
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: patterns})
	if err != nil {
		return nil // Accept already wrote the HTTP error
	}
	conn.SetReadLimit(1 << 20)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), terminalMaxAge)
	defer cancel()

	user := ""
	if asRoot {
		user = "0"
	}
	sess, err := s.Runtime.ExecTTY(ctx, res.ContainerID, runtime.TTYOptions{
		Cmd: shellCmd, User: user, Cols: cols, Rows: rows,
		Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "IGNITION_RESOURCE=" + res.Name},
	})
	if err != nil {
		sendControl(ctx, conn, map[string]any{"type": "exit", "code": -1, "reason": "Couldn't start a shell: " + err.Error()})
		conn.Close(websocket.StatusInternalError, "exec failed")
		return nil
	}
	defer sess.Close()
	started := time.Now()
	s.Store.Audit(ctx, u, "resource.terminal_open", "resource", id, map[string]any{"root": asRoot}, clientIP(r))
	sendControl(ctx, conn, map[string]any{"type": "ready", "root": asRoot, "container": res.ContainerID[:12]})

	var lastInput atomic.Int64
	lastInput.Store(time.Now().UnixNano())
	done := make(chan string, 3)

	// Container → browser.
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					done <- "browser went away"
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					slog.Debug("terminal read", "error", err)
				}
				done <- "shell exited"
				return
			}
		}
	}()
	// Browser → container.
	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				done <- "browser went away"
				return
			}
			if typ == websocket.MessageBinary {
				lastInput.Store(time.Now().UnixNano())
				if _, err := sess.Write(data); err != nil {
					done <- "shell exited"
					return
				}
				continue
			}
			var msg struct {
				Type       string `json:"type"`
				Cols, Rows int
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
				s.Runtime.ResizeExec(ctx, sess.ID, clamp(msg.Cols, 20, 500, 120), clamp(msg.Rows, 5, 200, 32))
			}
		}
	}()
	// Idle and age limits.
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- "session reached its 8 hour limit"
				return
			case <-tick.C:
				if time.Since(time.Unix(0, lastInput.Load())) > terminalIdle {
					done <- "closed after 15 minutes without input"
					return
				}
			}
		}
	}()

	why := <-done
	code, running, _ := s.Runtime.ExecExitCode(context.WithoutCancel(ctx), sess.ID)
	reason := ""
	switch {
	case why == "shell exited" && !running && (code == 126 || code == 127) && time.Since(started) < 3*time.Second:
		reason = "This image has no shell (it's probably built FROM scratch or distroless), so a terminal can't run in it."
	case why == "shell exited":
		reason = fmt.Sprintf("The shell exited with code %d.", code)
	case why != "browser went away":
		reason = "The session " + why + "."
	}
	if why != "browser went away" {
		sendControl(context.Background(), conn, map[string]any{"type": "exit", "code": code, "reason": reason})
	}
	conn.Close(websocket.StatusNormalClosure, "")
	s.Store.Audit(context.Background(), u, "resource.terminal_close", "resource", id,
		map[string]any{"seconds": int(time.Since(started).Seconds()), "reason": strings.TrimSuffix(why, "."), "root": asRoot}, clientIP(r))
	return nil
}

func sendControl(ctx context.Context, conn *websocket.Conn, v any) {
	b, _ := json.Marshal(v)
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn.Write(wctx, websocket.MessageText, b)
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	return max(lo, min(hi, v))
}
