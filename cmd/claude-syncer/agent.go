package main

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/Cookiezisg/Claude-Syncer/internal/launchd"
	"github.com/Cookiezisg/Claude-Syncer/internal/logx"
)

// desiredAgent is the launchd definition for the current set of folders.
//
// launchd watches each path for changes to the entries directly in it, so
// every account and org folder is listed, plus the folders that gain new
// ones and config.json, which changes when you switch account. The
// once-a-minute run covers anything the watches miss.
func desiredAgent(p paths, program string) (launchd.Agent, error) {
	folders, err := p.layout.Folders()
	if err != nil {
		return launchd.Agent{}, err
	}
	watch := []string{p.layout.ConfigPath(), p.layout.SessionsRoot(), p.projectsDir}
	seen := map[string]bool{}
	for _, f := range folders {
		if acct := filepath.Dir(f.Dir); !seen[acct] {
			seen[acct] = true
			watch = append(watch, acct)
		}
		watch = append(watch, f.Dir)
	}
	return launchd.Agent{
		Program:    program,
		Args:       []string{"sync", "--background"},
		WatchPaths: watch,
		Interval:   60,
		LogPath:    p.logPath,
	}, nil
}

// maintainAgent keeps the installed agent current without the user
// re-running install: a new account or org needs new watch paths, and a new
// version may change the definition. It runs at the end of a background
// pass, so it rewrites the definition and hands the reload to a detached
// helper. Reloading the job directly would kill this very process.
func maintainAgent(p paths, log *logx.Logger) {
	if !launchd.Installed() {
		return
	}
	have, err := os.ReadFile(launchd.PlistPath())
	if err != nil {
		return
	}
	program := launchd.ProgramOf(have)
	if program == "" {
		return
	}
	if _, err := os.Stat(program); err != nil {
		// We are running, so launchd found a program; trust what we are.
		if exe, err := os.Executable(); err == nil {
			program = exe
		}
	}
	agent, err := desiredAgent(p, program)
	if err != nil {
		return
	}
	want := agent.Plist()
	if bytes.Equal(have, want) {
		return
	}
	if err := launchd.ReloadDetached(agent); err != nil {
		log.Printf("agent refresh failed: %v", err)
		return
	}
	log.Printf("agent definition updated (%d watched paths)", len(agent.WatchPaths))
}
