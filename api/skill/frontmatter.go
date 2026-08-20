/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package skill reads and validates the Agent Skills format: a SKILL.md
// carrying YAML frontmatter, optionally alongside supporting files.
// Frontmatter is the only skill metadata; there is no sidecar manifest.
//
// It lives in the api module because the controller, the loader and CI all
// validate skills and sit in different Go modules; this is the only one they
// all import (ADR 0015). It depends on nothing beyond a YAML parser.
package skill

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// File is the name of the file carrying a skill's frontmatter and content.
// The Agent Skills spec fixes it, and a directory without one is not a skill.
const File = "SKILL.md"

// Frontmatter is the subset of SKILL.md's YAML header the platform reads.
// Load policy is deliberately absent: it lives on the SkillCard, because
// `type:` is not a field the spec allows here (ADR 0015).
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// extra holds top-level keys outside the spec's set, recorded by Parse for
	// Validate to reject. A hand-built Frontmatter has none.
	extra []string
}

// allowedFields is the spec's closed set. skills-ref errors on anything
// outside it, so accepting one here would pass a skill that fails the format.
var allowedFields = map[string]bool{
	"name":          true,
	"description":   true,
	"license":       true,
	"compatibility": true,
	"metadata":      true,
	"allowed-tools": true,
}

const (
	maxNameLen        = 64
	maxDescriptionLen = 1024
)

var fence = []byte("---")

// Parse reads the YAML header delimited by --- fences at the very start of a
// SKILL.md. A file without a leading fence has no frontmatter, which is an
// error rather than a default: without name and description the skill is
// invisible to the agent runtime, so shipping one is never intended.
func Parse(content []byte) (Frontmatter, error) {
	var fm Frontmatter

	rest, ok := cutOpeningFence(content)
	if !ok {
		return fm, fmt.Errorf("no YAML frontmatter: file does not start with ---")
	}

	end := findClosingFence(rest)
	if end < 0 {
		return fm, fmt.Errorf("unterminated YAML frontmatter: no closing ---")
	}

	if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
		return fm, fmt.Errorf("parsing frontmatter: %w", err)
	}

	// Unmarshal again loosely to see the keys the struct above discards.
	// Anything the spec does not define is recorded for Validate to reject.
	var all map[string]any
	if err := yaml.Unmarshal(rest[:end], &all); err != nil {
		return fm, fmt.Errorf("parsing frontmatter: %w", err)
	}
	for k := range all {
		if !allowedFields[k] {
			fm.extra = append(fm.extra, k)
		}
	}
	sort.Strings(fm.extra)

	// A folded scalar (`description: >`) keeps a trailing newline, and every
	// shipped skill writes its description that way. The value is rendered
	// into the runtime's skill listing, so carry it without the whitespace.
	fm.Name = strings.TrimSpace(fm.Name)
	fm.Description = strings.TrimSpace(fm.Description)
	return fm, nil
}

// ParseAndValidate is Parse followed by Validate, which is what every caller
// checking bytes it did not write actually wants.
func ParseAndValidate(content []byte) (Frontmatter, error) {
	fm, err := Parse(content)
	if err != nil {
		return fm, err
	}
	return fm, fm.Validate()
}

// Validate reports why a skill would not work, or nil.
//
// The rules are the Agent Skills spec's, not ours: name and description are
// required, and they are exactly what the runtime uses for progressive
// disclosure, since name identifies the skill and description is the whole
// basis on which a model decides whether to read it.
func (fm Frontmatter) Validate() error {
	if fm.Name == "" {
		return fmt.Errorf("frontmatter has no name")
	}
	if fm.Description == "" {
		return fmt.Errorf("frontmatter has no description (skill %q would be unselectable)", fm.Name)
	}
	// Characters, not bytes: the spec counts characters, and a description
	// written in a language that is not mostly ASCII would otherwise be
	// rejected at roughly a third of its stated limit, with a count the author
	// cannot reconcile with what they wrote.
	if n := utf8.RuneCountInString(fm.Description); n > maxDescriptionLen {
		return fmt.Errorf("skill %q has a description of %d characters, the limit is %d",
			fm.Name, n, maxDescriptionLen)
	}
	if err := ValidName(fm.Name); err != nil {
		return fmt.Errorf("skill name %q: %w", fm.Name, err)
	}
	if len(fm.extra) > 0 {
		return fmt.Errorf(
			"skill %q has frontmatter fields the Agent Skills spec does not define: %s. "+
				"The spec allows only name, description, license, compatibility, metadata "+
				"and allowed-tools; put anything else under metadata",
			fm.Name, strings.Join(fm.extra, ", "))
	}
	return nil
}

// ValidName applies the spec's naming rules: 1-64 characters, lowercase
// alphanumerics and hyphens, no leading or trailing hyphen, no consecutive
// hyphens. Being stricter than a path segment needs is the point, since a name
// that fails here fails `skills-ref validate` too.
func ValidName(name string) error {
	if name == "" {
		return fmt.Errorf("is empty")
	}
	if n := utf8.RuneCountInString(name); n > maxNameLen {
		return fmt.Errorf("is %d characters, the limit is %d", n, maxNameLen)
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("cannot start or end with a hyphen")
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("cannot contain consecutive hyphens")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Errorf("contains %q; only lowercase letters, digits and hyphens are allowed", r)
		}
	}
	return nil
}

// Body returns the markdown without its YAML header, which is what belongs in
// a prompt: the frontmatter is loader bookkeeping, not instructions. Content
// with no frontmatter is returned unchanged -- it is not valid as a skill, but
// that is Validate's complaint to make, not this function's.
func Body(content string) string {
	// The same opening fence Parse accepts, so a file it reads frontmatter out
	// of cannot be one this hands back whole -- which would put the YAML header
	// into the prompt as if it were instructions.
	cut, ok := cutOpeningFence([]byte(content))
	if !ok {
		return content
	}
	rest := string(cut)
	end := findClosingFence([]byte(rest))
	if end < 0 {
		return content
	}
	// Drop the whole closing-fence line, not just the --- itself, so trailing
	// whitespace on it does not lead the body.
	body := rest[end:]
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}
	return strings.TrimLeft(body, "\r\n")
}

// bom is what Notepad and some VS Code configurations put in front of the
// first byte of a UTF-8 file. It is invisible in every editor that writes it.
var bom = []byte{0xEF, 0xBB, 0xBF}

// cutOpeningFence removes the --- that opens the frontmatter block, returning
// what follows.
//
// It is as forgiving as findClosingFence is about the line that closes the
// block, and for the same reason: an editor that does not trim on save leaves
// "--- ", and telling the author their file does not start with --- sends them
// looking for something they can plainly see. A leading BOM is invisible, so
// that one is worse again.
func cutOpeningFence(content []byte) ([]byte, bool) {
	content = bytes.TrimPrefix(content, bom)

	line, rest, found := bytes.Cut(content, []byte("\n"))
	if !found {
		return nil, false
	}
	if !bytes.Equal(bytes.TrimRight(line, " \t\r"), fence) {
		return nil, false
	}
	return rest, true
}

// findClosingFence returns the offset of the line that closes the frontmatter
// block, or -1. Only a line consisting solely of --- closes it, so a --- inside
// a quoted YAML value does not terminate the block early.
func findClosingFence(b []byte) int {
	for offset := 0; offset < len(b); {
		line := b[offset:]
		if nl := bytes.IndexByte(line, '\n'); nl >= 0 {
			line = line[:nl]
		}
		// Trailing whitespace as well as CR: an editor that does not trim on
		// save leaves "--- ", and reporting that file as having no closing
		// fence sends the author looking for something they can plainly see.
		if bytes.Equal(bytes.TrimRight(line, " \t\r"), fence) {
			return offset
		}
		nl := bytes.IndexByte(b[offset:], '\n')
		if nl < 0 {
			break
		}
		offset += nl + 1
	}
	return -1
}
