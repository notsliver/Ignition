package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/automations"
	"github.com/notsliver/Ignition/services/internal/games"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

// gameEnv merges what the game's adapter asks for under the owner's own variables (theirs win), so a
// version change is just a redeploy and power users can still override anything in Environment Variables.
func (w *Worker) gameEnv(ctx context.Context, jobID string, res *store.Resource, own []string) ([]string, error) {
	gs, err := w.Store.GetGameServer(ctx, res.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: this game server's settings are missing", jobs.ErrPermanent)
	}
	adapter, ok := games.Get(gs.Game)
	if !ok {
		return nil, fmt.Errorf("%w: %s servers aren't supported by this version of Ignition", jobs.ErrPermanent, gs.Game)
	}
	spec, err := games.ParseSpec(gs.Spec)
	if err != nil {
		return nil, fmt.Errorf("%w: this game server's settings can't be read: %v", jobs.ErrPermanent, err)
	}
	// Setup values (name, slots, difficulty) are only passed until the game has written its own
	// settings file; after that the file is the truth and edits there aren't overwritten.
	firstRun := !gs.Initialized
	rt, err := adapter.Runtime(spec, res.MemoryMB, firstRun)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", jobs.ErrPermanent, err)
	}
	if firstRun && w.Docker != nil && rt.Marker != "" {
		data := games.Data{D: w.Docker, Volume: VolumeName(res.ID, "data")}
		if exists, err := data.Exists(ctx, rt.Marker); err == nil && exists {
			w.Store.MarkGameInitialized(ctx, res.ID)
			firstRun = false
			rt, _ = adapter.Runtime(spec, res.MemoryMB, false)
		}
	}
	e, _ := games.EditionOf(adapter, spec.Edition)
	what := e.Name
	if v := games.GameVersion(spec); v != "" {
		what += " " + v
	}
	if spec.Modpack != nil {
		what += " · " + spec.Modpack.Title + " " + spec.Modpack.VersionNumber
	}
	w.Queue.Log(ctx, jobID, "info", "source", fmt.Sprintf("%s server: %s on %s", adapter.Manifest().Name, what, rt.Image))
	if firstRun {
		w.Queue.Log(ctx, jobID, "info", "source", "First start: the server downloads and sets itself up, which can take a few minutes")
	}

	mine := map[string]bool{}
	for _, kv := range own {
		k, _, _ := strings.Cut(kv, "=")
		mine[k] = true
	}
	keys := make([]string, 0, len(rt.Env))
	for k := range rt.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys)+len(own))
	for _, k := range keys {
		if mine[k] {
			w.Queue.Log(ctx, jobID, "info", "source", "Using your own "+k+" from Environment Variables")
			continue
		}
		out = append(out, k+"="+rt.Env[k])
	}
	return append(out, own...), nil
}

// GameServer builds the games.Server for a game resource on this machine.
func GameServer(ctx context.Context, st *store.Store, docker *runtime.Docker, res *store.Resource) (games.Server, error) {
	if res.Type != "game" {
		return games.Server{}, fmt.Errorf("%s isn't a game server", res.Name)
	}
	gs, err := st.GetGameServer(ctx, res.ID)
	if err != nil {
		return games.Server{}, fmt.Errorf("%s has no game settings", res.Name)
	}
	adapter, ok := games.Get(gs.Game)
	if !ok {
		return games.Server{}, fmt.Errorf("%s servers aren't supported by this version of Ignition", gs.Game)
	}
	spec, err := games.ParseSpec(gs.Spec)
	if err != nil {
		return games.Server{}, err
	}
	srv := games.Server{Adapter: adapter, Spec: spec, Docker: docker,
		Data: games.Data{D: docker, Volume: VolumeName(res.ID, "data"), Backups: VolumeName(res.ID, "backups")}}
	if res.ContainerID != "" && res.Status == "running" {
		srv.Container = res.ContainerID
	}
	return srv, nil
}

// stepGame runs the game server actions of an automation.
func (w *Worker) stepGame(ctx context.Context, t target, s automations.Step) stepOutcome {
	res, err := w.localOnly(ctx, t)
	if err != nil {
		return stepOutcome{err: err}
	}
	if w.Docker == nil {
		return stepOutcome{err: errors.New("game servers need the local Docker engine")}
	}
	srv, err := GameServer(ctx, w.Store, w.Docker, res)
	if err != nil {
		return stepOutcome{err: err}
	}
	switch s.Action {
	case "game_announce":
		if !srv.Running() {
			o := stepOutcome{output: res.Name + " is stopped, so nobody was there to tell."}
			o.verified, o.verification = verdict(true, "Nothing to announce to.")
			return o
		}
		if err := srv.Announce(ctx, s.Text("message")); err != nil {
			return stepOutcome{err: err}
		}
		o := stepOutcome{output: "Announced: " + s.Text("message")}
		o.verified, o.verification = verdict(true, "The server took the message.")
		return o

	case "game_command":
		out, replies, err := srv.Command(ctx, s.Text("command"))
		if err != nil {
			return stepOutcome{err: err}
		}
		o := stepOutcome{output: out}
		expect := s.Text("expect")
		switch {
		case expect == "":
			o.verified, o.verification = verdict(true, "The server accepted the command.")
		case !replies:
			o.verified, o.verification = verdict(true, "Sent. This game doesn't reply on the console, so the expected text can't be checked; see Logs.")
		default:
			o.verified, o.verification = verdict(strings.Contains(out, expect), fmt.Sprintf("Expected %q in the reply.", expect))
		}
		return o

	case "game_backup":
		b, err := srv.Backup(ctx, "scheduled", true)
		if err != nil {
			return stepOutcome{err: err}
		}
		removed, _ := srv.Data.PruneBackups(ctx, max(s.Int("keep"), 1))
		o := stepOutcome{output: fmt.Sprintf("Wrote %s (%s).", b.Name, games.HumanSize(b.Size))}
		if removed > 0 {
			o.output += fmt.Sprintf(" Removed %d older automatic backup(s).", removed)
		}
		o.verified, o.verification = verdict(b.Size > 0, "The archive is in the backups volume.")
		return o

	case "game_restart":
		if !srv.Running() {
			return stepOutcome{err: fmt.Errorf("%s isn't running", res.Name)}
		}
		msg := strings.TrimSpace(s.Text("message"))
		if msg == "" {
			msg = "The server restarts"
		}
		var log []string
		warn := func(text string) {
			if srv.Announce(ctx, text) == nil {
				log = append(log, "Announced: "+text)
			}
		}
		wait := func(d time.Duration) bool {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(d):
				return true
			}
		}
		// Count down: at the start, a minute before, and ten seconds before.
		if m := s.Int("warnMinutes"); m > 0 {
			unit := map[bool]string{true: "minute", false: "minutes"}[m == 1]
			warn(fmt.Sprintf("%s in %d %s", msg, m, unit))
			if m > 1 && !wait(time.Duration(m-1)*time.Minute) {
				return stepOutcome{err: ctx.Err()}
			}
			if m > 1 {
				warn(msg + " in 1 minute")
			}
			if !wait(50 * time.Second) {
				return stepOutcome{err: ctx.Err()}
			}
			warn(msg + " in 10 seconds")
			if !wait(10 * time.Second) {
				return stepOutcome{err: ctx.Err()}
			}
		}
		if _, _, err := srv.Command(ctx, "save-all"); err == nil {
			log = append(log, "Saved the world.")
		}
		ds, _ := w.Store.GetBuildSettings(ctx, res.ID)
		started := time.Now()
		if err := w.Runtime.Restart(ctx, res.ContainerID, time.Duration(ds.StopGrace)*time.Second); err != nil {
			return stepOutcome{err: fmt.Errorf("restart failed: %v", err)}
		}
		log = append(log, "Restarted the server.")
		o := stepOutcome{output: strings.Join(log, "\n")}
		deadline := started.Add(time.Duration(max(s.Int("verifySeconds"), 30)) * time.Second)
		for time.Now().Before(deadline) {
			if !wait(5 * time.Second) {
				break
			}
			if st, ok := srv.Status(ctx); ok && st.Online {
				o.verified, o.verification = verdict(true, fmt.Sprintf("Back up after %ds and answering players.", int(time.Since(started).Seconds())))
				return o
			}
		}
		w.refreshStatus(ctx, &res.ID)
		o.verified, o.verification = verdict(false, fmt.Sprintf("Not answering players within %ds.", s.Int("verifySeconds")))
		return o
	}
	return stepOutcome{err: errors.New("unknown game action")}
}

// stepBackup archives every volume of a resource (a game server saves its world first).
func (w *Worker) stepBackup(ctx context.Context, t target, s automations.Step) stepOutcome {
	res, err := w.localOnly(ctx, t)
	if err != nil {
		return stepOutcome{err: err}
	}
	if w.Docker == nil {
		return stepOutcome{err: errors.New("backups need the local Docker engine")}
	}
	vols, err := w.Store.Volumes(ctx, res.ID)
	if err != nil {
		return stepOutcome{err: err}
	}
	if len(vols) == 0 {
		return stepOutcome{err: fmt.Errorf("%s has no volumes to back up", res.Name)}
	}
	running := res.Status == "running" && res.ContainerID != ""
	if s.Bool("stop") && running && res.Type != "game" {
		ds, _ := w.Store.GetBuildSettings(ctx, res.ID)
		if err := w.Runtime.Stop(ctx, res.ContainerID, time.Duration(max(ds.StopGrace, 10))*time.Second); err != nil {
			return stepOutcome{err: fmt.Errorf("couldn't pause %s: %v", res.Name, err)}
		}
		defer func() {
			w.Runtime.Start(context.WithoutCancel(ctx), res.ContainerID)
			w.refreshStatus(context.WithoutCancel(ctx), &res.ID)
		}()
	}
	var lines []string
	for _, v := range vols {
		if v.Name == "backups" {
			continue
		}
		data := games.Data{D: w.Docker, Volume: VolumeName(res.ID, v.Name), Backups: VolumeName(res.ID, "backups")}
		if v.Name != "data" {
			data.BackupDir = v.Name
		}
		var b games.Backup
		if res.Type == "game" && v.Name == "data" {
			srv, gerr := GameServer(ctx, w.Store, w.Docker, res)
			if gerr != nil {
				return stepOutcome{err: gerr}
			}
			srv.Data = data
			b, err = srv.Backup(ctx, "scheduled", true)
		} else {
			b, err = data.CreateBackup(ctx, games.BackupName("scheduled", true, time.Now()))
		}
		if err != nil {
			return stepOutcome{err: fmt.Errorf("%s: %v", v.Name, err), output: strings.Join(lines, "\n")}
		}
		removed, _ := data.PruneBackups(ctx, max(s.Int("keep"), 1))
		line := fmt.Sprintf("%s: wrote %s (%s)", v.Name, b.Name, games.HumanSize(b.Size))
		if removed > 0 {
			line += fmt.Sprintf(", removed %d older", removed)
		}
		lines = append(lines, line+".")
	}
	o := stepOutcome{output: strings.Join(lines, "\n")}
	o.verified, o.verification = verdict(len(lines) > 0, "Each volume's archive is in the backups volume.")
	return o
}
