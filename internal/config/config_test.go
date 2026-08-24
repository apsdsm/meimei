package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a config file in a fresh directory and returns its path.
func write(t *testing.T, body string) string {
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
name = "jjc2"

[[images]]
name = "api"
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
	img := cfg.Images[0]
	if img.Platform != DefaultPlatform {
		t.Errorf("image Platform = %q, want it inherited from the project", img.Platform)
	}
	if img.Context != "." {
		t.Errorf("image Context = %q, want the build root", img.Context)
	}
	if got := cfg.Repository(img); got != "jjc2-api" {
		t.Errorf("Repository = %q, want jjc2-api", got)
	}
}

func TestServicePlatformOverridesProject(t *testing.T) {
	cfg, err := LoadFrom(write(t, `
[project]
name = "jjc2"
platform = "linux/arm64"

[[images]]
name = "api"
dockerfile = "a/Dockerfile"

[[images]]
name = "legacy"
dockerfile = "b/Dockerfile"
platform = "linux/amd64"
`))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Images[0].Platform != "linux/arm64" {
		t.Errorf("api platform = %q, want the project default", cfg.Images[0].Platform)
	}
	if cfg.Images[1].Platform != "linux/amd64" {
		t.Errorf("legacy platform = %q, want its own override", cfg.Images[1].Platform)
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
			body: "[[images]]\nname = \"api\"\ndockerfile = \"a/Dockerfile\"\n",
			want: "project.name is required",
		},
		{
			name: "no images",
			body: "[project]\nname = \"jjc2\"\n",
			want: "no images defined",
		},
		{
			name: "image without a name",
			body: "[project]\nname = \"jjc2\"\n\n[[images]]\ndockerfile = \"a/Dockerfile\"\n",
			want: "images[0] has no name",
		},
		{
			name: "duplicate image",
			body: "[project]\nname = \"jjc2\"\n\n[[images]]\nname = \"api\"\ndockerfile = \"a\"\n\n[[images]]\nname = \"api\"\ndockerfile = \"b\"\n",
			want: `duplicate image "api"`,
		},
		{
			name: "clashing shorts",
			body: "[project]\nname = \"jjc2\"\n\n[[images]]\nname = \"api\"\nshort = \"aa\"\ndockerfile = \"a\"\n\n[[images]]\nname = \"web\"\nshort = \"aa\"\ndockerfile = \"b\"\n",
			want: `share the short name "aa"`,
		},
		{
			name: "no dockerfile",
			body: "[project]\nname = \"jjc2\"\n\n[[images]]\nname = \"api\"\n",
			want: `image "api" has no dockerfile`,
		},
		{
			name: "absolute dockerfile",
			body: "[project]\nname = \"jjc2\"\n\n[[images]]\nname = \"api\"\ndockerfile = \"/etc/Dockerfile\"\n",
			want: "must be relative to the build root",
		},
		{
			name: "dockerfile escaping the root",
			body: "[project]\nname = \"jjc2\"\n\n[[images]]\nname = \"api\"\ndockerfile = \"../other/Dockerfile\"\n",
			want: "escapes the build root",
		},
		{
			name: "context escaping the root",
			body: "[project]\nname = \"jjc2\"\n\n[[images]]\nname = \"api\"\ndockerfile = \"a/Dockerfile\"\ncontext = \"..\"\n",
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
name = "jjc2"

[[images]]
name = "api"
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
	img := cfg.Images[0]
	if want := filepath.Join(cfg.Root, "services/api/Dockerfile"); cfg.AbsDockerfile(img) != want {
		t.Errorf("AbsDockerfile = %q, want %q", cfg.AbsDockerfile(img), want)
	}
	if cfg.AbsContext(img) != cfg.Root {
		t.Errorf("AbsContext = %q, want the build root %q", cfg.AbsContext(img), cfg.Root)
	}
}
