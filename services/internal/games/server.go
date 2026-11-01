package games

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/runtime"
)

// Server is one game server as the API and the automation engine use it: the adapter, what the
// owner chose, its running container (if any) and its files.
type Server struct {
	Adapter   Adapter
	Spec      Spec
	Docker    *runtime.Docker
	Container string // the running container, "" when stopped
	Data      Data
}

var ErrNotRunning = errors.New("the server isn't running. Start it first")

func (s Server) Running() bool { return s.Container != "" }

// Command sends one console line. out is the reply when the game returns one (Java's RCON).
func (s Server) Command(ctx context.Context, line string) (out string, replies bool, err error) {
	c, ok := s.Adapter.(Console)
	if !ok {
		return "", false, errors.New("this game has no console")
	}
	if !s.Running() {
		return "", false, ErrNotRunning
	}
	line = strings.TrimSpace(line)
	if line == "" || len(line) > 1000 || strings.ContainsAny(line, "\r\n\x00") {
		return "", false, errors.New("enter one command (up to 1,000 characters)")
	}
	cmd, replies := c.ConsoleCommand(s.Spec, line)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	text, code, err := s.Docker.ExecCapture(ctx, s.Container, cmd)
	if err != nil {
		return "", replies, err
	}
	text = strings.TrimSpace(colorCodes.ReplaceAllString(text, ""))
	if code != 0 {
		if strings.Contains(strings.ToLower(text), "connection refused") || strings.Contains(text, "dial tcp") || text == "" {
			return "", replies, errors.New("the server is still starting; the console answers once it's up")
		}
		return "", replies, errors.New(text)
	}
	return text, replies, nil
}

// Status asks the running server for players and version. ok is false while it's starting.
func (s Server) Status(ctx context.Context) (Status, bool) {
	p, isPinger := s.Adapter.(Pinger)
	if !isPinger || !s.Running() {
		return Status{Names: []string{}}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, _, err := s.Docker.ExecCapture(ctx, s.Container, p.StatusCommand(s.Spec))
	if err != nil {
		return Status{Names: []string{}}, false
	}
	st, ok := p.ParseStatus(out)
	if !ok || !st.Online {
		return Status{Names: []string{}}, false
	}
	// The status ping only samples names; ask the console for the full list when it can answer.
	if ps, isPlayers := s.Adapter.(PlayerSupport); isPlayers && !st.NamesKnown && st.Players > 0 {
		cfg := ps.Players(s.Spec)
		if cfg.OnlineCommand != "" && cfg.ParseOnline != nil {
			if text, replies, err := s.Command(ctx, cfg.OnlineCommand); err == nil && replies {
				st.Names, st.NamesKnown = cfg.ParseOnline(text), true
			}
		}
	}
	return st, true
}

// Announce broadcasts a message to everyone on the server. It's skipped when the server is stopped.
func (s Server) Announce(ctx context.Context, message string) error {
	c, ok := s.Adapter.(Console)
	if !ok || !s.Running() {
		return nil
	}
	_, _, err := s.Command(ctx, c.Announce(s.Spec, message))
	return err
}

// Backup archives the data volume. A running server is told to save and pause writing first, so
// the world on disk is complete.
func (s Server) Backup(ctx context.Context, label string, auto bool) (Backup, error) {
	if s.Running() {
		if h, ok := s.Adapter.(SaveSupport); ok {
			before, after := h.SaveHooks(s.Spec)
			paused := true
			for _, line := range before {
				if _, _, err := s.Command(ctx, line); err != nil {
					paused = false // still starting: nothing to flush yet
					break
				}
			}
			if paused {
				defer func() {
					for _, line := range after {
						s.Command(context.WithoutCancel(ctx), line)
					}
				}()
				time.Sleep(3 * time.Second) // let the flush finish writing
			}
		}
	}
	return s.Data.CreateBackup(ctx, BackupName(label, auto, time.Now()))
}
