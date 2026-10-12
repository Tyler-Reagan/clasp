// Command gen builds catalog.json from Anthropic's Claude Code settings
// reference. It runs only at development time through go generate; the clasp
// binary never links it and never touches the network.
//
//	go run ./gen [-src URL|path] [-fetched YYYY-MM-DD] -out catalog.json [-ste ste.json]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const defaultSource = "https://code.claude.com/docs/en/settings-reference.md"

type catalog struct {
	Source  string  `json:"source"`
	Fetched string  `json:"fetched"`
	Keys    []entry `json:"keys"`
}

func main() {
	src := flag.String("src", defaultSource, "settings reference URL or local markdown path")
	fetched := flag.String("fetched", "", "fetch date YYYY-MM-DD (required with a local path; defaults to today UTC for a URL)")
	out := flag.String("out", "", "output catalog.json path")
	ste := flag.String("ste", "", "optional ste.json to check coverage against")
	flag.Parse()

	if err := run(*src, *fetched, *out, *ste); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(src, fetched, out, ste string) error {
	if out == "" {
		return fmt.Errorf("-out is required")
	}
	isURL := strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")
	source := src
	if !isURL {
		// Local copies stand in for the published page.
		source = defaultSource
		if fetched == "" {
			return fmt.Errorf("-fetched is required with a local -src")
		}
	} else if fetched == "" {
		fetched = time.Now().UTC().Format(time.DateOnly)
	}
	if _, err := time.Parse(time.DateOnly, fetched); err != nil {
		return fmt.Errorf("-fetched %q: want YYYY-MM-DD", fetched)
	}

	data, err := load(src, isURL)
	if err != nil {
		return err
	}
	entries, err := parse(data)
	if err != nil {
		return err
	}
	if ste != "" {
		if err := checkSTE(ste, entries); err != nil {
			return err
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(catalog{Source: source, Fetched: fetched, Keys: entries}); err != nil {
		return err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "gen: wrote %d keys to %s\n", len(entries), out)
	return nil
}

func load(src string, isURL bool) ([]byte, error) {
	if !isURL {
		return os.ReadFile(src)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(src)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", src, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// checkSTE warns about catalog keys without an STE line and STE keys the
// catalog does not have. It never fails the run on coverage gaps.
func checkSTE(path string, entries []entry) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var lines map[string]string
	if err := json.Unmarshal(data, &lines); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	known := map[string]bool{}
	var missing []string
	for _, e := range entries {
		known[e.Key] = true
		if strings.TrimSpace(lines[e.Key]) == "" {
			missing = append(missing, e.Key)
		}
	}
	var extra []string
	for k := range lines {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	if len(missing) > 0 {
		warnf("%d keys missing STE lines: %s", len(missing), strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		warnf("%d STE keys not in catalog: %s", len(extra), strings.Join(extra, ", "))
	}
	return nil
}

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: warning: "+format+"\n", args...)
}
