package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file is the profiles half of the config write path. It applies the
// same node-surgery approach write.go uses for protected_paths, one level
// deeper (root -> profiles -> <name> -> categories/paths), and reuses
// readDocForEdit/writeDocAndVerify unchanged: every mutation here is an
// atomic write followed by a reload that must satisfy Load's own invariants.

// CreateProfile adds an empty profile named name to ~/.tidymymac/config.yaml.
// Unlike AddProtectedPath's silent no-op on duplicates, an existing profile is
// an error: "create" is an explicit request for a *new* profile, so silently
// doing nothing would hide a name collision with a profile the user forgot
// about.
func CreateProfile(name string) error {
	p, err := path()
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}
	return createProfileAt(p, name)
}

func createProfileAt(p, name string) error {
	if err := validateProfileName(name); err != nil {
		return err
	}

	doc, err := readDocForEdit(p)
	if err != nil {
		return err
	}
	root := docRoot(doc)

	profiles, err := profilesMapping(root, p, true)
	if err != nil {
		return err
	}
	if _, exists := findMappingValue(profiles, name); exists {
		return fmt.Errorf("profile %q already exists", name)
	}

	keyNode := &yaml.Node{}
	keyNode.SetString(name)
	profiles.Content = append(profiles.Content, keyNode, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})

	return writeDocAndVerify(p, doc)
}

// DeleteProfile removes the profile named name. Not present is a no-op:
// returns (false, nil), not an error -- same shape as RemoveProtectedPath.
func DeleteProfile(name string) (removed bool, err error) {
	p, err := path()
	if err != nil {
		return false, fmt.Errorf("resolving config path: %w", err)
	}
	return deleteProfileAt(p, name)
}

func deleteProfileAt(p, name string) (bool, error) {
	if err := validateProfileName(name); err != nil {
		return false, err
	}

	doc, err := readDocForEdit(p)
	if err != nil {
		return false, err
	}
	root := docRoot(doc)

	profiles, err := profilesMapping(root, p, false)
	if err != nil {
		return false, err
	}
	if profiles == nil {
		return false, nil
	}
	if _, exists := findMappingValue(profiles, name); !exists {
		return false, nil
	}

	removeMappingKey(profiles, name)
	if len(profiles.Content) == 0 {
		removeMappingKey(root, "profiles")
	}

	if err := writeDocAndVerify(p, doc); err != nil {
		return false, err
	}
	return true, nil
}

// AddProfileCategory appends category to the profile's category list. The
// profile must already exist. Already listed is a no-op that skips the write
// entirely, so it never touches the file's formatting or comments.
func AddProfileCategory(name, category string) error {
	p, err := path()
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}
	return addProfileCategoryAt(p, name, category)
}

func addProfileCategoryAt(p, name, category string) error {
	category = strings.TrimSpace(category)
	if category == "" {
		return fmt.Errorf("category must not be empty")
	}

	doc, _, profile, err := openProfileForEdit(p, name)
	if err != nil {
		return err
	}

	seq, err := profileSequence(profile, p, name, "categories", true)
	if err != nil {
		return err
	}
	for _, item := range seq.Content {
		if item.Value == category {
			return nil // already listed: no-op, don't touch the file
		}
	}

	newItem := &yaml.Node{}
	newItem.SetString(category)
	seq.Content = append(seq.Content, newItem)

	return writeDocAndVerify(p, doc)
}

// RemoveProfileCategory drops category from the profile's category list. The
// profile must exist; a category that isn't listed is a no-op returning
// (false, nil).
func RemoveProfileCategory(name, category string) (removed bool, err error) {
	p, err := path()
	if err != nil {
		return false, fmt.Errorf("resolving config path: %w", err)
	}
	return removeProfileCategoryAt(p, name, category)
}

func removeProfileCategoryAt(p, name, category string) (bool, error) {
	category = strings.TrimSpace(category)
	if category == "" {
		return false, fmt.Errorf("category must not be empty")
	}

	doc, _, profile, err := openProfileForEdit(p, name)
	if err != nil {
		return false, err
	}

	seq, err := profileSequence(profile, p, name, "categories", false)
	if err != nil {
		return false, err
	}
	if seq == nil || len(seq.Content) == 0 {
		return false, nil
	}

	kept := make([]*yaml.Node, 0, len(seq.Content))
	removedAny := false
	for _, item := range seq.Content {
		if item.Value == category {
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
		removeMappingKey(profile, "categories")
	}

	if err := writeDocAndVerify(p, doc); err != nil {
		return false, err
	}
	return true, nil
}

// AddProfilePath appends entry to the profile's path list. entry is validated
// with validateProfilePathEntry BEFORE writing -- this is one of the two
// enforcement points for the broad-root rule (the other being ResolveProfile),
// so a typo like "~" is rejected here rather than discovered at scan time.
// Already listed (by normalized equivalence) is a no-op.
func AddProfilePath(name, entry string) error {
	p, err := path()
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}
	return addProfilePathAt(p, name, entry)
}

func addProfilePathAt(p, name, entry string) error {
	clean, err := validateProfilePathEntry(entry)
	if err != nil {
		return err
	}
	normEntry := strings.ToLower(clean)

	doc, _, profile, err := openProfileForEdit(p, name)
	if err != nil {
		return err
	}

	seq, err := profileSequence(profile, p, name, "paths", true)
	if err != nil {
		return err
	}
	for _, item := range seq.Content {
		if itemClean, err := validateProfilePathEntry(item.Value); err == nil && strings.ToLower(itemClean) == normEntry {
			return nil // already listed: no-op, don't touch the file
		}
	}

	newItem := &yaml.Node{}
	newItem.SetString(entry)
	seq.Content = append(seq.Content, newItem)

	return writeDocAndVerify(p, doc)
}

// RemoveProfilePath drops entry from the profile's path list, matching by the
// same normalized equivalence AddProfilePath dedupes on (so "~/proj" removes
// an entry stored as "/Users/vini/proj"). Not listed is a no-op returning
// (false, nil).
func RemoveProfilePath(name, entry string) (removed bool, err error) {
	p, err := path()
	if err != nil {
		return false, fmt.Errorf("resolving config path: %w", err)
	}
	return removeProfilePathAt(p, name, entry)
}

func removeProfilePathAt(p, name, entry string) (bool, error) {
	clean, err := validateProfilePathEntry(entry)
	if err != nil {
		return false, err
	}
	normEntry := strings.ToLower(clean)

	doc, _, profile, err := openProfileForEdit(p, name)
	if err != nil {
		return false, err
	}

	seq, err := profileSequence(profile, p, name, "paths", false)
	if err != nil {
		return false, err
	}
	if seq == nil || len(seq.Content) == 0 {
		return false, nil
	}

	kept := make([]*yaml.Node, 0, len(seq.Content))
	removedAny := false
	for _, item := range seq.Content {
		if itemClean, err := validateProfilePathEntry(item.Value); err == nil && strings.ToLower(itemClean) == normEntry {
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
		removeMappingKey(profile, "paths")
	}

	if err := writeDocAndVerify(p, doc); err != nil {
		return false, err
	}
	return true, nil
}

// validateProfileName rejects names that would be confusing to type back on
// the command line or to spot in the config file.
func validateProfileName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("profile name must not be empty")
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("profile name %q must not start or end with whitespace", name)
	}
	return nil
}

// openProfileForEdit reads p, locates the named profile and returns the
// document (for writing) alongside the profile's mapping node. A missing
// config file, a missing "profiles" key or a missing profile all produce the
// same actionable error.
func openProfileForEdit(p, name string) (doc *yaml.Node, root *yaml.Node, profile *yaml.Node, err error) {
	if err := validateProfileName(name); err != nil {
		return nil, nil, nil, err
	}

	doc, err = readDocForEdit(p)
	if err != nil {
		return nil, nil, nil, err
	}
	root = docRoot(doc)

	profiles, err := profilesMapping(root, p, false)
	if err != nil {
		return nil, nil, nil, err
	}
	if profiles == nil {
		return nil, nil, nil, errProfileNotFound(name)
	}

	node, ok := findMappingValue(profiles, name)
	if !ok {
		return nil, nil, nil, errProfileNotFound(name)
	}
	if !coerceToKind(node, yaml.MappingNode, "!!map") {
		return nil, nil, nil, fmt.Errorf("config file %s: profile %q is not a mapping, refusing to edit", p, name)
	}

	return doc, root, node, nil
}

func errProfileNotFound(name string) error {
	return fmt.Errorf("profile %q does not exist; create it first with \"tidymymac profile create %s\"", name, name)
}

// profilesMapping returns the top-level "profiles" mapping node, creating it
// when create is set. Returns (nil, nil) when absent and create is false.
func profilesMapping(root *yaml.Node, p string, create bool) (*yaml.Node, error) {
	node, found := findMappingValue(root, "profiles")
	if !found {
		if !create {
			return nil, nil
		}
		keyNode := &yaml.Node{}
		keyNode.SetString("profiles")
		valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, keyNode, valNode)
		return valNode, nil
	}

	if !coerceToKind(node, yaml.MappingNode, "!!map") {
		return nil, fmt.Errorf("config file %s: profiles is not a mapping, refusing to edit", p)
	}
	return node, nil
}

// profileSequence returns the "categories" or "paths" sequence node of a
// profile, creating it when create is set. Returns (nil, nil) when absent and
// create is false.
func profileSequence(profile *yaml.Node, p, name, key string, create bool) (*yaml.Node, error) {
	node, found := findMappingValue(profile, key)
	if !found {
		if !create {
			return nil, nil
		}
		keyNode := &yaml.Node{}
		keyNode.SetString(key)
		valNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		profile.Content = append(profile.Content, keyNode, valNode)
		return valNode, nil
	}

	if !coerceToKind(node, yaml.SequenceNode, "!!seq") {
		return nil, fmt.Errorf("config file %s: %s of profile %q is not a list, refusing to edit", p, key, name)
	}
	return node, nil
}

// coerceToKind reports whether node is (or can be treated as) the given kind,
// rewriting it in place when needed. Two cases matter: an explicitly empty
// value ("profiles:" with nothing after it) parses as a null scalar and is
// really an empty collection; and an empty flow collection ("dev: {}", which
// is exactly what CreateProfile writes) must drop its flow style before we
// append to it, or the result marshals back as an unreadable one-liner.
func coerceToKind(node *yaml.Node, kind yaml.Kind, tag string) bool {
	if node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || strings.TrimSpace(node.Value) == "") {
		node.Kind = kind
		node.Tag = tag
		node.Value = ""
		node.Style = 0
		node.Content = nil
		return true
	}
	if node.Kind != kind {
		return false
	}
	if len(node.Content) == 0 {
		node.Style = 0
	}
	return true
}
