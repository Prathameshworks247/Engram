package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type aofState struct {
	mu    sync.Mutex
	file  *os.File
	fsync string // "always" | "everysec" | "no"
}

var aof *aofState
var aofReplaying bool

func aofEnabled() bool { return strings.ToLower(cfg.AppendOnly) == "yes" }

func aofDirPath() string { return filepath.Join(cfg.Dir, cfg.AppendDirname) }

func aofManifestPath() string {
	return filepath.Join(aofDirPath(), cfg.AppendFilename+".manifest")
}

// manifestEntry is one "file <name> seq <n> type <b|i>" line.
type manifestEntry struct {
	name string
	typ  string // "b" base (RDB) or "i" incr (RESP command log)
}

func parseManifest(text string) []manifestEntry {
	var out []manifestEntry
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		// file <name> seq <n> type <t>
		if len(f) >= 6 && f[0] == "file" && f[4] == "type" {
			out = append(out, manifestEntry{name: f[1], typ: f[5]})
		}
	}
	return out
}

// setupAOF prepares the append-only directory/file/manifest, replays any
// existing data, and opens the active incremental file for appending.
func setupAOF() {
	if !aofEnabled() {
		return
	}
	dir := aofDirPath()
	os.MkdirAll(dir, 0o755)

	manifestPath := aofManifestPath()
	var entries []manifestEntry
	if data, err := os.ReadFile(manifestPath); err == nil {
		entries = parseManifest(string(data))
	}

	if len(entries) == 0 {
		// Fresh start: create the default incr file + manifest.
		incrName := cfg.AppendFilename + ".1.incr.aof"
		os.WriteFile(filepath.Join(dir, incrName), nil, 0o644)
		os.WriteFile(manifestPath, []byte("file "+incrName+" seq 1 type i\n"), 0o644)
		entries = []manifestEntry{{name: incrName, typ: "i"}}
	}

	// Make sure every referenced file exists.
	for _, e := range entries {
		p := filepath.Join(dir, e.name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			os.WriteFile(p, nil, 0o644)
		}
	}

	replayAOF(dir, entries)

	// Append to the last incremental file.
	var active string
	for _, e := range entries {
		if e.typ == "i" {
			active = e.name
		}
	}
	if active == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, active), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	aof = &aofState{file: f, fsync: strings.ToLower(cfg.AppendFsync)}
}

func replayAOF(dir string, entries []manifestEntry) {
	aofReplaying = true
	defer func() { aofReplaying = false }()
	for _, e := range entries {
		path := filepath.Join(dir, e.name)
		switch e.typ {
		case "b":
			loadRDBFrom(path)
		case "i":
			replayIncr(path)
		}
	}
}

func replayIncr(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		if strings.ToUpper(args[0]) == "SELECT" {
			continue
		}
		execCommand(args)
	}
}

// appendAOF writes a processed write command to the active incremental file.
func appendAOF(args []string) {
	if aof == nil || aofReplaying {
		return
	}
	aof.mu.Lock()
	defer aof.mu.Unlock()
	aof.file.WriteString(encodeBulkArray(args))
	if aof.fsync == "always" {
		aof.file.Sync()
	}
}
