package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/konveyor/agentic-controller/api/skill"
)

// newRepo builds a real git repository on disk and returns a URL the loader
// can clone. Exercising the actual clone path matters more than a fake: the
// git source is the only one that runs a network-shaped operation at pod init.
func newRepo(t *testing.T, build func(dir string)) string {
	t.Helper()
	dir := t.TempDir()

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	build(dir)

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("skills", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadGitSourceSingleSkill(t *testing.T) {
	url := newRepo(t, func(dir string) {
		writeSkill(t, dir, "acme-internal", nil)
	})
	src, dest := t.TempDir(), t.TempDir()

	m, err := load(t, src, dest, Source{Name: "acme", Git: &GitSource{URL: url}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := names(m); len(got) != 1 || got[0] != "acme-internal" {
		t.Fatalf("got %v, want [acme-internal]", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "acme-internal", SkillFile)); err != nil {
		t.Fatalf("git skill not assembled: %v", err)
	}
}

// A repository of several skills is the SkillCollection case, not a card.
func TestLoadGitSourceWithSeveralSkillsNeedsSubPath(t *testing.T) {
	url := newRepo(t, func(dir string) {
		writeSkill(t, filepath.Join(dir, "one"), "one", nil)
		writeSkill(t, filepath.Join(dir, "two"), "two", nil)
	})
	src, dest := t.TempDir(), t.TempDir()

	_, err := load(t, src, dest, Source{Name: "acme", Git: &GitSource{URL: url}})
	if err == nil || !strings.Contains(err.Error(), "holds 2 skills") {
		t.Fatalf("want a several-skills error, got %v", err)
	}
}

// spec.source points at a repository; the skill is often below its root.
func TestLoadGitSourceSubPath(t *testing.T) {
	url := newRepo(t, func(dir string) {
		writeSkill(t, filepath.Join(dir, "contrib", "skills", "nested"), "nested", nil)
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a skill"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	src, dest := t.TempDir(), t.TempDir()

	m, err := load(t, src, dest,
		Source{Name: "acme", SubPath: "contrib/skills/nested", Git: &GitSource{URL: url}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := names(m); len(got) != 1 || got[0] != "nested" {
		t.Fatalf("got %v, want [nested]", got)
	}
}

func TestLoadGitSourceBadSubPath(t *testing.T) {
	url := newRepo(t, func(dir string) {
		writeSkill(t, dir, "root-skill", nil)
	})
	src, dest := t.TempDir(), t.TempDir()

	_, err := load(t, src, dest,
		Source{Name: "acme", SubPath: "does/not/exist", Git: &GitSource{URL: url}})
	if err == nil || !strings.Contains(err.Error(), "subPath") {
		t.Fatalf("want a subPath error, got %v", err)
	}
}

// A bad URL must fail the pod at init with something readable, rather than
// letting the agent start with a skill silently missing.
func TestLoadGitSourceUnreachableFailsReadably(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	missing := filepath.Join(t.TempDir(), "no-such-repo")

	_, err := load(t, src, dest, Source{Name: "acme", Git: &GitSource{URL: missing}})
	if err == nil {
		t.Fatal("want an error for an unreachable repository")
	}
	// The message names the source and the URL, which is what makes an init
	// failure diagnosable from kubectl logs alone.
	for _, want := range []string{"acme", missing} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// Git and mounted sources share one namespace, so a git skill colliding with a
// mounted one is caught like any other collision.
func TestLoadGitAndMountedSourcesCollide(t *testing.T) {
	url := newRepo(t, func(dir string) {
		writeSkill(t, dir, "plan", nil)
	})
	src, dest := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(src, "konveyor"), "plan", nil)

	_, err := load(t, src, dest,
		Source{Name: "konveyor"},
		Source{Name: "acme", Git: &GitSource{URL: url}})
	if err == nil || !strings.Contains(err.Error(), "duplicate skill name") {
		t.Fatalf("want a duplicate-name error, got %v", err)
	}
}

// All three delivery mechanisms in one run, which is the case the ADR has to
// stand behind.
func TestLoadAllThreeSourceKinds(t *testing.T) {
	url := newRepo(t, func(dir string) {
		writeSkill(t, dir, "from-git", nil)
	})
	src, dest := t.TempDir(), t.TempDir()

	// An image holding several skills, one selected.
	writeSkill(t, filepath.Join(src, "konveyor", "plan"), "plan", nil)
	writeSkill(t, filepath.Join(src, "konveyor", "javaee"), "javaee", map[string]string{
		"references/annotations.md": "map",
	})
	// A ConfigMap, symlink farm and all.
	cm := filepath.Join(src, "house-rules")
	data := filepath.Join(cm, "..2026_08_13_12_00_00.1")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: house-rules\ndescription: house rules\n---\n"
	if err := os.WriteFile(filepath.Join(data, SkillFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(data), filepath.Join(cm, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", SkillFile), filepath.Join(cm, SkillFile)); err != nil {
		t.Fatal(err)
	}

	m, err := load(t, src, dest,
		Source{Name: "konveyor", SubPath: "javaee"},
		Source{Name: "house-rules", Type: TypeRule},
		Source{Name: "acme", Git: &GitSource{URL: url}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Every skill lands one level deep, whatever produced it.
	for _, want := range []string{"javaee", "house-rules", "from-git"} {
		if _, err := os.Stat(filepath.Join(dest, want, SkillFile)); err != nil {
			t.Errorf("skill %q not assembled: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "javaee", "references", "annotations.md")); err != nil {
		t.Errorf("supporting file lost: %v", err)
	}
	// The unselected skill in the image stays out.
	if _, err := os.Stat(filepath.Join(dest, "plan")); err == nil {
		t.Error("a skill outside the subPath was assembled")
	}
	if len(m.Rules) != 1 || m.Rules[0] != "house-rules" {
		t.Errorf("rules = %v, want [house-rules]", m.Rules)
	}
}

// versionedRepo builds a repo whose history distinguishes the refs: the first
// commit is tagged v1 and holds one description, the default branch moves on
// to another, and a side branch holds a third. A ref that is honoured produces
// visibly different content from one that is not, so a checkout that silently
// no-ops cannot pass.
type versionedRepo struct {
	url      string
	v1Commit string // tagged v1, description "version one"
	branch   string // side branch, description "on a branch"
	// The default branch head says "version two".
}

func versionedSkillRepo(t *testing.T) versionedRepo {
	t.Helper()
	dir := t.TempDir()

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	commit := func(description string) string {
		writeSkillRaw(t, filepath.Join(dir, "acme"),
			"---\nname: acme\ndescription: "+description+"\n---\n\n# acme\n")
		if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
			t.Fatal(err)
		}
		h, err := wt.Commit(description, &gogit.CommitOptions{
			Author: &object.Signature{Name: "test", Email: "test@example.com"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return h.String()
	}

	out := versionedRepo{url: dir, branch: "side"}
	out.v1Commit = commit("version one")
	if _, err := repo.CreateTag("v1", plumbing.NewHash(out.v1Commit), nil); err != nil {
		t.Fatal(err)
	}

	// A side branch off v1, so it is not reachable by walking the default
	// branch and a wrong checkout cannot land on it by accident.
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(out.branch),
		Hash:   plumbing.NewHash(out.v1Commit),
		Create: true,
	}); err != nil {
		t.Fatal(err)
	}
	commit("on a branch")

	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: head.Name()}); err != nil {
		t.Fatal(err)
	}
	// Back to the default branch and move it on, so its head differs from
	// both the tag and the side branch.
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("master"),
	}); err != nil {
		t.Fatal(err)
	}
	commit("version two")

	return out
}

// descriptionOf reads back what the loader actually assembled, which is the
// only proof the requested revision was the one checked out.
func descriptionOf(t *testing.T, dest, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dest, name, SkillFile))
	if err != nil {
		t.Fatal(err)
	}
	fm, err := skill.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	return fm.Description
}

// An unpinned source clones the default branch. Recorded as behaviour rather
// than endorsed: it is what makes the run unreproducible, which is why the
// loader warns.
func TestLoadGitSourceWithoutRefTakesTheDefaultBranch(t *testing.T) {
	repo := versionedSkillRepo(t)
	src, dest := t.TempDir(), t.TempDir()

	if _, err := load(t, src, dest,
		Source{Name: "acme", Git: &GitSource{URL: repo.url}}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := descriptionOf(t, dest, "acme"); got != "version two" {
		t.Errorf("description = %q, want the default branch head", got)
	}
}

// A branch ref goes through the shallow single-branch clone.
func TestLoadGitSourceAtBranch(t *testing.T) {
	repo := versionedSkillRepo(t)
	src, dest := t.TempDir(), t.TempDir()

	if _, err := load(t, src, dest,
		Source{Name: "acme", Git: &GitSource{URL: repo.url, Ref: repo.branch}}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := descriptionOf(t, dest, "acme"); got != "on a branch" {
		t.Errorf("description = %q, want the side branch", got)
	}
}

// A tag is not a branch reference, so the shallow clone fails and the loader
// falls back to cloning and checking the revision out. Pinning to a tag is the
// ordinary way to make a run reproducible, so this path is not an edge case.
func TestLoadGitSourceAtTag(t *testing.T) {
	repo := versionedSkillRepo(t)
	src, dest := t.TempDir(), t.TempDir()

	if _, err := load(t, src, dest,
		Source{Name: "acme", Git: &GitSource{URL: repo.url, Ref: "v1"}}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := descriptionOf(t, dest, "acme"); got != "version one" {
		t.Errorf("description = %q, want the tagged commit", got)
	}
}

// A commit SHA takes the same fallback and is the strongest pin available.
func TestLoadGitSourceAtCommit(t *testing.T) {
	repo := versionedSkillRepo(t)
	src, dest := t.TempDir(), t.TempDir()

	if _, err := load(t, src, dest,
		Source{Name: "acme", Git: &GitSource{URL: repo.url, Ref: repo.v1Commit}}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := descriptionOf(t, dest, "acme"); got != "version one" {
		t.Errorf("description = %q, want the pinned commit", got)
	}
}

// A ref that does not exist must fail the pod, not quietly fall back to the
// default branch — that would run the wrong skills while reporting success.
func TestLoadGitSourceAtMissingRefFails(t *testing.T) {
	repo := versionedSkillRepo(t)
	src, dest := t.TempDir(), t.TempDir()

	_, err := load(t, src, dest,
		Source{Name: "acme", Git: &GitSource{URL: repo.url, Ref: "v99"}})
	if err == nil {
		t.Fatal("want an error for a ref that does not exist")
	}
	for _, want := range []string{"acme", "v99"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}
