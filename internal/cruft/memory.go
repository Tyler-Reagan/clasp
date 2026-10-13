package cruft

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	wikiLink  = regexp.MustCompile(`\[\[([^\[\]\n]+)\]\]`)
	indexLink = regexp.MustCompile(`\]\(([^)\s]+\.md)\)`)
)

const maxMemoryFile = 1 << 20

func scanMemories(ctx context.Context, env Env, projects []ProjectDir, errf func(string, error)) []MemoryIssue {
	dead := map[string]bool{}
	for _, p := range projects {
		if p.Dead {
			dead[p.Name] = true
		}
	}
	root := filepath.Join(env.ClaudeDir, "projects")
	ents, _ := os.ReadDir(root)
	var out []MemoryIssue
	for _, e := range ents {
		if ctx.Err() != nil {
			break
		}
		if !e.IsDir() {
			continue
		}
		out = append(out, memoryIssues(e.Name(), filepath.Join(root, e.Name(), "memory"), dead[e.Name()])...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Detail < b.Detail
	})
	return out
}

func readCapped(path string) (string, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxMemoryFile))
	return string(b), err == nil
}

func memoryIssues(project, dir string, gone bool) []MemoryIssue {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string // all *.md, MEMORY.md included
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		return nil
	}
	index, haveIndex := readCapped(filepath.Join(dir, "MEMORY.md"))
	bodies := map[string]string{}
	names := map[string]bool{}
	for _, f := range files {
		if f == "MEMORY.md" {
			continue
		}
		body, _ := readCapped(filepath.Join(dir, f))
		bodies[f] = body
		names[strings.TrimSuffix(f, ".md")] = true
		if n := frontmatterName(body); n != "" {
			names[n] = true
		}
	}
	var out []MemoryIssue
	add := func(file string, k MemoryIssueKind, detail string) {
		out = append(out, MemoryIssue{Project: project, File: file, Kind: k, Detail: detail})
	}
	for _, f := range files {
		if f == "MEMORY.md" {
			continue
		}
		if gone {
			add(f, ProjectGone, "project folder is gone")
		}
		seen := map[string]bool{}
		for _, m := range wikiLink.FindAllStringSubmatch(bodies[f], -1) {
			x := strings.TrimSpace(m[1])
			if i := strings.Index(x, "|"); i >= 0 {
				x = strings.TrimSpace(x[:i])
			}
			if x == "" || seen[x] || names[x] || names[strings.TrimSuffix(x, ".md")] {
				continue
			}
			seen[x] = true
			add(f, DeadLink, x)
		}
		// Claude Code loads memories through MEMORY.md, so without an index
		// every memory file is unreachable.
		switch {
		case !haveIndex:
			add(f, NotInIndex, "no MEMORY.md")
		case !mentions(index, f):
			add(f, NotInIndex, "")
		}
	}
	if haveIndex {
		seen := map[string]bool{}
		for _, m := range indexLink.FindAllStringSubmatch(index, -1) {
			link := m[1]
			if seen[link] || strings.Contains(link, "://") {
				continue
			}
			seen[link] = true
			if pathGone(filepath.Join(dir, filepath.FromSlash(link))) {
				add("MEMORY.md", IndexMissingFile, link)
			}
		}
	}
	return out
}

// mentions reports whether name appears in text as a whole file name, so
// data.md does not count as a mention of a.md.
func mentions(text, name string) bool {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_-])`)
	return re.MatchString(text)
}

// frontmatterName returns the name: value of a leading --- block.
func frontmatterName(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(body, "---\n") {
		return ""
	}
	for _, line := range strings.Split(body[4:], "\n") {
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
