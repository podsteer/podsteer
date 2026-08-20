// Package notices embeds the third-party licence inventory K8Sense ships.
//
// Every dependency K8Sense distributes is permissively licensed — MIT, BSD,
// ISC or Apache-2.0 — and all of them require the same thing back: that the
// licence and its copyright notice travel with the binary. Embedding the
// inventory rather than generating it at runtime means the obligation is met
// by a single self-contained artefact, with no dependency on the module cache
// or node_modules existing on the machine the build ran on.
//
// The file is produced by `make notices` and committed. It is regenerated from
// what actually ships: modules linked into the binary and packages in the
// runtime dependency tree, never the build toolchain.
package notices

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed notices.json
var raw []byte

// Package is one dependency and the licence it is distributed under.
type Package struct {
	// Name is the module path or npm package name.
	Name string `json:"name"`
	// Version is the resolved version.
	Version string `json:"version"`
	// Ecosystem is "go" or "npm".
	Ecosystem string `json:"ecosystem"`
	// Licence is the SPDX-ish identifier, e.g. "MIT" or "Apache-2.0".
	Licence string `json:"licence"`
	// Copyright is the copyright line, which is the part MIT and BSD
	// specifically require to be reproduced. Empty when upstream ships none.
	Copyright string `json:"copyright"`
	// TextID keys into the deduplicated licence texts. Empty when the project
	// publishes no licence file — a handful genuinely do not, and inventing
	// one would be worse than saying so.
	TextID string `json:"textId"`
}

// inventory is the file's shape.
type inventory struct {
	Packages []Package         `json:"packages"`
	Texts    map[string]string `json:"texts"`
}

var (
	once    sync.Once
	loaded  inventory
	loadErr error
)

// load parses the embedded inventory exactly once.
func load() (inventory, error) {
	once.Do(func() {
		if err := json.Unmarshal(raw, &loaded); err != nil {
			loadErr = fmt.Errorf("notices: parsing embedded inventory: %w", err)
			return
		}
		// Sorted here rather than in the generator so the order the UI shows
		// is this package's decision: by ecosystem, then by name, which is how
		// somebody scanning for one dependency reads it.
		sort.SliceStable(loaded.Packages, func(i, j int) bool {
			left, right := loaded.Packages[i], loaded.Packages[j]
			if left.Ecosystem != right.Ecosystem {
				return left.Ecosystem < right.Ecosystem
			}
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		})
	})
	return loaded, loadErr
}

// Packages returns every shipped dependency.
func Packages() ([]Package, error) {
	data, err := load()
	if err != nil {
		return nil, err
	}
	return append([]Package(nil), data.Packages...), nil
}

// Text returns the licence text with the given id, and whether it exists.
func Text(id string) (string, bool) {
	data, err := load()
	if err != nil || id == "" {
		return "", false
	}
	text, ok := data.Texts[id]
	return text, ok
}

// Texts returns every licence text, keyed by id.
func Texts() (map[string]string, error) {
	data, err := load()
	if err != nil {
		return nil, err
	}
	texts := make(map[string]string, len(data.Texts))
	for id, text := range data.Texts {
		texts[id] = text
	}
	return texts, nil
}
