package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AddProtectedPath adds entry to protected_paths in ~/.tidymymac/config.yaml,
// creating the file (and directory) if it doesn't exist. entry is validated
// with the same rules Load applies (via normalize) BEFORE writing, so a
// typo'd/relative path is rejected immediately, not silently written and
// only discovered broken on the next Load. Already-protected (by normalized
// equivalence) is a no-op, not an error -- and skips the write entirely so a
// no-op call never touches file formatting/comments.
func AddProtectedPath(entry string) error {
	p, err := path()
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}
	return addProtectedPathAt(p, entry)
}

func addProtectedPathAt(p, entry string) error {
	clean, err := validateProtectedPathEntry(entry)
	if err != nil {
		return err
	}
	normEntry := strings.ToLower(clean)

	doc, err := readDocForEdit(p)
	if err != nil {
		return err
	}
	root := docRoot(doc)

	seq, found := findMappingValue(root, "protected_paths")
	if found {
		if seq.Kind != yaml.SequenceNode {
			return fmt.Errorf("config file %s: protected_paths is not a list, refusing to edit", p)
		}
		for _, item := range seq.Content {
			if itemClean, err := validateProtectedPathEntry(item.Value); err == nil && strings.ToLower(itemClean) == normEntry {
				return nil // already protected: no-op, don't touch the file
			}
		}
		newItem := &yaml.Node{}
		newItem.SetString(entry)
		seq.Content = append(seq.Content, newItem)
	} else {
		keyNode := &yaml.Node{}
		keyNode.SetString("protected_paths")
		newItem := &yaml.Node{}
		newItem.SetString(entry)
		valNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{newItem}}
		root.Content = append(root.Content, keyNode, valNode)
	}

	return writeDocAndVerify(p, doc)
}

// RemoveProtectedPath removes entry from protected_paths, matching by the
// same normalized-path equivalence IsProtected uses for enforcement (e.g.
// "~/Secrets" removes an entry stored as "/Users/vini/Secrets"). Not present
// is a no-op: returns (false, nil), not an error.
func RemoveProtectedPath(entry string) (removed bool, err error) {
	p, err := path()
	if err != nil {
		return false, fmt.Errorf("resolving config path: %w", err)
	}
	return removeProtectedPathAt(p, entry)
}

func removeProtectedPathAt(p, entry string) (bool, error) {
	clean, err := validateProtectedPathEntry(entry)
	if err != nil {
		return false, err
	}
	normEntry := strings.ToLower(clean)

	doc, err := readDocForEdit(p)
	if err != nil {
		return false, err
	}
	root := docRoot(doc)

	seq, found := findMappingValue(root, "protected_paths")
	if !found || len(seq.Content) == 0 {
		return false, nil
	}

	kept := make([]*yaml.Node, 0, len(seq.Content))
	removedAny := false
	for _, item := range seq.Content {
		if itemClean, err := validateProtectedPathEntry(item.Value); err == nil && strings.ToLower(itemClean) == normEntry {
			removedAny = true
			continue
		}
		kept = append(kept, item)
	}
	if !removedAny {
		return false, nil
	}
	seq.Content = kept

	if len(seq.Content) == 0 {
		removeMappingKey(root, "protected_paths")
	}

	if err := writeDocAndVerify(p, doc); err != nil {
		return false, err
	}
	return true, nil
}

// readDocForEdit reads the config file at p and parses it into a *yaml.Node
// tree ready for surgical editing. A missing or empty file yields a fresh,
// minimal document rather than an error. A present-but-invalid file (one
// that wouldn't itself pass Load's own checks -- unknown key, malformed
// YAML, an already-invalid protected_paths entry) is refused: we don't
// silently patch around a pre-existing problem in a file we're about to
// rewrite.
func readDocForEdit(p string) (*yaml.Node, error) {
	freshDoc := func() *yaml.Node {
		return &yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
		}
	}

	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return freshDoc(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", p, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return freshDoc(), nil
	}

	if cfg, err := decodeConfig(data); err != nil {
		return nil, fmt.Errorf("config file %s: unrecognized or malformed content, refusing to edit: %w", p, err)
	} else if err := cfg.normalize(); err != nil {
		return nil, fmt.Errorf("config file %s: refusing to edit: %w", p, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("config file %s is not valid YAML, refusing to edit: %w", p, err)
	}
	return &doc, nil
}

// docRoot returns the top-level mapping node of doc, drilling past the
// DocumentNode wrapper that yaml.Unmarshal (and readDocForEdit's freshDoc)
// produce.
func docRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

// findMappingValue returns the value node for key in mapping's flat
// alternating key/value Content slice.
func findMappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1], true
		}
	}
	return nil, false
}

// removeMappingKey removes the key/value pair for key from mapping's
// Content, if present.
func removeMappingKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// writeDocAndVerify encodes doc and atomically writes it to p (mirroring
// internal/history's temp-file-then-rename pattern, since this is a
// safety-critical file that must never be left truncated by a crash
// mid-write), then reloads it once purely to confirm the file we just wrote
// still satisfies Load's own invariants -- catching a self-inflicted
// Node-surgery bug at protect/unprotect time instead of the next real clean
// run.
func writeDocAndVerify(p string, doc *yaml.Node) error {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "config-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}

	if _, err := loadFrom(p); err != nil {
		return fmt.Errorf("wrote config file %s but it failed to reload back (internal bug): %w", p, err)
	}
	return nil
}
