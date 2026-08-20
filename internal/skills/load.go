package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/konveyor/agentic-controller/api/skill"
)

// The manifest is the contract with the harness, so its shape lives in
// api/skill next to the frontmatter rules rather than here.
const ManifestFile = skill.ManifestFile

type (
	// Loaded is one assembled skill.
	Loaded = skill.Loaded
	// Manifest is what this package writes for the harness to read.
	Manifest = skill.Manifest
)

// Source and GitSource are the controller's declaration of what it staged.
// Aliases rather than copies: the controller marshals the same types onto
// KONVEYOR_SKILL_SOURCES from the other side of the module boundary, and
// api/skill is the package both modules already import.
type Source = skill.Source

// GitSource is a repository cloned at pod start.
type GitSource = skill.GitSource

// Options configures an assembly run.
type Options struct {
	// SrcDir holds one subdirectory per mounted source: ImageVolumes and
	// ConfigMap volumes alike.
	SrcDir string
	// DestDir is the assembled skills root the agent runtime sees.
	DestDir string
	// Sources is the declared source list. When empty the loader falls back
	// to scanning SrcDir, which is what makes the command usable by hand;
	// in a pod the controller always declares them, so an unexpected
	// directory cannot quietly become skills.
	Sources []Source
}

// Loaded is one assembled skill.
// candidate is a skill directory found in a source, before validation.
type candidate struct {
	dir        string // absolute path on disk
	source     string // staging directory name
	sourcePath string // subdirectory within the source, empty if single-skill
	typeName   string // load policy the source imposes, empty to let frontmatter decide
}

// Load assembles DestDir from every source and validates the result. It fails
// before writing anything if any skill is unusable, so the agent either starts
// with a known-good skills root or does not start.
func Load(ctx context.Context, opts Options) (*Manifest, error) {
	candidates, cleanup, err := collect(ctx, opts)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		// Not an error. Skills are optional (#82) and a run with none is a
		// supported configuration.
		if err := prune(opts.DestDir, nil); err != nil {
			return nil, err
		}
		return writeManifest(opts.DestDir, &Manifest{Skills: []Loaded{}, Rules: []string{}})
	}

	// Validate everything before copying anything, so a bad skill in the last
	// source does not leave a half-assembled root behind.
	loaded, byName, err := check(candidates)
	if err != nil {
		return nil, err
	}

	// Assembly is not necessarily the first thing to touch DestDir: an init
	// container that reruns, or a by-hand invocation, finds the previous run's
	// output. A skill dropped from the sources has to leave with it, because
	// the agent runtime discovers skills by walking this directory and would
	// otherwise go on seeing one nothing declares.
	keep := make(map[string]bool, len(loaded))
	for _, l := range loaded {
		keep[l.Name] = true
	}
	// Drop the previous run's manifest before touching anything it describes.
	// Validation is all-or-nothing, but the copying below is not -- a symlink
	// out of a source, or a read error, stops it partway -- and a manifest left
	// behind would then name directories this run has already deleted. The
	// harness reads it and fails on the first missing rule, blaming a skill
	// rather than the assembly that never finished.
	if err := os.Remove(filepath.Join(opts.DestDir, ManifestFile)); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("clearing the previous manifest: %w", err)
	}
	if err := prune(opts.DestDir, keep); err != nil {
		return nil, err
	}

	for _, l := range loaded {
		src := byName[l.Name].dir
		dst := filepath.Join(opts.DestDir, l.Name)
		// Replace rather than copy over. A copy only adds and overwrites, so a
		// file the source no longer has would survive a rerun against a reused
		// directory and go on being served as part of the skill.
		if err := os.RemoveAll(dst); err != nil {
			return nil, fmt.Errorf("clearing %s before assembling %q: %w", dst, l.Name, err)
		}
		if err := copyTree(src, dst); err != nil {
			return nil, fmt.Errorf("assembling skill %q from %s: %w", l.Name, src, err)
		}
	}

	rules := make([]string, 0, len(loaded))
	for _, l := range loaded {
		if l.Type == TypeRule {
			rules = append(rules, l.Name)
		}
	}
	return writeManifest(opts.DestDir, &Manifest{Skills: loaded, Rules: rules})
}

// Validate checks a skill directory tree without assembling anything, so CI
// can catch an unusable skill at review time rather than at pod init. dir is
// one skill or a directory of them, the same shapes Load accepts. Load policy
// is not a property of content, so the reported Type is always the default.
func Validate(dir string) ([]Loaded, error) {
	candidates, err := discover(dir, filepath.Base(dir))
	if err != nil {
		return nil, err
	}
	out, _, err := check(candidates)
	return out, err
}

// check reads and validates every candidate's frontmatter and settles its load
// policy, returning what would be assembled and where each one came from.
//
// One implementation, because CI runs it through Validate and pod init runs it
// through Load: a rule that lived in only one of them would let a skill pass
// review and then fail the pod, which is the drift ADR 0015 gave the shared
// api/skill package to avoid.
func check(candidates []candidate) ([]Loaded, map[string]candidate, error) {
	loaded := make([]Loaded, 0, len(candidates))
	byName := make(map[string]candidate, len(candidates))
	var problems []string

	for _, c := range candidates {
		fm, err := readFrontmatter(filepath.Join(c.dir, SkillFile))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", c.label(), err))
			continue
		}
		if prev, dup := byName[fm.Name]; dup {
			problems = append(problems, fmt.Sprintf(
				"duplicate skill name %q: %s and %s", fm.Name, prev.label(), c.label()))
			continue
		}
		byName[fm.Name] = c

		// The destination is the frontmatter name, not the directory the skill
		// arrived in. That collapses the two namespaces, the mount path and
		// the runtime's own view of a skill's name, into one, so a collision
		// is a real collision rather than a silent walk-order choice.
		if base := filepath.Base(c.dir); base != fm.Name {
			logf("skill %q arrived as %s, assembling it under its frontmatter name", fm.Name, c.label())
		}

		// Load policy comes from the SkillCard, never from content. Validate
		// walks a bare directory with no card behind it, so typeName is empty
		// there and every skill reports the default.
		skillType := c.typeName
		switch skillType {
		case "":
			skillType = TypeSkill
		case TypeSkill, TypeRule:
		default:
			problems = append(problems, fmt.Sprintf(
				"source %q sets type %q, want %q or %q", c.source, skillType, TypeSkill, TypeRule))
			continue
		}

		loaded = append(loaded, Loaded{
			Name:        fm.Name,
			Description: fm.Description,
			Type:        skillType,
			Source:      c.source,
			SourcePath:  c.sourcePath,
		})
	}

	if len(problems) > 0 {
		return loaded, byName, fmt.Errorf("%d unusable skill(s):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	return loaded, byName, nil
}

func (c candidate) label() string {
	if c.sourcePath == "" {
		return c.source
	}
	return c.source + "/" + c.sourcePath
}

// collect finds every candidate skill across mounted sources and git clones.
// The returned cleanup removes any temporary clone directories.
func collect(ctx context.Context, opts Options) ([]candidate, func(), error) {
	var candidates []candidate
	var tmpDirs []string
	cleanup := func() {
		for _, d := range tmpDirs {
			_ = os.RemoveAll(d)
		}
	}

	sources := opts.Sources

	// "One source, one skill" is a SkillCard invariant, so it only applies to
	// sources the controller declared. Scanning is the by-hand path with no
	// cards behind it, where a directory of skills is a reasonable thing to
	// point at.
	declared := len(sources) > 0

	if !declared {
		// Fall back to whatever is staged. In a pod the controller always
		// declares the sources, so an unexpected directory cannot become
		// skills.
		scanned, err := scanSrcDir(opts.SrcDir)
		if err != nil {
			return nil, cleanup, err
		}
		sources = scanned
	}

	for _, s := range sources {
		// The name is joined onto SrcDir, so it has to be one path segment.
		// Guarded here as well as in the controller, because the loader also
		// runs by hand and against a declaration it did not write, and because
		// the sibling subPath join below has always been guarded.
		if s.Name == "" || s.Name != filepath.Base(s.Name) || s.Name == "." || s.Name == ".." {
			return nil, cleanup, fmt.Errorf(
				"source %q is not a usable directory name; it must be a single path segment", s.Name)
		}
		root := filepath.Join(opts.SrcDir, s.Name)

		if s.Git != nil {
			tmp, err := os.MkdirTemp("", "skill-clone-")
			if err != nil {
				return nil, cleanup, fmt.Errorf("temp dir for %s: %w", s.Name, err)
			}
			tmpDirs = append(tmpDirs, tmp)

			if err := clone(ctx, s.Name, *s.Git, tmp); err != nil {
				return nil, cleanup, err
			}
			root = tmp
		} else if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return nil, cleanup, fmt.Errorf(
				"source %q was declared but nothing is staged at %s", s.Name, root)
		}

		// subPath selects one skill out of a source holding several. It works
		// the same for a clone and for a mount, so it lives on the source
		// rather than on the git block.
		if s.SubPath != "" {
			root = filepath.Join(root, filepath.Clean("/"+s.SubPath))
			if info, err := os.Stat(root); err != nil || !info.IsDir() {
				return nil, cleanup, fmt.Errorf(
					"source %q: subPath %q is not a directory in it", s.Name, s.SubPath)
			}
		}

		found, err := discover(root, s.Name)
		if err != nil {
			return nil, cleanup, err
		}

		// A SkillCard is one skill. Resolving to several means the source
		// holds a set and the card did not say which, so name them rather
		// than silently turning one card into many skills.
		if declared && len(found) > 1 {
			names := make([]string, 0, len(found))
			for _, f := range found {
				names = append(names, f.sourcePath)
			}
			slices.Sort(names)
			return nil, cleanup, fmt.Errorf(
				"source %q holds %d skills (%s); set subPath to select one, or use a SkillCollection",
				s.Name, len(found), strings.Join(names, ", "))
		}

		for i := range found {
			found[i].typeName = s.Type
		}
		// Sorted within a source, but sources keep the order they were
		// declared in. The rules list is built from this order and the harness
		// injects it into the prompt in that order, so a global sort would
		// silently make rule precedence alphabetical by SkillCard name, with
		// no way for anyone to say which rule comes first.
		slices.SortFunc(found, func(a, b candidate) int { return strings.Compare(a.sourcePath, b.sourcePath) })
		candidates = append(candidates, found...)
	}

	return candidates, cleanup, nil
}

// scanSrcDir treats every staged subdirectory as an undeclared source.
func scanSrcDir(srcDir string) ([]Source, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading source dir %s: %w", srcDir, err)
	}

	var out []Source
	for _, e := range entries {
		if isKubernetesInternal(e.Name()) {
			continue
		}
		if info, err := os.Stat(filepath.Join(srcDir, e.Name())); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, Source{Name: e.Name()})
	}
	return out, nil
}

// discover decides whether a source holds one skill or several. SKILL.md at
// the root means one; otherwise every immediate subdirectory holding a
// SKILL.md is a skill. The shape is detected rather than declared, so a
// single-skill image and a bundle need no metadata to tell them apart.
func discover(dir, sourceName string) ([]candidate, error) {
	if exists(filepath.Join(dir, SkillFile)) {
		return []candidate{{dir: dir, source: sourceName}}, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading source %s: %w", sourceName, err)
	}

	var out []candidate
	for _, e := range entries {
		if isKubernetesInternal(e.Name()) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if info, err := os.Stat(sub); err != nil || !info.IsDir() {
			continue
		}
		if exists(filepath.Join(sub, SkillFile)) {
			out = append(out, candidate{dir: sub, source: sourceName, sourcePath: e.Name()})
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf(
			"source %q contributed no skills: no %s at its root and no subdirectory containing one",
			sourceName, SkillFile)
	}
	return out, nil
}

// CloneTimeout bounds a single git source. Without it an unreachable host
// leaves the init container running until something else kills it, and because
// anything short of Finished reads as Running, the AgentRun says the agent is
// working with nothing to say otherwise.
var CloneTimeout = 3 * time.Minute

func clone(ctx context.Context, name string, g GitSource, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, CloneTimeout)
	defer cancel()

	if g.Ref == "" {
		logf("git source %q has no ref, cloning the default branch of %s, so this run is not reproducible", name, g.URL)
	}
	opts := &gogit.CloneOptions{URL: g.URL, Depth: 1, SingleBranch: true}
	if g.Ref != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(g.Ref)
	}

	if _, err := gogit.PlainCloneContext(ctx, dest, false, opts); err != nil {
		if g.Ref == "" {
			return fmt.Errorf("git source %q: cloning %s: %w", name, g.URL, err)
		}
		// A tag is not a branch reference, but it is still shallow-cloneable,
		// and a pinned tag is the case the log line above pushes people
		// towards. Try it before falling back to a full clone.
		tagOpts := *opts
		tagOpts.ReferenceName = plumbing.NewTagReferenceName(g.Ref)
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		if _, err := gogit.PlainCloneContext(ctx, dest, false, &tagOpts); err == nil {
			return nil
		}
		// A commit is neither, so give up on shallow and check it out from a
		// full clone rather than guessing further.
		if err2 := cloneAndCheckout(ctx, g, dest); err2 != nil {
			return fmt.Errorf("git source %q: cloning %s at ref %q: %w", name, g.URL, g.Ref, err2)
		}
	}
	return nil
}

func cloneAndCheckout(ctx context.Context, g GitSource, dest string) error {
	// PlainCloneContext leaves the directory behind on failure.
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	repo, err := gogit.PlainCloneContext(ctx, dest, false, &gogit.CloneOptions{URL: g.URL})
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(g.Ref))
	if err != nil {
		return fmt.Errorf("resolving %q: %w", g.Ref, err)
	}
	return wt.Checkout(&gogit.CheckoutOptions{Hash: *hash})
}

// maxCopyDepth bounds the recursion below, so a symlink cycle within a source
// ends with an error naming the directory rather than running until the stack
// does.
const maxCopyDepth = 32

// copyTree copies src to dst, resolving symlinks into their content.
//
// Resolving rather than preserving is what makes a ConfigMap source work: the
// kubelet projects keys as symlinks into a timestamped ..data directory, so a
// link-preserving copy would produce a tree of dangling links once the source
// mount is out of scope.
//
// A link is followed only while it stays inside the source: skill content is
// whatever the publisher put there, and a link out of the tree would copy
// anything the init container can read, its ServiceAccount token included.
func copyTree(src, dst string) error {
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", src, err)
	}
	return copyDir(root, root, dst, 0)
}

// copyDir copies dir, which must resolve within root, to dst.
//
// A recursion rather than filepath.WalkDir, which never descends through a
// symlinked directory and would silently drop a skill's supporting files.
func copyDir(root, dir, dst string, depth int) error {
	if depth > maxCopyDepth {
		return fmt.Errorf("%s nests more than %d directories deep", dir, maxCopyDepth)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		// The kubelet's bookkeeping in a projected volume, and the clone
		// metadata a git source arrives with: .git carries the remote URL with
		// whatever credential was in it, and no skill needs it.
		if isKubernetesInternal(name) || name == ".git" {
			continue
		}
		resolved, info, err := resolveWithin(root, filepath.Join(dir, name), e)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// A link with nothing at the other end. That is no more skill
				// content than the sockets and devices skipped below, and
				// failing here would take down every run referencing the card
				// over a supporting file none of them asked for.
				logf("skipping %s: it points at nothing", filepath.Join(dir, name))
				continue
			}
			return err
		}
		switch {
		case info.IsDir():
			if err := copyDir(root, resolved, filepath.Join(dst, name), depth+1); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if err := copyFile(resolved, filepath.Join(dst, name), info.Mode().Perm()); err != nil {
				return err
			}
		}
		// Anything else -- a socket, a device -- is not skill content.
	}
	return nil
}

// resolveWithin follows path to what it really is, and refuses to leave root.
//
// entry is what os.ReadDir already reported. Anything that is not a symlink is
// where it appears to be, and dir is resolved by construction, so the walk of
// every parent component that EvalSymlinks does is only worth paying for the
// links the containment check exists for.
func resolveWithin(root, path string, entry os.DirEntry) (string, os.FileInfo, error) {
	if entry != nil && entry.Type()&os.ModeSymlink == 0 {
		info, err := entry.Info()
		if err != nil {
			return "", nil, fmt.Errorf("resolving %s: %w", path, err)
		}
		return path, info, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolving %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, resolved)
	// Compared against ".." exactly, and against a "../" prefix: a projected
	// ConfigMap's real directory is named "..<timestamp>", which is inside.
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf(
			"%s points at %s, outside the skill; a skill can only carry files its own source holds",
			path, resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, fmt.Errorf("resolving %s: %w", path, err)
	}
	return resolved, info, nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// prune removes skill directories a previous run assembled that this one did
// not. A nil keep set removes all of them.
func prune(destDir string, keep map[string]bool) error {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", destDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() || keep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(destDir, e.Name())); err != nil {
			return fmt.Errorf("removing stale skill %q: %w", e.Name(), err)
		}
		logf("removed %s, it is no longer in the sources", e.Name())
	}
	return nil
}

func writeManifest(destDir string, m *Manifest) (*Manifest, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(destDir, ManifestFile), append(b, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("writing manifest: %w", err)
	}
	return m, nil
}

// isKubernetesInternal matches the bookkeeping entries the kubelet projects
// into ConfigMap and Secret volumes: the ..data symlink and the timestamped
// directory it points at.
func isKubernetesInternal(name string) bool {
	return strings.HasPrefix(name, "..")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
