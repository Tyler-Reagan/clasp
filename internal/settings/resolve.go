package settings

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Status is what happened to one layer's value for a key.
type Status int

const (
	StatusWins       Status = iota // replace: this layer's value applies
	StatusOverridden               // replace: a higher layer also sets it
	StatusMerged                   // list/map: combined with the other layers
	StatusIgnored                  // not read from this file (see Reason)
	StatusListed                   // merge rule not documented; shown only
)

func (s Status) String() string {
	switch s {
	case StatusWins:
		return "wins"
	case StatusOverridden:
		return "overridden"
	case StatusMerged:
		return "merged"
	case StatusIgnored:
		return "ignored"
	case StatusListed:
		return "listed"
	}
	return "unknown"
}

// Contribution is one layer's value for a key.
type Contribution struct {
	Layer  *Layer
	Value  any
	Status Status
	Reason string // why it was ignored
}

// Part is one entry of a list or one member of a map, with the layer it came
// from.
type Part struct {
	Name     string // map member name; "" for list entries
	Value    any
	Layer    *Layer
	Shadowed bool   // duplicate entry, or member set by a higher layer
	Missing  string // path named by a rule that does not exist
	Inactive bool   // project rule while the folder is not trusted
}

// Item is one row of the settings view: a documented key or an undocumented
// key found in a file.
type Item struct {
	Key        string
	Doc        Doc
	Documented bool
	Value      any            // effective value; nil when unset or not derivable
	Winner     *Layer         // replace merges only
	Contribs   []Contribution // highest precedence first
	Parts      []Part
	Notes      []string
	Warnings   int
}

// Set reports whether any file sets the key, even where it is ignored.
func (it Item) Set() bool {
	return len(it.Contribs) > 0
}

// Sources lists the layers whose value is used or shown, highest first.
func (it Item) Sources() []*Layer {
	var out []*Layer
	for _, c := range it.Contribs {
		if c.Status != StatusIgnored {
			out = append(out, c.Layer)
		}
	}
	return out
}

// Snapshot is the resolved settings view for one project.
type Snapshot struct {
	Project       string
	ProjectAuto   bool    // filled by caller: project came from the latest session
	Layers        []Layer // highest first; global last
	Items         []Item  // all catalog docs plus undocumented keys, sorted case-insensitively
	KnownProjects []string
	Trust         *bool // project's hasTrustDialogAccepted; nil if unknown
	MDMPresent    bool
	Fetched       string
}

// Notes shared by several keys.
const (
	noteUndocumentedMerge = "The docs do not say how this key merges across files."
	noteStricter          = "A stricter value from a lower file can apply over managed settings."
	noteArrayMember       = "Set through strictPluginOnlyCustomization."
	noteManagedAsIs       = "managed list applies as-is"
)

// special lists keys whose merge differs from what their Type implies.
type special struct {
	whole       bool   // replace the whole value
	managedAsIs bool   // a managed list replaces the others
	note        string // shown, not computed
}

var specials = map[string]special{
	"fallbackModel":   {whole: true},
	"modelPicker":     {whole: true},
	"availableModels": {managedAsIs: true},
	"modelSettings":   {note: "effortLevel can change which file wins per model."},

	"disableClaudeAiConnectors":                       {note: noteStricter},
	"enableArtifact":                                  {note: noteStricter},
	"isolatePeerMachines":                             {note: noteStricter},
	"permissions.blockReadsOutsideWorkingDirectories": {note: noteStricter},
	"autoMode.classifyAllShell":                       {note: noteStricter},
	"remoteControlAtStartup":                          {note: noteStricter},
	"crossSessionInbound":                             {note: noteStricter},
	"useAutoModeDuringPlan":                           {note: noteStricter},
	"syncClaudeAiSkills":                              {note: noteStricter},
	"syncClaudeAiPlugins":                             {note: noteStricter},
	"maxEffortLevel":                                  {note: noteStricter},
}

// Resolve reads every settings layer for project ("" for none) and works out
// each key's effective value with the embedded catalog. It never writes.
func Resolve(env Env, project string) *Snapshot {
	return resolveWith(env, project, Default())
}

func resolveWith(env Env, project string, cat *Catalog) *Snapshot {
	if project != "" {
		project = filepath.Clean(project)
	}
	g := readGlobal(cat, env, project)
	low := readLayers(cat, env, project)

	s := &Snapshot{
		Project:       project,
		KnownProjects: g.known,
		Trust:         g.trust,
		Fetched:       cat.Fetched(),
	}
	for i := len(low) - 1; i >= 0; i-- {
		s.Layers = append(s.Layers, low[i])
	}
	s.Layers = append(s.Layers, g.layer)
	for _, p := range env.MDMPaths {
		if _, err := os.Stat(p); err == nil {
			s.MDMPresent = true
		}
	}

	// Layers is final now, so pointers into it stay valid.
	keys := map[string]bool{}
	for _, d := range cat.Docs() {
		keys[d.Key] = true
	}
	for i := range s.Layers {
		for k := range s.Layers[i].Values {
			keys[k] = true
		}
	}
	for k := range keys {
		s.Items = append(s.Items, resolveKey(cat, s.Layers, k))
	}
	sort.Slice(s.Items, func(i, j int) bool {
		a, b := strings.ToLower(s.Items[i].Key), strings.ToLower(s.Items[j].Key)
		if a != b {
			return a < b
		}
		return s.Items[i].Key < s.Items[j].Key
	})

	checkRules(s, env)
	return s
}

func resolveKey(cat *Catalog, layers []Layer, key string) Item {
	doc, documented := cat.Lookup(key)
	if !documented {
		doc = Doc{Key: key, Merge: MergeReplace}
	}
	it := Item{Key: key, Doc: doc, Documented: documented}

	if parent, _, ok := cutLast(key); ok && arrayMembers[parent] {
		it.Notes = append(it.Notes, noteArrayMember)
		return it
	}

	var allowed []int // indexes into it.Contribs
	for i := range layers {
		l := &layers[i]
		v, ok := l.Values[key]
		if !ok {
			continue
		}
		c := Contribution{Layer: l, Value: v}
		if reason := scopeReason(doc.Scope, documented, l.Kind); reason != "" {
			c.Status, c.Reason = StatusIgnored, reason
			it.Warnings++
		} else {
			allowed = append(allowed, len(it.Contribs))
		}
		it.Contribs = append(it.Contribs, c)
	}
	if len(it.Contribs) == 0 {
		return it
	}

	sp := specials[key]
	if sp.managedAsIs {
		allowed = managedOnly(&it, allowed)
	}
	merge := doc.Merge
	if sp.whole {
		merge = MergeReplace
	}
	switch merge {
	case MergeList:
		mergeList(&it, allowed)
	case MergeMap:
		mergeMap(&it, allowed)
	case MergeUndocumented:
		for _, i := range allowed {
			it.Contribs[i].Status = StatusListed
		}
		it.Notes = append(it.Notes, noteUndocumentedMerge)
	default:
		for n, i := range allowed {
			if n == 0 {
				it.Contribs[i].Status = StatusWins
				it.Value, it.Winner = it.Contribs[i].Value, it.Contribs[i].Layer
			} else {
				it.Contribs[i].Status = StatusOverridden
			}
		}
	}

	if sp.note != "" {
		it.Notes = append(it.Notes, sp.note)
	}
	if doc.Removed != "" {
		it.Warnings++
		it.Notes = append(it.Notes, "Removed in "+doc.Removed+"; Claude Code ignores it.")
	}
	return it
}

// scopeReason returns why a layer of kind may not set a key with this scope,
// or "" if it may. Undocumented keys are read from any settings file.
func scopeReason(scope Scope, documented bool, kind LayerKind) string {
	if !documented {
		if kind == LayerGlobal {
			return "only settings files are read for this key"
		}
		return ""
	}
	if scope == ScopeGlobal {
		if kind == LayerGlobal {
			return ""
		}
		return "only read from ~/.claude.json"
	}
	var ok bool
	switch scope {
	case ScopeAny:
		ok = kind != LayerGlobal
	case ScopeUserLocalManaged:
		ok = kind == LayerUser || kind == LayerLocal || kind == LayerManaged
	case ScopeUserManaged:
		ok = kind == LayerUser || kind == LayerManaged
	case ScopeUser:
		ok = kind == LayerUser
	case ScopeManaged:
		ok = kind == LayerManaged
	default:
		ok = kind != LayerGlobal
	}
	if ok {
		return ""
	}
	return string(scope) + " only"
}

// managedOnly drops non-managed contributions when a managed file sets the
// key, and returns the allowed indexes that remain.
func managedOnly(it *Item, allowed []int) []int {
	var managed []int
	for _, i := range allowed {
		if it.Contribs[i].Layer.Kind == LayerManaged {
			managed = append(managed, i)
		}
	}
	if len(managed) == 0 {
		return allowed
	}
	for _, i := range allowed {
		if it.Contribs[i].Layer.Kind != LayerManaged {
			it.Contribs[i].Status, it.Contribs[i].Reason = StatusIgnored, noteManagedAsIs
		}
	}
	return managed
}

// mergeList unions array entries, highest layer first. A repeated entry is
// kept as a Part but marked Shadowed and left out of Value.
func mergeList(it *Item, allowed []int) {
	seen := map[string]bool{}
	union := []any{}
	for _, i := range allowed {
		c := &it.Contribs[i]
		c.Status = StatusMerged
		entries, ok := c.Value.([]any)
		if !ok {
			entries = []any{c.Value}
		}
		for _, e := range entries {
			k := FormatValue(e)
			p := Part{Value: e, Layer: c.Layer, Shadowed: seen[k]}
			if !p.Shadowed {
				seen[k] = true
				union = append(union, e)
			}
			it.Parts = append(it.Parts, p)
		}
	}
	it.Value = union
}

// mergeMap unions object members. Per member the highest layer wins and lower
// ones are Shadowed, except that array members (hooks per event) concatenate.
// Parts are grouped by member name, highest layer first.
func mergeMap(it *Item, allowed []int) {
	var objs []int
	for _, i := range allowed {
		c := &it.Contribs[i]
		if _, ok := c.Value.(map[string]any); !ok {
			c.Status, c.Reason = StatusIgnored, "not an object"
			it.Warnings++
			continue
		}
		c.Status = StatusMerged
		objs = append(objs, i)
	}

	var names []string
	seen := map[string]bool{}
	for _, i := range objs {
		for name := range it.Contribs[i].Value.(map[string]any) {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)

	merged := map[string]any{}
	for _, name := range names {
		var win any
		have := false
		for _, i := range objs {
			c := &it.Contribs[i]
			v, ok := c.Value.(map[string]any)[name]
			if !ok {
				continue
			}
			p := Part{Name: name, Value: v, Layer: c.Layer}
			switch {
			case !have:
				win, have = v, true
			case isArray(win) && isArray(v):
				win = append(append([]any{}, win.([]any)...), v.([]any)...)
			default:
				p.Shadowed = true
			}
			it.Parts = append(it.Parts, p)
		}
		merged[name] = win
	}
	if len(objs) > 0 {
		it.Value = merged
	}
}

func isArray(v any) bool {
	_, ok := v.([]any)
	return ok
}

// cutLast splits "a.b.c" into "a.b" and "c".
func cutLast(key string) (parent, name string, ok bool) {
	i := strings.LastIndexByte(key, '.')
	if i < 0 {
		return "", key, false
	}
	return key[:i], key[i+1:], true
}

// FormatValue renders v as compact JSON with sorted object keys. Numbers keep
// the text they had in the file.
func FormatValue(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "?"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
