// Package startertemplates ships the pre-built starter email template
// masters that seed a new account's template library.
//
// Each master lives under masters/<alias>/ as three files:
//
//	meta.json  - catalog metadata (name, description, version, subject,
//	             declared variables)
//	body.html  - the HTML part
//	body.txt   - the plain-text part
//
// Templates use flat interpolation only: {{var}} (HTML-escaped in the HTML
// part, plain in subject/text) and {{{var}}} (raw, for pre-rendered HTML
// fragments). There are no loops, conditionals, or partials. Variables that
// are missing at render time produce empty strings.
//
// This is a leaf package: stdlib only. The content is embedded at compile
// time and validated by the QA gates in startertemplates_test.go, so a
// malformed master is a build/test failure, never a runtime surprise.
package startertemplates

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"sync"
)

//go:embed masters
var mastersFS embed.FS

// Variable describes one interpolation slot declared by a master.
type Variable struct {
	// Name is the identifier used inside {{...}} / {{{...}}} tokens.
	Name string `json:"name"`
	// Required marks variables the caller should always supply. Optional
	// variables render as empty strings when absent.
	Required bool `json:"required"`
	// Raw marks {{{...}}} slots that accept pre-rendered HTML fragments and
	// are inserted without escaping. Never feed untrusted input to raw slots.
	Raw bool `json:"raw"`
	// Description explains what the variable is and, for raw slots, the
	// exact fragment shape the template expects.
	Description string `json:"description"`
	// Example is a realistic sample value, used for previews and QA renders.
	Example string `json:"example"`
}

// Master is one parsed starter template.
type Master struct {
	Alias       string     `json:"alias"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Version     string     `json:"version"`
	Subject     string     `json:"subject"`
	Variables   []Variable `json:"variables"`
	HTMLBody    string     `json:"-"`
	TextBody    string     `json:"-"`
}

var (
	loadOnce sync.Once
	masters  []Master
	byAlias  map[string]Master
)

// load parses every embedded master exactly once. Content is embedded and
// test-gated, so any error here is a programmer error; panic loudly.
func load() {
	entries, err := fs.ReadDir(mastersFS, "masters")
	if err != nil {
		panic(fmt.Sprintf("startertemplates: reading embedded masters dir: %v", err))
	}
	byAlias = make(map[string]Master, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := "masters/" + e.Name()
		metaBytes, err := mastersFS.ReadFile(dir + "/meta.json")
		if err != nil {
			panic(fmt.Sprintf("startertemplates: %s: %v", dir, err))
		}
		var m Master
		dec := json.NewDecoder(bytes.NewReader(metaBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			panic(fmt.Sprintf("startertemplates: %s/meta.json: %v", dir, err))
		}
		if m.Alias != e.Name() {
			panic(fmt.Sprintf("startertemplates: %s/meta.json alias %q does not match directory", dir, m.Alias))
		}
		htmlBytes, err := mastersFS.ReadFile(dir + "/body.html")
		if err != nil {
			panic(fmt.Sprintf("startertemplates: %s: %v", dir, err))
		}
		textBytes, err := mastersFS.ReadFile(dir + "/body.txt")
		if err != nil {
			panic(fmt.Sprintf("startertemplates: %s: %v", dir, err))
		}
		m.HTMLBody = string(htmlBytes)
		m.TextBody = string(textBytes)
		masters = append(masters, m)
		byAlias[m.Alias] = m
	}
	sort.Slice(masters, func(i, j int) bool { return masters[i].Alias < masters[j].Alias })
}

// Catalog returns all starter template masters, sorted by alias.
// The returned slice is a copy; callers may reorder it freely.
func Catalog() []Master {
	loadOnce.Do(load)
	out := make([]Master, len(masters))
	copy(out, masters)
	return out
}

// Get returns the master with the given alias, if it exists.
func Get(alias string) (Master, bool) {
	loadOnce.Do(load)
	m, ok := byAlias[alias]
	return m, ok
}
