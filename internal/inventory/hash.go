package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// fileEntry is a cached file hash, valid while size and mtime still match.
type fileEntry struct {
	size  int64
	mtime time.Time
	sum   string
	desc  string // SKILL.md frontmatter description, parsed when the file is read
}

// hashCache outlives Load calls so a refresh only re-reads changed files.
var hashCache = struct {
	sync.Mutex
	m map[string]fileEntry
}{m: map[string]fileEntry{}}

// hashReads counts file reads that missed the cache; tests watch it.
var hashReads atomic.Int64

// fileHash returns the hex sha256 of path's content. For a SKILL.md it also
// returns the frontmatter description, so one read serves both.
func fileHash(path string) (sum, desc string, err error) {
	touch(path)
	fi, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if fi.IsDir() {
		return "", "", fmt.Errorf("hash %s: is a directory", path)
	}
	hashCache.Lock()
	e, ok := hashCache.m[path]
	hashCache.Unlock()
	if ok && e.size == fi.Size() && e.mtime.Equal(fi.ModTime()) {
		return e.sum, e.desc, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	hashReads.Add(1)
	e = fileEntry{size: fi.Size(), mtime: fi.ModTime(), sum: sha(data)}
	if filepath.Base(path) == "SKILL.md" {
		e.desc = skillDescription(string(data))
	}
	hashCache.Lock()
	hashCache.m[path] = e
	hashCache.Unlock()
	return e.sum, e.desc, nil
}

func sha(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// skipInTree reports whether a tree entry is a marker or cache file that must
// not count as plugin content.
func skipInTree(name string, dir bool) bool {
	if dir {
		return name == ".git" || name == "node_modules"
	}
	return name == ".in_use" || name == ".DS_Store" || strings.HasSuffix(name, ".meta.json")
}

// treeHash hashes a directory as the sorted "relpath\x00filesum\n" lines of
// its files. Symlinked directories are not followed. On error it still returns
// the hash of what it could read, alongside the joined errors.
func treeHash(root string) (string, error) {
	type line struct{ rel, sum string }
	var lines []line
	var errs []error

	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		ents, err := readDir(dir)
		if err != nil {
			errs = append(errs, err)
		}
		for _, e := range ents {
			name := e.Name()
			if skipInTree(name, e.IsDir()) {
				continue
			}
			p, r := filepath.Join(dir, name), rel+name
			if e.IsDir() {
				walk(p, r+"/")
				continue
			}
			if e.Type()&fs.ModeSymlink != 0 {
				// Symlinked dirs are skipped; dangling links count as nothing.
				if fi, err := os.Stat(p); err != nil || fi.IsDir() {
					continue
				}
			} else if !e.Type().IsRegular() {
				continue
			}
			sum, _, err := fileHash(p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			lines = append(lines, line{r, sum})
		}
	}

	if !isDir(root) {
		return "", fmt.Errorf("hash %s: %w", root, fs.ErrNotExist)
	}
	walk(root, "")

	sort.Slice(lines, func(i, j int) bool { return lines[i].rel < lines[j].rel })
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.rel + "\x00" + l.sum + "\n")
	}
	return sha([]byte(sb.String())), errors.Join(errs...)
}
