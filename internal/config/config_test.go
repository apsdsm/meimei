package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a config file in a fresh directory and returns its path.
// write puts the format version on the front, so the fixtures below stay about
// what they are testing. The version itself is tested through writeRaw.
func write(t *testing.T, body string) string {
	t.Helper()
	return writeRaw(t, "version = 2\n"+body)
}

// writeRaw writes a body verbatim, version key and all.
func writeRaw(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

const minimal = `
[project]
name = "acme"

[[builds]]
name = "api"
repository = "acme-api"
container = "api"
dockerfile = "services/api/Dockerfile"
`

func TestLoadFromDefaults(t *testing.T) {
	path := write(t, minimal)

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if cfg.Root != filepath.Dir(path) {
		t.Errorf("Root = %q, want the config's directory %q", cfg.Root, filepath.Dir(path))
	}
	if cfg.Project.Platform != DefaultPlatform {
		t.Errorf("Project.Platform = %q, want the default %q", cfg.Project.Platform, DefaultPlatform)
	}
	b := cfg.Builds[0]
	if b.Platform != DefaultPlatform {
		t.Errorf("build Platform = %q, want it inherited from the project", b.Platform)
	}
	if b.Context != "." {
		t.Errorf("build Context = %q, want the build root", b.Context)
	}
	if b.Repository != "acme-api" {
		t.Errorf("Repository = %q, want the declared acme-api", b.Repository)
	}
	if b.Container != "api" {
		t.Errorf("Container = %q, want the declared api", b.Container)
	}
}

// The repository is whatever the file says, with no relationship to the project
// or build names. This is the whole point of declaring it: a project called
// anything can push to a repository called anything.
func TestRepositoryIsNotComposed(t *testing.T) {
	cfg, err := LoadFrom(write(t, `
[project]
name = "acme"

[[builds]]
name = "app"
repository = "totally-unrelated"
container = "web"
dockerfile = "a/Dockerfile"
`))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := cfg.Builds[0].Repository; got != "totally-unrelated" {
		t.Errorf("Repository = %q, want it taken verbatim from the file", got)
	}
	if got := cfg.Builds[0].Container; got != "web" {
		t.Errorf("Container = %q, want it taken verbatim from the file", got)
	}
}

// Two builds may share a repository: that is one image reaching two
// environments under different container names.
func TestTwoBuildsMayShareARepository(t *testing.T) {
	if _, err := LoadFrom(write(t, `
[project]
name = "tc"

[[builds]]
name = "chatbot"
repository = "nova-chatbot"
container = "chatbot"
dockerfile = "a/Dockerfile"

[[builds]]
name = "chatbot-stg"
repository = "nova-chatbot"
container = "chatbot-stg"
dockerfile = "a/Dockerfile"
`)); err != nil {
		t.Errorf("LoadFrom: %v", err)
	}
}

func TestBuildPlatformOverridesProject(t *testing.T) {
	cfg, err := LoadFrom(write(t, `
[project]
name = "acme"
platform = "linux/arm64"

[[builds]]
name = "api"
repository = "acme-api"
container = "api"
dockerfile = "a/Dockerfile"

[[builds]]
name = "legacy"
repository = "acme-legacy"
container = "legacy"
dockerfile = "b/Dockerfile"
platform = "linux/amd64"
`))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Builds[0].Platform != "linux/arm64" {
		t.Errorf("api platform = %q, want the project default", cfg.Builds[0].Platform)
	}
	if cfg.Builds[1].Platform != "linux/amd64" {
		t.Errorf("legacy platform = %q, want its own override", cfg.Builds[1].Platform)
	}
}

// A missing Dockerfile must load cleanly. It is a fact about the working tree,
// not about the file, and the catalog reports it per image — failing the load
// would hide every other image behind one bad path.
func TestLoadDoesNotCheckDockerfileExists(t *testing.T) {
	if _, err := LoadFrom(write(t, minimal)); err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "no project name",
			body: "[[builds]]\nname = \"api\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"a/Dockerfile\"\n",
			want: "project.name is required",
		},
		{
			name: "no builds",
			body: "[project]\nname = \"acme\"\n",
			want: "no builds defined",
		},
		{
			name: "build without a name",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"a/Dockerfile\"\n",
			want: "builds[0] has no name",
		},
		{
			name: "build without a repository",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nname = \"api\"\ncontainer = \"c\"\ndockerfile = \"a/Dockerfile\"\n",
			want: `build "api" has no repository`,
		},
		{
			name: "build without a container",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nname = \"api\"\nrepository = \"r\"\ndockerfile = \"a/Dockerfile\"\n",
			want: `build "api" has no container`,
		},
		{
			name: "duplicate build",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nname = \"api\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"a\"\n\n[[builds]]\nname = \"api\"\nrepository = \"r2\"\ncontainer = \"c2\"\ndockerfile = \"b\"\n",
			want: `duplicate build "api"`,
		},
		{
			name: "clashing shorts",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nname = \"api\"\nshort = \"aa\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"a\"\n\n[[builds]]\nname = \"web\"\nshort = \"aa\"\nrepository = \"r2\"\ncontainer = \"c2\"\ndockerfile = \"b\"\n",
			want: `share the short name "aa"`,
		},
		{
			name: "no dockerfile",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nname = \"api\"\nrepository = \"r\"\ncontainer = \"c\"\n",
			want: `build "api" has no dockerfile`,
		},
		{
			name: "absolute dockerfile",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nname = \"api\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"/etc/Dockerfile\"\n",
			want: "must be relative to the build root",
		},
		{
			name: "dockerfile escaping the root",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nname = \"api\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"../other/Dockerfile\"\n",
			want: "escapes the build root",
		},
		{
			name: "context escaping the root",
			body: "[project]\nname = \"acme\"\n\n[[builds]]\nname = \"api\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"a/Dockerfile\"\ncontext = \"..\"\n",
			want: "escapes the build root",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFrom(write(t, tc.body))
			if err == nil {
				t.Fatalf("want an error containing %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// A path that climbs out and comes back is inside the root, and rejecting it
// would be a surprise — filepath.Clean settles it before the check.
func TestValidateAllowsPathsThatReturn(t *testing.T) {
	if _, err := LoadFrom(write(t, `
[project]
name = "acme"

[[builds]]
name = "api"
repository = "acme-api"
container = "api"
dockerfile = "services/../services/api/Dockerfile"
`)); err != nil {
		t.Errorf("LoadFrom: %v", err)
	}
}

func TestFindFromWalksUp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, FileName)
	if err := os.WriteFile(path, []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "services", "api", "internal")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findFrom(deep)
	if err != nil {
		t.Fatalf("findFrom: %v", err)
	}
	if got != path {
		t.Errorf("findFrom = %q, want %q", got, path)
	}
}

func TestFindFromReportsWhereItLooked(t *testing.T) {
	dir := t.TempDir()
	_, err := findFrom(dir)
	if err == nil {
		t.Fatal("want an error, got none")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error = %q, want it to name the directory it started from", err)
	}
}

// A directory called .meimei.toml is not a config, and treating it as one gives
// a confusing read error instead of continuing the walk.
func TestFindFromSkipsDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(filepath.Join(child, FileName), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findFrom(child)
	if err != nil {
		t.Fatalf("findFrom: %v", err)
	}
	if got != filepath.Join(root, FileName) {
		t.Errorf("findFrom = %q, want the real config above it", got)
	}
}

func TestAbsPaths(t *testing.T) {
	cfg, err := LoadFrom(write(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Builds[0]
	if want := filepath.Join(cfg.Root, "services/api/Dockerfile"); cfg.AbsDockerfile(b) != want {
		t.Errorf("AbsDockerfile = %q, want %q", cfg.AbsDockerfile(b), want)
	}
	if cfg.AbsContext(b) != cfg.Root {
		t.Errorf("AbsContext = %q, want the build root %q", cfg.AbsContext(b), cfg.Root)
	}
}

// The version gate is checked before Validate, and both directions of a
// mismatch have to say what to do: an older file needs editing, a newer one
// needs a newer meimei. A v1 file is the case that matters most, because
// [[services]] decodes into nothing and the honest-looking failure would be
// "no builds defined" — true, and no help at all.

func TestVersionTwoLoads(t *testing.T) {
	cfg, err := LoadFrom(writeRaw(t, "version = 2\n"+minimal))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Version != ConfigVersion {
		t.Errorf("Version = %d, want %d", cfg.Version, ConfigVersion)
	}
}

func TestVersionOneIsRefusedWithTheEdits(t *testing.T) {
	// A real v1 file: no version key, and the old table name.
	body := "[project]\nname = \"acme\"\n\n[[services]]\nname = \"api\"\ndockerfile = \"a/Dockerfile\"\n"

	_, err := LoadFrom(writeRaw(t, body))
	if err == nil {
		t.Fatal("LoadFrom succeeded, want a refusal")
	}
	for _, want := range []string{"version 1", "version = 2", "[[builds]]", "repository", "container"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
	// The failure a version check exists to prevent.
	if strings.Contains(err.Error(), "no builds defined") {
		t.Errorf("refused as an empty file rather than a v1 file:\n%v", err)
	}
}

func TestVersionFromTheFutureSaysUpgrade(t *testing.T) {
	_, err := LoadFrom(writeRaw(t, "version = 99\n"+minimal))
	if err == nil {
		t.Fatal("LoadFrom succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "version 99") || !strings.Contains(err.Error(), "go install") {
		t.Errorf("want the version and how to upgrade, got:\n%v", err)
	}
}
