// Package settings describes Claude Code settings keys and resolves their
// effective values across settings files.
package settings

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:generate go run ./gen -src https://code.claude.com/docs/en/settings-reference.md -out catalog.json -ste ste.json

//go:embed catalog.json
var catalogJSON []byte

// ste.json maps each key to one ASD-STE100 sentence, written by hand.
//
//go:embed ste.json
var steJSON []byte

// docsPage is the human-readable settings reference that anchors point into.
const docsPage = "https://code.claude.com/docs/en/settings-reference"

// Scope is the set of files a key may be written in, as the docs index names it.
type Scope string

const (
	ScopeAny              Scope = "Any file"
	ScopeUserLocalManaged Scope = "User, local, or managed"
	ScopeUserManaged      Scope = "User or managed"
	ScopeUser             Scope = "User"
	ScopeManaged          Scope = "Managed"
	ScopeGlobal           Scope = "Global config" // ~/.claude.json
)

// Valid reports whether s is one of the scopes the docs define.
func (s Scope) Valid() bool {
	switch s {
	case ScopeAny, ScopeUserLocalManaged, ScopeUserManaged, ScopeUser, ScopeManaged, ScopeGlobal:
		return true
	}
	return false
}

// MergeKind is how values from several settings files combine, derived from
// the documented Type.
type MergeKind string

const (
	MergeReplace      MergeKind = "replace"      // highest layer wins
	MergeList         MergeKind = "list"         // arrays concatenate
	MergeMap          MergeKind = "map"          // per-member highest wins
	MergeUndocumented MergeKind = "undocumented" // "object with…": docs don't say
)

// Doc is one documented settings key. Dotted keys (permissions.allow) name
// nested JSON paths.
type Doc struct {
	Key           string    `json:"key"`
	Anchor        string    `json:"anchor"`
	Topic         string    `json:"topic"`
	Scope         Scope     `json:"scope"`
	ScopeNote     string    `json:"scopeNote"` // prose after the scope label
	Summary       string    `json:"summary"`
	STE           string    `json:"-"` // from ste.json
	Type          string    `json:"type"`
	TypeValues    []string  `json:"typeValues,omitempty"`
	Default       string    `json:"default"`
	OverrideFlags []string  `json:"overrideFlags,omitempty"` // e.g. --model
	OverrideEnv   []string  `json:"overrideEnv,omitempty"`   // e.g. ANTHROPIC_MODEL
	Merge         MergeKind `json:"merge"`
	Removed       string    `json:"removed,omitempty"` // version, e.g. v2.1.277
}

// Catalog is the read-only set of documented keys.
type Catalog struct {
	source  string
	fetched string
	docs    []Doc
	index   map[string]int
	parents map[string]bool // keys that prefix another key plus "."
}

var (
	defaultOnce    sync.Once
	defaultCatalog *Catalog
)

// Default returns the catalog embedded at build time. It panics if the
// embedded files are malformed, which tests catch before release.
func Default() *Catalog {
	defaultOnce.Do(func() {
		c, err := parseEmbedded(catalogJSON, steJSON)
		if err != nil {
			panic(err)
		}
		defaultCatalog = c
	})
	return defaultCatalog
}

func parseEmbedded(catalogData, steData []byte) (*Catalog, error) {
	var raw struct {
		Source  string `json:"source"`
		Fetched string `json:"fetched"`
		Keys    []Doc  `json:"keys"`
	}
	if err := json.Unmarshal(catalogData, &raw); err != nil {
		return nil, fmt.Errorf("settings: catalog.json: %w", err)
	}
	var ste map[string]string
	if err := json.Unmarshal(steData, &ste); err != nil {
		return nil, fmt.Errorf("settings: ste.json: %w", err)
	}
	for i := range raw.Keys {
		raw.Keys[i].STE = ste[raw.Keys[i].Key]
	}
	c := NewCatalog(raw.Fetched, raw.Keys)
	if raw.Source != "" {
		c.source = raw.Source
	}
	return c, nil
}

// NewCatalog builds a catalog from docs in the given order. Tests in other
// packages use it to supply small fixtures.
func NewCatalog(fetched string, docs []Doc) *Catalog {
	c := &Catalog{
		source:  docsPage + ".md",
		fetched: fetched,
		docs:    docs,
		index:   make(map[string]int, len(docs)),
		parents: map[string]bool{},
	}
	for i, d := range docs {
		c.index[d.Key] = i
		for k := d.Key; strings.Contains(k, "."); {
			k = k[:strings.LastIndexByte(k, '.')]
			c.parents[k] = true
		}
	}
	return c
}

// Lookup returns the doc for an exact key.
func (c *Catalog) Lookup(key string) (Doc, bool) {
	i, ok := c.index[key]
	if !ok {
		return Doc{}, false
	}
	return c.docs[i], true
}

// HasChildren reports whether any catalog key starts with key + ".".
func (c *Catalog) HasChildren(key string) bool {
	return c.parents[key]
}

// Docs returns every key in docs index order. Callers must not modify it.
func (c *Catalog) Docs() []Doc {
	return c.docs
}

// Fetched is the date (YYYY-MM-DD) the docs were fetched.
func (c *Catalog) Fetched() string {
	return c.fetched
}

// DocsURL links to a key's entry in the published settings reference.
func (c *Catalog) DocsURL(anchor string) string {
	page := strings.TrimSuffix(c.source, ".md")
	if anchor == "" {
		return page
	}
	return page + "#" + anchor
}
