package cruft

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	headLines   = 20      // transcript lines read looking for cwd
	maxLineSize = 1 << 20 // longer lines are skipped, not buffered
)

// ScanProject re-inspects one ~/.claude/projects/<dir> folder from scratch.
func ScanProject(env Env, dir string) (ProjectDir, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return ProjectDir{}, err
	}
	if !fi.IsDir() {
		return ProjectDir{}, errors.New("not a directory (or a symlink): " + dir)
	}
	return scanProjectDir(context.Background(), dir, LiveSessions(env), cutoffDays(env)), nil
}

func scanProjects(ctx context.Context, env Env, sessions []LiveSession, cutoff int, errf func(string, error)) []ProjectDir {
	root := filepath.Join(env.ClaudeDir, "projects")
	ents, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			errf("reading projects dir", err)
		}
		return nil
	}
	var out []ProjectDir
	for _, e := range ents {
		if ctx.Err() != nil {
			break
		}
		if !e.IsDir() { // symlinks and files are not project dirs
			continue
		}
		out = append(out, scanProjectDir(ctx, filepath.Join(root, e.Name()), sessions, cutoff))
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Dead != b.Dead {
			return a.Dead
		}
		if a.Size != b.Size {
			return a.Size > b.Size
		}
		return a.Name < b.Name
	})
	return out
}

func scanProjectDir(ctx context.Context, dir string, sessions []LiveSession, cutoff int) ProjectDir {
	p := ProjectDir{Dir: dir, Name: filepath.Base(dir)}
	old := time.Now().Add(-time.Duration(cutoff) * 24 * time.Hour)
	cwds := map[string]bool{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if ctx.Err() != nil {
			break
		}
		if e.Name() == "memory" && e.IsDir() {
			p.HasMemories = hasMarkdown(filepath.Join(dir, "memory"))
			continue
		}
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p.Transcripts++
		if info, err := e.Info(); err == nil && info.ModTime().Before(old) {
			p.OlderThanCutoff++
		}
		cwd, entry := readTranscriptHead(filepath.Join(dir, e.Name()))
		if filepath.IsAbs(cwd) {
			cwds[filepath.Clean(cwd)] = true
		}
		switch entry {
		case "claude-desktop":
			p.Desktop++
		case "cli":
			p.CLI++
		default:
			p.Other++
		}
	}
	for c := range cwds {
		p.CWDs = append(p.CWDs, c)
	}
	sort.Strings(p.CWDs)

	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			p.Size += info.Size()
			if info.ModTime().After(p.Newest) {
				p.Newest = info.ModTime()
			}
		}
		return nil
	})

	p.DeadKnown = len(p.CWDs) > 0
	if p.DeadKnown {
		p.Dead = true
		for _, c := range p.CWDs {
			if !pathGone(c) {
				p.Dead = false
				break
			}
		}
	}
	for _, s := range sessions {
		if EncodeProjectPath(s.CWD) == p.Name || contains(p.CWDs, filepath.Clean(s.CWD)) {
			p.LivePIDs = append(p.LivePIDs, s.PID)
		}
	}
	switch {
	case !p.DeadKnown:
		p.Why = "no working directory recorded in its transcripts"
	case !p.Dead:
		p.Why = "project folder still exists"
	case p.HasMemories:
		p.Why = "has memory files"
	case len(p.LivePIDs) > 0:
		p.Why = "in use by " + pidList(p.LivePIDs)
	default:
		p.Trashable = true
	}
	return p
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hasMarkdown(dir string) bool {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			return true
		}
	}
	return false
}

// readTranscriptHead reads at most headLines lines of a transcript looking for
// the first recorded cwd, and the entrypoint seen alongside it. Lines over
// maxLineSize are skipped without being held in memory.
func readTranscriptHead(path string) (cwd, entrypoint string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for i := 0; i < headLines; i++ {
		line, ok, eof := readLine(r)
		if ok {
			var l struct {
				CWD        string `json:"cwd"`
				Entrypoint string `json:"entrypoint"`
			}
			if json.Unmarshal(line, &l) == nil {
				if entrypoint == "" {
					entrypoint = l.Entrypoint
				}
				if l.CWD != "" {
					return l.CWD, entrypoint
				}
			}
		}
		if eof {
			break
		}
	}
	return "", entrypoint
}

// readLine returns the next line; ok is false when it was longer than
// maxLineSize (skipped) or empty. eof is true when input is exhausted.
func readLine(r *bufio.Reader) (line []byte, ok, eof bool) {
	var buf []byte
	over := false
	for {
		part, isPrefix, err := r.ReadLine()
		if err != nil {
			return buf, !over && len(buf) > 0, true
		}
		if !over {
			if len(buf)+len(part) > maxLineSize {
				over, buf = true, nil
			} else {
				buf = append(buf, part...)
			}
		}
		if !isPrefix {
			return buf, !over && len(buf) > 0, false
		}
	}
}
