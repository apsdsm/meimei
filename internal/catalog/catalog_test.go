package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/apsdsm/meimei/internal/config"
)

// project builds a throwaway project: a .meimei.toml plus whatever Dockerfiles
// the test names, and returns the loaded config.
func project(t *testing.T, toml string, dockerfiles ...string) *config.Config {
	t.Helper()
	root := t.TempDir()

	path := filepath.Join(root, config.FileName)
	// Every fixture gets the format version, so the fixtures below stay about
	// what they are testing.
	body := "version = 2\n" + toml
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range dockerfiles {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("FROM golang:1.25 AS builder\nFROM alpine:3.21\nEXPOSE 40102\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	return cfg
}

const twoServices = `
[project]
name = "jjc2"

[[images]]
name = "api"
dockerfile = "services/api/Dockerfile"
group = "api"

[[images]]
name = "user-web"
dockerfile = "services/web/Dockerfile"
group = "web"
`

func TestLoadBuildable(t *testing.T) {
	cfg := project(t, twoServices, "services/api/Dockerfile", "services/web/Dockerfile")
	cat := Load(cfg, "")

	if len(cat.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(cat.Entries))
	}
	for _, e := range cat.Entries {
		if e.Status != Buildable {
			t.Errorf("%s: status = %v (%s), want buildable", e.Image.Name, e.Status, e.Problem)
		}
	}

	api, ok := cat.Find("api")
	if !ok {
		t.Fatal("Find(api) found nothing")
	}
	if api.Repository != "jjc2-api" {
		t.Errorf("Repository = %q, want jjc2-api", api.Repository)
	}
	if api.Info.Base() != "golang" {
		t.Errorf("Base = %q, want golang", api.Info.Base())
	}
	if len(api.Info.Stages) != 2 {
		t.Errorf("stages = %d, want 2", len(api.Info.Stages))
	}
}

// A missing Dockerfile marks one service broken and leaves the rest alone —
// the whole reason it isn't a load error.
func TestLoadMissingDockerfileIsPerService(t *testing.T) {
	cfg := project(t, twoServices, "services/api/Dockerfile")
	cat := Load(cfg, "")

	api, _ := cat.Find("api")
	if api.Status != Buildable {
		t.Errorf("api status = %v, want buildable", api.Status)
	}

	web, _ := cat.Find("user-web")
	if web.Status != Broken {
		t.Fatalf("user-web status = %v, want broken", web.Status)
	}
	if web.Problem == "" {
		t.Error("a broken entry must explain itself")
	}

	buildable, broken, disabled := cat.Counts()
	if buildable != 1 || broken != 1 || disabled != 0 {
		t.Errorf("counts = %d/%d/%d, want 1 buildable, 1 broken, 0 disabled", buildable, broken, disabled)
	}
}

func TestLoadDisabledIsNotInspected(t *testing.T) {
	// No Dockerfile written: a disabled service is excluded by declaration, so
	// its missing file must not be reported as a problem.
	cfg := project(t, `
[project]
name = "jjc2"

[[images]]
name = "retired"
dockerfile = "services/gone/Dockerfile"
disabled = true
`)
	cat := Load(cfg, "")

	e := cat.Entries[0]
	if e.Status != Disabled {
		t.Errorf("status = %v, want disabled", e.Status)
	}
	if e.Problem != "" {
		t.Errorf("Problem = %q, want a disabled service to report none", e.Problem)
	}
}

func TestLoadFileWithoutFromIsBroken(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(`version = 2

[project]
name = "jjc2"

[[images]]
name = "api"
dockerfile = "Dockerfile"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The commonest way to get a FROM-less file is pointing at the wrong one.
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("# notes, not a build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatal(err)
	}

	e := Load(cfg, "").Entries[0]
	if e.Status != Broken {
		t.Errorf("status = %v, want broken for a file with no FROM", e.Status)
	}
}

func TestLoadDirectoryAtDockerfilePathIsBroken(t *testing.T) {
	cfg := project(t, `
[project]
name = "jjc2"

[[images]]
name = "api"
dockerfile = "services/api/Dockerfile"
`)
	if err := os.MkdirAll(filepath.Join(cfg.Root, "services/api/Dockerfile"), 0o755); err != nil {
		t.Fatal(err)
	}

	e := Load(cfg, "").Entries[0]
	if e.Status != Broken {
		t.Errorf("status = %v, want broken when the path is a directory", e.Status)
	}
}

func TestGroupsAreInFirstSeenOrder(t *testing.T) {
	cfg := project(t, `
[project]
name = "jjc2"

[[images]]
name = "api"
dockerfile = "a/Dockerfile"
group = "api"

[[images]]
name = "user-web"
dockerfile = "b/Dockerfile"
group = "web"

[[images]]
name = "process-runner"
dockerfile = "c/Dockerfile"
group = "api"
`, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	groups := Load(cfg, "").Groups()
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].Name != "api" || groups[1].Name != "web" {
		t.Fatalf("groups = %q/%q, want api then web (first-seen)", groups[0].Name, groups[1].Name)
	}
	// Both api members land together, even though one was declared last.
	if len(groups[0].Entries) != 2 {
		t.Errorf("group api has %d entries, want 2", len(groups[0].Entries))
	}
	if groups[0].Entries[1].Image.Name != "process-runner" {
		t.Errorf("second entry in api = %q, want process-runner", groups[0].Entries[1].Image.Name)
	}
}

// The unnamed group is a leftover rather than a group, so it sorts last however
// early it first appeared.
func TestGroupsPutUngroupedLast(t *testing.T) {
	cfg := project(t, `
[project]
name = "jjc2"

[[images]]
name = "docs"
dockerfile = "a/Dockerfile"

[[images]]
name = "api"
dockerfile = "b/Dockerfile"
group = "api"
`, "a/Dockerfile", "b/Dockerfile")

	groups := Load(cfg, "").Groups()
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].Name != "api" {
		t.Errorf("first group = %q, want api", groups[0].Name)
	}
	if groups[1].Name != "" {
		t.Errorf("last group = %q, want the ungrouped bucket", groups[1].Name)
	}
}

func TestFindMissing(t *testing.T) {
	cfg := project(t, twoServices, "services/api/Dockerfile", "services/web/Dockerfile")
	if _, ok := Load(cfg, "").Find("nope"); ok {
		t.Error("Find returned an entry for a service that isn't declared")
	}
}

// A label is used verbatim as the tag; without one the build is tagged by
// commit under the reserved sha- prefix.
func TestLabelOverridesTheCommitTag(t *testing.T) {
	cfg := project(t, twoServices, "services/api/Dockerfile", "services/web/Dockerfile")

	cat := Load(cfg, "jjc2.2026_010.001")
	if cat.Entries[0].Tag != "jjc2.2026_010.001" {
		t.Errorf("Tag = %q, want the label verbatim", cat.Entries[0].Tag)
	}
}

func TestGitTag(t *testing.T) {
	cases := []struct {
		name  string
		git   Git
		label string
		want  string
	}{
		{"commit", Git{SHA: "abc1234", Available: true}, "", "sha-abc1234"},
		{"label wins", Git{SHA: "abc1234", Available: true}, "rel.001", "rel.001"},
		{"no git, no label", Git{}, "", ""},
		{"no git, label still tags", Git{}, "rel.001", "rel.001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.git.Tag(tc.label); got != tc.want {
				t.Errorf("Tag(%q) = %q, want %q", tc.label, got, tc.want)
			}
		})
	}
}

// A project outside git still lists — it just cannot be told which commit a
// build would carry.
func TestLoadWithoutGit(t *testing.T) {
	cfg := project(t, twoServices, "services/api/Dockerfile", "services/web/Dockerfile")
	cat := Load(cfg, "")

	if cat.Git.Available {
		t.Skip("temp dir is inside a git repo; nothing to check here")
	}
	if cat.Entries[0].Tag != "" {
		t.Errorf("Tag = %q, want empty without git", cat.Entries[0].Tag)
	}
	if cat.Entries[0].Status != Buildable {
		t.Error("a service should still be buildable outside git")
	}
}

func TestStatusString(t *testing.T) {
	for _, tc := range []struct {
		s    Status
		want string
	}{{Buildable, "buildable"}, {Broken, "broken"}, {Disabled, "disabled"}} {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("Status(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}
