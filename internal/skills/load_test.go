package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates a skill directory with valid frontmatter and optional
// extra files, keyed by path relative to the skill directory. There is no type
// argument: load policy is a SkillCard field, never content.
// Fixture names shared by the tests in this package.
const (
	srcAcme       = "acme"
	srcKonveyor   = "konveyor"
	skillPlan     = "plan"
	skillJavaee   = "javaee"
	skillHouse    = "house-rules"
	testNamespace = "default"
)

func writeSkill(t *testing.T, dir, name string, extra map[string]string) {
	t.Helper()
	writeSkillRaw(t, dir, "---\nname: "+name+"\ndescription: does "+name+"\n---\n\n# "+name+"\n")
	for rel, content := range extra {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writeSkillRaw writes a SKILL.md verbatim, for content the helper above
// cannot express.
func writeSkillRaw(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T, src, dest string, sources ...Source) (*Manifest, error) {
	t.Helper()
	return Load(context.Background(), Options{SrcDir: src, DestDir: dest, Sources: sources})
}

func names(m *Manifest) []string {
	out := make([]string, 0, len(m.Skills))
	for _, s := range m.Skills {
		out = append(out, s.Name)
	}
	return out
}

// A single-skill image: SKILL.md at the root of the source.
func TestLoadSingleSkillSource(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, "ejb-to-cdi"), "ejb-to-cdi", nil)

	m, err := load(t, src, dest)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := names(m); len(got) != 1 || got[0] != "ejb-to-cdi" {
		t.Fatalf("got %v, want [ejb-to-cdi]", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "ejb-to-cdi", SkillFile)); err != nil {
		t.Fatalf("skill not assembled: %v", err)
	}
}

// Scanning has no SkillCards behind it, so pointing at a directory of skills
// by hand is fine. The one-skill rule is a card invariant, not a file-layout
// one.
func TestLoadScanAcceptsSeveralSkillsPerDirectory(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	multi := filepath.Join(src, srcKonveyor)
	writeSkill(t, filepath.Join(multi, skillPlan), skillPlan, nil)
	writeSkill(t, filepath.Join(multi, "execute"), "execute", nil)

	m, err := load(t, src, dest)
	if err != nil {
		t.Fatalf("scanning should accept a directory of skills: %v", err)
	}
	if len(m.Skills) != 2 {
		t.Fatalf("got %v, want both skills", names(m))
	}
}

// A source holding several skills needs subPath to say which, because a
// SkillCard is one skill.
func TestLoadMultiSkillSourceNeedsSubPath(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	multi := filepath.Join(src, srcKonveyor)
	writeSkill(t, filepath.Join(multi, skillPlan), skillPlan, nil)
	writeSkill(t, filepath.Join(multi, "execute"), "execute", nil)
	writeSkill(t, filepath.Join(multi, "verify"), "verify", nil)

	_, err := load(t, src, dest, Source{Name: srcKonveyor})
	if err == nil {
		t.Fatal("want an error when one card resolves to several skills")
	}
	// The message has to name what it found, or the user cannot pick.
	for _, want := range []string{"holds 3 skills", skillPlan, "execute", "verify", "subPath"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// subPath picks one skill out, and it flattens so the mount contract stays
// one level deep.
func TestLoadSubPathSelectsOneSkill(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	multi := filepath.Join(src, srcKonveyor)
	writeSkill(t, filepath.Join(multi, skillPlan), skillPlan, map[string]string{
		"references/phases.md": "phases",
	})
	writeSkill(t, filepath.Join(multi, "execute"), "execute", nil)

	m, err := load(t, src, dest, Source{Name: srcKonveyor, SubPath: skillPlan})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := names(m); len(got) != 1 || got[0] != skillPlan {
		t.Fatalf("got %v, want [plan]", got)
	}
	if _, err := os.Stat(filepath.Join(dest, skillPlan, SkillFile)); err != nil {
		t.Errorf("not assembled one level deep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, skillPlan, "references", "phases.md")); err != nil {
		t.Errorf("supporting file not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "execute")); err == nil {
		t.Error("a skill outside the subPath was assembled")
	}
}

// Two cards selecting different skills out of the same image, which is what
// "publish one image, reference skills individually" looks like.
func TestLoadTwoSubPathsFromOneImage(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	for _, stage := range []string{"skill-plan", "skill-execute"} {
		writeSkill(t, filepath.Join(src, stage, skillPlan), skillPlan, nil)
		writeSkill(t, filepath.Join(src, stage, "execute"), "execute", nil)
	}

	m, err := load(t, src, dest,
		Source{Name: "skill-plan", SubPath: skillPlan, Type: TypeRule},
		Source{Name: "skill-execute", SubPath: "execute"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(m.Skills) != 2 {
		t.Fatalf("got %v, want plan and execute", names(m))
	}
	if len(m.Rules) != 1 || m.Rules[0] != skillPlan {
		t.Errorf("rules = %v, want [plan]", m.Rules)
	}
}

func TestLoadBadSubPath(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, srcKonveyor, skillPlan), skillPlan, nil)

	_, err := load(t, src, dest, Source{Name: srcKonveyor, SubPath: "nope"})
	if err == nil || !strings.Contains(err.Error(), "subPath") {
		t.Fatalf("want a subPath error, got %v", err)
	}
}

// The destination is the frontmatter name, not the directory the skill arrived
// in, which is what collapses the mount namespace and the runtime's into one.
func TestLoadUsesFrontmatterNameNotDirectoryName(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, "some-skillcard-name"), "actual-skill-name", nil)

	if _, err := load(t, src, dest); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "actual-skill-name", SkillFile)); err != nil {
		t.Errorf("not assembled under its frontmatter name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "some-skillcard-name")); err == nil {
		t.Error("assembled under the source directory name as well")
	}
}

// Load policy comes from the SkillCard the source arrived on, never from the
// content. Otherwise any skill an operator mounts could promote itself into
// every prompt.
func TestLoadRulesListComesFromTheSource(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, nil)
	writeSkill(t, filepath.Join(src, skillJavaee), skillJavaee, nil)

	m, err := load(t, src, dest,
		Source{Name: skillPlan, Type: TypeRule},
		Source{Name: skillJavaee})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(m.Rules) != 1 || m.Rules[0] != skillPlan {
		t.Fatalf("rules = %v, want only [plan]", m.Rules)
	}
	for _, sk := range m.Skills {
		if sk.Name != skillPlan && sk.Type != TypeSkill {
			t.Errorf("skill %q has type %q, want the default", sk.Name, sk.Type)
		}
	}
}

// A `type:` key is not an Agent Skills field, so a skill carrying one is
// malformed and the pod does not start. Ignoring it would leave an author
// believing they had set a load policy that nothing reads.
func TestLoadRejectsTypeInFrontmatter(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkillRaw(t, filepath.Join(src, "sneaky"),
		"---\nname: sneaky\ndescription: d\ntype: rule\n---\n")

	if _, err := load(t, src, dest, Source{Name: "sneaky"}); err == nil {
		t.Fatal("want an error: type is not a frontmatter field")
	}
}

func TestLoadRejectsInvalidSourceType(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, "vendor"), "a", nil)

	_, err := load(t, src, dest, Source{Name: "vendor", Type: "constraint"})
	if err == nil || !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("want an error naming the bad type, got %v", err)
	}
}

// A declared source that was never staged is an error, not a silent absence:
// the controller said it would be there.
func TestLoadRejectsDeclaredButUnstagedSource(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()

	_, err := load(t, src, dest, Source{Name: "missing"})
	if err == nil || !strings.Contains(err.Error(), "nothing is staged") {
		t.Fatalf("want an unstaged-source error, got %v", err)
	}
}

func TestLoadRejectsDuplicateFrontmatterNames(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	// Two sources, each shipping a skill that calls itself skillPlan.
	writeSkill(t, filepath.Join(src, srcKonveyor), skillPlan, nil)
	writeSkill(t, filepath.Join(src, "vendor"), skillPlan, nil)

	_, err := load(t, src, dest)
	if err == nil {
		t.Fatal("want an error on a duplicate skill name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate skill name") {
		t.Fatalf("error should name the collision, got: %v", err)
	}
	// Both origins named, so the message is actionable.
	for _, want := range []string{srcKonveyor, "vendor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestLoadRejectsMissingFrontmatter(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkillRaw(t, filepath.Join(src, "bare"), "# no frontmatter\n")

	_, err := load(t, src, dest)
	if err == nil {
		t.Fatal("want an error for a skill with no frontmatter, got nil")
	}
	if !strings.Contains(err.Error(), "unusable skill") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsDescriptionlessSkill(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkillRaw(t, filepath.Join(src, "nodesc"), "---\nname: nodesc\n---\n")

	_, err := load(t, src, dest)
	if err == nil || !strings.Contains(err.Error(), "no description") {
		t.Fatalf("want a description error, got %v", err)
	}
}

// The spec's name rules apply to real skills, not just to unit-tested structs.
func TestLoadRejectsNonConformantName(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkillRaw(t, filepath.Join(src, "shouty"), "---\nname: Shouty_Name\ndescription: d\n---\n")

	_, err := load(t, src, dest)
	if err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Fatalf("want a name error, got %v", err)
	}
}

func TestLoadRejectsSourceWithNoSkills(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "empty", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := load(t, src, dest)
	if err == nil || !strings.Contains(err.Error(), "contributed no skills") {
		t.Fatalf("want a no-skills error, got %v", err)
	}
}

// Nothing is written when any skill in the set is bad, so a failed init never
// leaves a half-assembled root behind.
func TestLoadIsAllOrNothing(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, "good"), "good", nil)
	writeSkillRaw(t, filepath.Join(src, "bad"), "no frontmatter")

	if _, err := load(t, src, dest); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(filepath.Join(dest, "good")); err == nil {
		t.Error("a valid skill was assembled despite the set failing validation")
	}
}

// A ConfigMap volume is a symlink farm: keys are symlinks into a timestamped
// ..data directory. A link-preserving copy would leave dangling links once the
// source mount is gone.
func TestLoadResolvesConfigMapSymlinkFarm(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	cm := filepath.Join(src, skillHouse)
	data := filepath.Join(cm, "..2026_08_13_12_00_00.123456")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: house-rules\ndescription: house rules\n---\n\nno javax\n"
	if err := os.WriteFile(filepath.Join(data, SkillFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(data), filepath.Join(cm, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", SkillFile), filepath.Join(cm, SkillFile)); err != nil {
		t.Fatal(err)
	}

	m, err := load(t, src, dest)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := names(m); len(got) != 1 || got[0] != skillHouse {
		t.Fatalf("got %v, want [house-rules]", got)
	}

	// The assembled file is real content, not a link.
	out := filepath.Join(dest, skillHouse, SkillFile)
	info, err := os.Lstat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("assembled SKILL.md is a symlink; it would dangle once the mount is gone")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("content mismatch:\ngot  %q\nwant %q", got, body)
	}

	// The kubelet's bookkeeping entries do not become skills or stray files.
	entries, err := os.ReadDir(filepath.Join(dest, skillHouse))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "..") {
			t.Errorf("copied kubelet internal entry %q", e.Name())
		}
	}
}

func TestLoadWritesManifest(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, nil)
	writeSkill(t, filepath.Join(src, skillJavaee), skillJavaee, nil)

	if _, err := load(t, src, dest,
		Source{Name: skillPlan, Type: TypeRule},
		Source{Name: skillJavaee}); err != nil {
		t.Fatalf("load: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dest, ManifestFile))
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if len(m.Skills) != 2 {
		t.Errorf("manifest lists %d skills, want 2", len(m.Skills))
	}
	if len(m.Rules) != 1 || m.Rules[0] != skillPlan {
		t.Errorf("manifest rules = %v, want [plan]", m.Rules)
	}
	// The source is recorded, so the shadowing check has something to compare
	// against rather than re-walking.
	for _, s := range m.Skills {
		if s.Source == "" {
			t.Errorf("skill %q records no source", s.Name)
		}
	}
}

// Skills are optional (#82): a run with none is a supported configuration,
// not a failure.
func TestLoadWithNoSources(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()

	m, err := load(t, src, dest)
	if err != nil {
		t.Fatalf("no sources should not be an error: %v", err)
	}
	if len(m.Skills) != 0 {
		t.Errorf("got %v, want no skills", names(m))
	}
	if _, err := os.Stat(filepath.Join(dest, ManifestFile)); err != nil {
		t.Errorf("manifest should still be written: %v", err)
	}
}

func TestLoadMissingSourceDirIsNotAnError(t *testing.T) {
	dest := t.TempDir()

	if _, err := load(t, filepath.Join(t.TempDir(), "absent"), dest); err != nil {
		t.Fatalf("a missing source dir should behave like no sources: %v", err)
	}
}

// The end-to-end shape the harness relies on: what Load assembled is what
// ReadManifest and RuleContent hand back, with no second walk of the tree.
// A pod with no skills at all has no manifest. That is an ordinary run, not a
// failure, so the harness must not refuse to start over it.
// A manifest that cannot be parsed means the loader and the harness disagree
// about what is mounted. Guessing would drop rules silently.
// A rule named in the manifest but missing from the tree means the prompt
// would quietly lose a rule the run was told it has.
// Manifest order is assembly order, and the prompt preserves it, so a run's
// rules read the same way every time.
// A skill can only carry files its own source holds. Following a link out of
// the tree would copy whatever the init container can read -- its projected
// ServiceAccount token, for one -- into the root the agent reads.
func TestLoadRefusesASymlinkOutOfTheSource(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	secret := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(secret, []byte("SUPER-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(src, srcAcme), srcAcme, nil)
	if err := os.Symlink(secret, filepath.Join(src, srcAcme, "notes.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := load(t, src, dest, Source{Name: srcAcme}); err == nil {
		t.Fatal("want an error when a skill links outside its source")
	}
	if b, err := os.ReadFile(filepath.Join(dest, srcAcme, "notes.md")); err == nil {
		t.Fatalf("the linked content was copied anyway: %q", b)
	}
}

// A symlinked directory inside the source is content, and its files have to
// arrive. filepath.WalkDir never descends into one.
func TestLoadFollowsASymlinkedDirectoryInsideTheSource(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	skill := filepath.Join(src, srcAcme)
	writeSkill(t, skill, srcAcme, map[string]string{"shared/patterns.md": "# patterns"})
	if err := os.Symlink(filepath.Join(skill, "shared"), filepath.Join(skill, "references")); err != nil {
		t.Fatal(err)
	}

	if _, err := load(t, src, dest, Source{Name: srcAcme}); err != nil {
		t.Fatalf("load: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dest, srcAcme, "references", "patterns.md"))
	if err != nil {
		t.Fatalf("the symlinked directory arrived empty: %v", err)
	}
	if string(b) != "# patterns" {
		t.Errorf("content = %q", b)
	}
}

// A git source whose SKILL.md sits at the repository root brings the whole
// clone with it, and .git holds the remote URL with whatever credential was
// in it.
func TestLoadDoesNotCopyGitMetadata(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	skill := filepath.Join(src, srcAcme)
	writeSkill(t, skill, srcAcme, map[string]string{
		".git/config":   "[remote \"origin\"]\n\turl = https://x-access-token:SECRET@example.com/o/r\n",
		"references.md": "# refs",
	})

	if _, err := load(t, src, dest, Source{Name: srcAcme}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, srcAcme, ".git")); !os.IsNotExist(err) {
		t.Error(".git was copied into the skills root")
	}
	if _, err := os.Stat(filepath.Join(dest, srcAcme, "references.md")); err != nil {
		t.Errorf("ordinary supporting files should still arrive: %v", err)
	}
}

// The agent runtime discovers skills by walking the assembled root, so a skill
// dropped from the sources has to leave the disk as well as the manifest.
func TestLoadRemovesASkillTheSourcesNoLongerDeclare(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, "alpha"), "alpha", nil)
	writeSkill(t, filepath.Join(src, "beta"), "beta", nil)

	if _, err := load(t, src, dest, Source{Name: "alpha"}); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if _, err := load(t, src, dest, Source{Name: "beta"}); err != nil {
		t.Fatalf("second load: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "alpha")); !os.IsNotExist(err) {
		t.Error("alpha survived a load that does not declare it")
	}
	if _, err := os.Stat(filepath.Join(dest, "beta", SkillFile)); err != nil {
		t.Errorf("beta should be assembled: %v", err)
	}
}

// The name is joined onto the staging directory on both sides of the
// boundary, so it has to be one path segment.
func TestLoadRejectsATraversingSourceName(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, srcAcme), srcAcme, nil)

	if _, err := load(t, src, dest, Source{Name: "../../etc"}); err == nil {
		t.Fatal("want an error when a source name escapes the staging directory")
	}
}

// A rerun against a reused destination has to reflect the source as it is now.
// Copying only adds and overwrites, so without clearing first, a file the
// source has since dropped survives and is still served as part of the skill.
func TestLoadDropsFilesTheSourceNoLongerHas(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, map[string]string{
		"references/old.md": "stale guidance",
	})
	if _, err := load(t, src, dest, Source{Name: skillPlan}); err != nil {
		t.Fatalf("first load: %v", err)
	}
	stale := filepath.Join(dest, skillPlan, "references", "old.md")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("first load did not produce the file: %v", err)
	}

	// The source drops it, the way an edited ConfigMap or a moved git ref would.
	if err := os.RemoveAll(filepath.Join(src, skillPlan, "references")); err != nil {
		t.Fatal(err)
	}
	if _, err := load(t, src, dest, Source{Name: skillPlan}); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("%s survived a rerun whose source no longer has it", stale)
	}
	// The skill itself is still assembled.
	if _, err := os.Stat(filepath.Join(dest, skillPlan, SkillFile)); err != nil {
		t.Errorf("the skill went missing: %v", err)
	}
}
