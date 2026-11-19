// Command claude-syncer keeps Claude Code sessions identical across every
// Claude Desktop account on this Mac, and in step with the terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Cookiezisg/Claude-Syncer/internal/desktop"
	"github.com/Cookiezisg/Claude-Syncer/internal/engine"
	"github.com/Cookiezisg/Claude-Syncer/internal/fsutil"
	"github.com/Cookiezisg/Claude-Syncer/internal/launchd"
	"github.com/Cookiezisg/Claude-Syncer/internal/logx"
	"github.com/Cookiezisg/Claude-Syncer/internal/notify"
	"github.com/Cookiezisg/Claude-Syncer/internal/state"
)

// Set at build time with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = ""
)

const usage = `claude-syncer keeps Claude Code sessions identical across every Claude
Desktop account on this Mac, and in step with the terminal.

Usage:
  claude-syncer install      run a first sync and start syncing in the background
  claude-syncer status       show what the background sync is doing
  claude-syncer log [-f]     show recent sync activity (-f: keep following)
  claude-syncer sync         run one sync now
      --dry-run              show what would change, change nothing
      --force                allow a pass that deletes many sessions at once
  claude-syncer uninstall    stop background syncing
      --purge                also remove state, trash and logs
  claude-syncer version

Updating: brew upgrade claude-syncer (the background sync picks it up by itself).
`

type paths struct {
	appDir, statePath, trashDir, lockPath, binDir, logPath, projectsDir string
	layout                                                              desktop.Layout
}

func defaultPaths() paths {
	home, _ := os.UserHomeDir()
	app := filepath.Join(home, "Library", "Application Support", "claude-syncer")
	claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeDir == "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	return paths{
		appDir:      app,
		statePath:   filepath.Join(app, "state.json"),
		trashDir:    filepath.Join(app, "trash"),
		lockPath:    filepath.Join(app, "lock"),
		binDir:      filepath.Join(app, "bin"),
		logPath:     filepath.Join(home, "Library", "Logs", "claude-syncer", "sync.log"),
		projectsDir: filepath.Join(claudeDir, "projects"),
		layout:      desktop.DefaultLayout(),
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	p := defaultPaths()
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "sync":
		err = cmdSync(p, args)
	case "status":
		err = cmdStatus(p)
	case "install":
		err = cmdInstall(p, args)
	case "uninstall":
		err = cmdUninstall(p, args)
	case "log", "logs":
		err = cmdLog(p, args)
	case "version", "--version", "-v":
		v := "claude-syncer " + version
		if commit != "" {
			v += " (" + commit + ")"
		}
		fmt.Println(v)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claude-syncer:", err)
		os.Exit(1)
	}
}

func engineOptions(p paths) engine.Options {
	return engine.Options{
		Layout:            p.layout,
		ProjectsDir:       p.projectsDir,
		Trash:             &fsutil.Trash{Dir: p.trashDir, Retention: 7 * 24 * time.Hour},
		Import:            true,
		DeleteTranscripts: true,
		QuietPeriod:       10 * time.Minute,
		SwitchGrace:       60 * time.Second,
		VanishGrace:       2 * time.Minute,
		MaxDeletes:        20,
	}
}

// lock serialises runs: launchd may start one while a manual one is going.
func lock(p paths, wait bool) (func(), error) {
	if err := os.MkdirAll(p.appDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p.lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
		}
		if !wait || time.Now().After(deadline) {
			f.Close()
			return nil, errBusy
		}
		time.Sleep(200 * time.Millisecond)
	}
}

var errBusy = errors.New("another sync is running")

func cmdSync(p paths, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "show what would change, change nothing")
	force := fs.Bool("force", false, "allow a pass that deletes many sessions")
	background := fs.Bool("background", false, "run as the background agent (quiet, logs to file)")
	fs.Parse(args)

	var out io.Writer = os.Stdout
	log := logx.Open(p.logPath)
	if *background {
		out = io.Discard
	}
	unlock, err := lock(p, !*background)
	if errors.Is(err, errBusy) && *background {
		return nil // the running pass will see the same changes
	}
	if err != nil {
		return err
	}
	defer unlock()

	rep, err := syncOnce(p, *dry, *force, *background, log)
	if err != nil {
		log.Printf("error: %v", err)
		if *background {
			alert(p, "error:"+err.Error(), "Sync stopped: "+err.Error())
		}
		return err
	}
	printReport(out, rep, *dry)
	if *background {
		maintainAgent(p, log)
		notifyUpdate(p)
	}
	return nil
}

func syncOnce(p paths, dry, force, background bool, log *logx.Logger) (*engine.Report, error) {
	st, err := state.Load(p.statePath)
	if err != nil {
		return nil, fmt.Errorf("state %s: %w", p.statePath, err)
	}
	if background && !dry {
		if st.AgentVersion != version {
			log.Printf("background sync now running claude-syncer %s", version)
		}
		st.AgentVersion, st.AgentSeen = version, time.Now()
	}
	o := engineOptions(p)
	o.DryRun, o.Force = dry, force
	start := time.Now()
	rep, err := engine.Run(o, st)
	if err != nil {
		return nil, err
	}
	if dry {
		return rep, nil
	}
	o.Trash.Purge()
	st.LastRun = &state.RunSummary{
		Version: version, Background: background,
		At: start, DurationMS: time.Since(start).Milliseconds(),
		Folders: rep.Folders, Accounts: rep.Accounts, Sessions: rep.Sessions,
		ActiveAccount: rep.Active, Changes: len(rep.Changes), Pending: rep.Pending,
		Problems: rep.Problems, Halted: rep.Halted,
	}
	if err := st.Save(p.statePath); err != nil {
		return nil, err
	}
	for _, c := range rep.Changes {
		log.Printf("%s", c)
	}
	for _, pr := range rep.Problems {
		log.Printf("problem: %s", pr)
	}
	if rep.Halted != "" {
		log.Printf("held back: %s", rep.Halted)
		alert(p, "halted", rep.Halted)
	}
	if len(rep.Problems) > 0 {
		alert(p, "problems", fmt.Sprintf("%d session record(s) could not be read and were left alone. Run `claude-syncer status`.", len(rep.Problems)))
	}
	return rep, nil
}

// alert posts a notification at most once a day per issue.
func alert(p paths, issue, message string) {
	st, err := state.Load(p.statePath)
	if err != nil {
		notify.Show("Claude Syncer", message)
		return
	}
	now := time.Now().UnixMilli()
	if now-st.Notified[issue] < 24*time.Hour.Milliseconds() {
		return
	}
	st.Notified[issue] = now
	st.Save(p.statePath)
	notify.Show("Claude Syncer", message)
}

func printReport(w io.Writer, rep *engine.Report, dry bool) {
	verb := "Synced"
	if dry {
		verb = "Dry run"
	}
	fmt.Fprintf(w, "%s: %d accounts, %d folders, %d sessions.\n", verb, rep.Accounts, rep.Folders, rep.Sessions)
	if len(rep.Changes) == 0 {
		fmt.Fprintln(w, "Everything already in sync.")
	} else {
		counts := map[string]int{}
		for _, c := range rep.Changes {
			counts[c.Kind]++
		}
		var parts []string
		for _, k := range []string{"import", "create", "update", "remove", "tombstone", "index", "transcript"} {
			if counts[k] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
			}
		}
		action := "Changes"
		if dry {
			action = "Would make"
		}
		fmt.Fprintf(w, "%s: %s\n", action, strings.Join(parts, ", "))
		if dry || len(rep.Changes) <= 40 {
			for _, c := range rep.Changes {
				fmt.Fprintln(w, "  "+c.String())
			}
		}
	}
	if rep.Pending > 0 {
		fmt.Fprintf(w, "%d change(s) to the signed-in account wait until you switch account.\n", rep.Pending)
	}
	for _, pr := range rep.Problems {
		fmt.Fprintln(w, "Left alone:", pr)
	}
	if rep.Halted != "" {
		fmt.Fprintln(w, "Held back:", rep.Halted)
	}
}

func cmdStatus(p paths) error {
	st, err := state.Load(p.statePath)
	if err != nil {
		return err
	}
	switch {
	case launchd.Loaded():
		fmt.Print("✓ Background sync is on")
		if !st.AgentSeen.IsZero() {
			fmt.Printf(" (claude-syncer %s, last ran %s ago)", st.AgentVersion, time.Since(st.AgentSeen).Round(time.Second))
		}
		fmt.Println()
	case launchd.Installed():
		fmt.Println("✗ Background sync is installed but not running (try `claude-syncer install` again)")
	default:
		fmt.Println("✗ Background sync is off (run `claude-syncer install`)")
	}
	fmt.Printf("  claude-syncer %s", version)
	if latest := availableUpdate(p); latest != "" {
		fmt.Printf(" (%s available: brew upgrade claude-syncer)", latest)
	}
	fmt.Println()
	if v := desktopVersion(); v != "" {
		fmt.Printf("  Claude Desktop %s\n", v)
	}
	lr := st.LastRun
	if lr == nil {
		fmt.Println("  No sync has run yet.")
		return nil
	}
	fmt.Printf("  Last sync %s ago (%d ms): %d accounts, %d folders, %d sessions\n",
		time.Since(lr.At).Round(time.Second), lr.DurationMS, lr.Accounts, lr.Folders, lr.Sessions)
	if lr.Pending > 0 {
		fmt.Printf("  %d change(s) to the signed-in account wait until you switch account\n", lr.Pending)
	} else {
		fmt.Println("  All accounts in sync")
	}
	if lr.Halted != "" {
		fmt.Println("  Held back:", lr.Halted)
	}
	for _, pr := range lr.Problems {
		fmt.Println("  Left alone:", pr)
	}
	var stateSize int64
	if info, err := os.Stat(p.statePath); err == nil {
		stateSize = info.Size()
	}
	fmt.Printf("  Disk: state %s, trash %s (kept 7 days), log %s\n",
		human(stateSize), human(fsutil.DirSize(p.trashDir)), human(fsutil.DirSize(filepath.Dir(p.logPath))))
	return nil
}

func desktopVersion() string {
	out, err := exec.Command("defaults", "read", "/Applications/Claude.app/Contents/Info.plist", "CFBundleShortVersionString").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func cmdInstall(p paths, args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	fs.Parse(args)

	if _, err := os.Stat(p.layout.SessionsRoot()); err != nil {
		return fmt.Errorf("Claude Desktop session data not found at %s", p.layout.SessionsRoot())
	}
	program, err := programPath(p)
	if err != nil {
		return err
	}

	unlock, err := lock(p, true)
	if err != nil {
		return err
	}
	fmt.Println("Running the first sync…")
	rep, err := syncOnce(p, false, false, false, logx.Open(p.logPath))
	unlock()
	if err != nil {
		return err
	}
	printReport(os.Stdout, rep, false)

	agent, err := desiredAgent(p, program)
	if err != nil {
		return err
	}
	if err := launchd.Install(agent); err != nil {
		return err
	}
	fmt.Printf("\nBackground sync is on (%s).\n", program)
	fmt.Println("It runs whenever session files change, and once a minute. Check on it with `claude-syncer status`.")
	return nil
}

// programPath returns a path launchd can keep running.
//
// A Homebrew install is used through its stable link, so `brew upgrade`
// takes effect without reinstalling. Any other binary is copied into our
// own directory: it may live somewhere temporary, or under ~/Documents,
// ~/Desktop or ~/Downloads, which macOS privacy protection blocks for
// programs launchd starts.
func programPath(p paths) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.Abs(exe)
	if i := strings.Index(exe, "/Cellar/"); i >= 0 {
		link := filepath.Join(exe[:i], "bin", filepath.Base(exe))
		if _, err := os.Stat(link); err == nil {
			return link, nil
		}
	}
	for _, prefix := range []string{"/opt/homebrew/bin/", "/usr/local/bin/"} {
		if strings.HasPrefix(exe, prefix) {
			return exe, nil
		}
	}
	dst := filepath.Join(p.binDir, "claude-syncer")
	if exe == dst {
		return dst, nil
	}
	if err := copyFile(exe, dst); err != nil {
		return "", fmt.Errorf("copying the binary to %s: %w", p.binDir, err)
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(dst, data, 0o755)
}

func cmdUninstall(p paths, args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also remove state, trash and logs")
	fs.Parse(args)
	if err := launchd.Uninstall(); err != nil {
		return err
	}
	os.RemoveAll(p.binDir)
	fmt.Println("Background sync is off. Your sessions are left as they are.")
	if *purge {
		os.RemoveAll(p.appDir)
		os.RemoveAll(filepath.Dir(p.logPath))
		fmt.Println("Removed state, trash and logs.")
	}
	return nil
}

func cmdLog(p paths, args []string) error {
	fs := flag.NewFlagSet("log", flag.ExitOnError)
	follow := fs.Bool("f", false, "keep following the log")
	lines := fs.Int("n", 40, "number of lines to show")
	fs.Parse(args)
	if _, err := os.Stat(p.logPath); err != nil {
		fmt.Println("No sync activity logged yet.")
		return nil
	}
	tailArgs := []string{"-n", fmt.Sprint(*lines)}
	if *follow {
		tailArgs = append(tailArgs, "-F")
	}
	cmd := exec.Command("tail", append(tailArgs, p.logPath)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
