package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apsdsm/meimei/internal/catalog"
	"github.com/apsdsm/meimei/internal/config"
)

var stamp = time.Date(2026, 8, 14, 5, 42, 0, 0, time.UTC)

// fixture builds a throwaway project and returns its catalog. Git is filled in
// by hand rather than read from a real repo: the tests are about resolution,
// and a temp dir's git state is not ours to control.
func fixture(t *testing.T, toml string, dockerfiles ...string) *catalog.Catalog {
	t.Helper()
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range dockerfiles {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("FROM golang:1.25 AS b\nFROM alpine:3.21\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := config.LoadFrom(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.Load(cfg, "")
	cat.Git = catalog.Git{
		SHA:       "abc1234",
		FullSHA:   "abc1234000000000000000000000000000000000",
		RemoteURL: "git@github.com:example/acme.git",
		Available: true,
	}
	// Load stamped the entries with whatever the temp dir's git said; restate
	// them against the git above.
	for i := range cat.Entries {
		cat.Entries[i].Tag = cat.Git.Tag("")
	}
	return cat
}

const threeServices = `
[project]
name = "acme"

[[images]]
name = "api"
dockerfile = "a/Dockerfile"

[[images]]
name = "user-web"
dockerfile = "b/Dockerfile"

[[images]]
name = "retired"
dockerfile = "c/Dockerfile"
disabled = true
`

func opts(services ...string) Options {
	return Options{
		Images:   services,
		Platform: "linux/arm64",
		Now:      stamp,
		Output:   OutputLoad,
	}
}

// allOpts is the --all form: no names, the flag set.
func allOpts() Options {
	o := opts()
	o.All = true
	return o
}

func TestResolveSingleService(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	plans, err := Resolve(cat, opts("api"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(plans))
	}

	p := plans[0]
	if p.Image != "acme-api:sha-abc1234" {
		t.Errorf("Image = %q, want acme-api:sha-abc1234", p.Image)
	}
	if p.Tag != "sha-abc1234" {
		t.Errorf("Tag = %q", p.Tag)
	}
	if filepath.Base(filepath.Dir(p.Dockerfile)) != "a" {
		t.Errorf("Dockerfile = %q, want the one under a/", p.Dockerfile)
	}
	if p.Context != cat.Config.Root {
		t.Errorf("Context = %q, want the build root", p.Context)
	}
}

// --all builds every buildable service and silently leaves out the ones
// declared disabled.
func TestResolveAllSkipsDisabled(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	plans, err := Resolve(cat, allOpts())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("got %d plans, want 2 (retired is disabled)", len(plans))
	}
	for _, p := range plans {
		if p.Name == "retired" {
			t.Error("a disabled service was included in --all")
		}
	}
}

// Naming a disabled service explicitly is a mistake worth reporting — the
// caller asked for something specific and would otherwise get silence.
func TestResolveNamedDisabledIsAnError(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	_, err := Resolve(cat, opts("retired"))
	if err == nil {
		t.Fatal("want an error naming the disabled service")
	}
	if !strings.Contains(err.Error(), "disabled") || !strings.Contains(err.Error(), "retired") {
		t.Errorf("error = %q, want it to say retired is disabled", err)
	}
}

func TestResolveUnknownServiceListsKnown(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	_, err := Resolve(cat, opts("apo"))
	if err == nil {
		t.Fatal("want an error for an unknown service")
	}
	// A mistyped name is the usual cause, so the message has to show the real
	// ones to be any use.
	if !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "user-web") {
		t.Errorf("error = %q, want it to list the known services", err)
	}
}

func TestResolveBrokenServiceIsRefused(t *testing.T) {
	// b/Dockerfile is deliberately absent.
	cat := fixture(t, threeServices, "a/Dockerfile", "c/Dockerfile")

	if _, err := Resolve(cat, opts("user-web")); err == nil {
		t.Fatal("want an error for a service whose Dockerfile is missing")
	}

	// And --all refuses too rather than quietly building a subset: a caller who
	// asked for everything and got four of five images would not know.
	_, err := Resolve(cat, allOpts())
	if err == nil {
		t.Fatal("want --all to refuse while any service is broken")
	}
	if !strings.Contains(err.Error(), "user-web") {
		t.Errorf("error = %q, want it to name the broken service", err)
	}
}

func TestResolveNoServiceNamed(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")
	_, err := Resolve(cat, opts())
	if err == nil {
		t.Fatal("want an error when nothing is named")
	}
	// The way out has to be in the message; "no service named" alone leaves the
	// caller guessing at the spelling of the flag.
	if !strings.Contains(err.Error(), "--all") {
		t.Errorf("error = %q, want it to point at --all", err)
	}
}

// Several services in one invocation — something the script could not do at
// all, since its only choices were one name or the literal "all".
func TestResolveSeveralServices(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	plans, err := Resolve(cat, opts("api", "user-web"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("got %d plans, want 2", len(plans))
	}
	if plans[0].Name != "api" || plans[1].Name != "user-web" {
		t.Errorf("plans = %s/%s, want them in the order named", plans[0].Name, plans[1].Name)
	}

	// Naming one twice is a slip, not a request for two builds.
	plans, err = Resolve(cat, opts("api", "api"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plans) != 1 {
		t.Errorf("got %d plans for a repeated name, want 1", len(plans))
	}
}

// --all with a name is contradictory, and guessing which the caller meant
// would build either too much or too little.
func TestResolveAllWithNamesIsRefused(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	o := opts("api")
	o.All = true
	if _, err := Resolve(cat, o); err == nil {
		t.Fatal("want an error when --all is combined with a service name")
	}
}

func TestResolveLabelTagsVerbatim(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	o := opts("api")
	o.Label = "acme.2026_010.001"
	plans, err := Resolve(cat, o)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plans[0].Image != "acme-api:acme.2026_010.001" {
		t.Errorf("Image = %q, want the label used verbatim", plans[0].Image)
	}
}

func TestResolveWithoutGitOrLabelRefuses(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")
	cat.Git = catalog.Git{} // no git

	_, err := Resolve(cat, opts("api"))
	if err == nil {
		t.Fatal("want an error: there is nothing to tag the image with")
	}

	// A label is enough on its own, though.
	o := opts("api")
	o.Label = "manual.001"
	if _, err := Resolve(cat, o); err != nil {
		t.Errorf("a label should be sufficient without git: %v", err)
	}
}

func TestOCILabels(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	plans, err := Resolve(cat, opts("api"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(plans[0].Labels, " ")

	// The FULL sha, not the abbreviation: the label outlives the repo state
	// that made a short hash unambiguous.
	if !strings.Contains(got, "org.opencontainers.image.revision=abc1234000000000000000000000000000000000") {
		t.Errorf("labels = %v, want the full revision", plans[0].Labels)
	}
	if !strings.Contains(got, "org.opencontainers.image.created=2026-08-14T05:42:00Z") {
		t.Errorf("labels = %v, want a created stamp", plans[0].Labels)
	}
	if !strings.Contains(got, "org.opencontainers.image.source=git@github.com:example/acme.git") {
		t.Errorf("labels = %v, want the source url", plans[0].Labels)
	}
	// No label was given, so version says nothing the revision does not.
	if strings.Contains(got, "image.version") {
		t.Errorf("labels = %v, want no version label for a commit build", plans[0].Labels)
	}

	o := opts("api")
	o.Label = "rel.001"
	plans, _ = Resolve(cat, o)
	if !strings.Contains(strings.Join(plans[0].Labels, " "), "org.opencontainers.image.version=rel.001") {
		t.Errorf("labels = %v, want the version label for a labelled build", plans[0].Labels)
	}
}

// Every build is told the tag it is being built as, so an app can report which
// build it is. A label cannot do this job: the code inside an image cannot read
// its own annotations.
func TestResolveHandsTheTagToTheBuild(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	plans, err := Resolve(cat, opts("api"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(plans[0].BuildArgs, " "), "MEIMEI_BUILD_ID=sha-abc1234"; got != want {
		t.Errorf("BuildArgs = %q, want %q", got, want)
	}

	// It follows the tag, so a labelled build reports the label rather than the
	// commit — the same string the image is tagged with, whichever it is.
	o := opts("api")
	o.Label = "rel.001"
	plans, err = Resolve(cat, o)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(plans[0].BuildArgs, " "), "MEIMEI_BUILD_ID=rel.001"; got != want {
		t.Errorf("BuildArgs = %q, want %q", got, want)
	}
	if plans[0].BuildArgs[0] != BuildIDArg+"="+plans[0].Tag {
		t.Errorf("BuildArgs = %v, want it to carry Tag %q", plans[0].BuildArgs, plans[0].Tag)
	}
}

// A local build targets the machine doing the building, whatever the config
// says — an image for another architecture cannot be run here.
func TestLocalBuildIgnoresServicePlatform(t *testing.T) {
	cat := fixture(t, `
[project]
name = "acme"
platform = "linux/arm64"

[[images]]
name = "api"
dockerfile = "a/Dockerfile"
`, "a/Dockerfile")

	o := opts("api")
	o.Platform = "linux/amd64" // what the host is
	plans, err := Resolve(cat, o)
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].Platform != "linux/amd64" {
		t.Errorf("Platform = %q, want the host's for a --load build", plans[0].Platform)
	}
}

func TestValidateLabel(t *testing.T) {
	valid := []string{"", "acme.2026_010.001", "ENJJC-12.001", "v1", "a", strings.Repeat("x", 128)}
	for _, l := range valid {
		if err := ValidateLabel(l); err != nil {
			t.Errorf("ValidateLabel(%q) = %v, want nil", l, err)
		}
	}

	invalid := []struct {
		label string
		why   string
	}{
		{"-leading-hyphen", "must start with a letter or digit"},
		{".leading-dot", "must start with a letter or digit"},
		{"has space", "no spaces"},
		{"has/slash", "no slashes"},
		{strings.Repeat("x", 129), "too long"},
		{"sha-abc1234", "the reserved commit-tag prefix"},
		{"sha-anything", "the reserved commit-tag prefix"},
	}
	for _, tc := range invalid {
		if err := ValidateLabel(tc.label); err == nil {
			t.Errorf("ValidateLabel(%q) = nil, want an error (%s)", tc.label, tc.why)
		}
	}
}

// The reserved prefix is the one rule with teeth: a release tagged sha-* would
// be swept by the registry's keep-last-N-commit-images lifecycle rule, taking
// away an image a live task definition still points at.
func TestReservedPrefixErrorExplainsItself(t *testing.T) {
	err := ValidateLabel("sha-deadbee")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "lifecycle") {
		t.Errorf("error = %q, want it to say why the prefix is reserved", err)
	}
}

// A pushed image is named for the registry; a local one is not.
func TestImageRefHonoursRegistryHost(t *testing.T) {
	cat := fixture(t, threeServices, "a/Dockerfile", "b/Dockerfile", "c/Dockerfile")

	o := opts("api")
	o.Output = OutputPush
	o.RegistryHost = "123456789012.dkr.ecr.ap-northeast-1.amazonaws.com"
	plans, err := Resolve(cat, o)
	if err != nil {
		t.Fatal(err)
	}
	want := "123456789012.dkr.ecr.ap-northeast-1.amazonaws.com/acme-api:sha-abc1234"
	if plans[0].Image != want {
		t.Errorf("Image = %q, want %q", plans[0].Image, want)
	}

	// Without a host the name stays local and runnable.
	plans, _ = Resolve(cat, opts("api"))
	if plans[0].Image != "acme-api:sha-abc1234" {
		t.Errorf("Image = %q, want a plain local name", plans[0].Image)
	}
}

// A push targets the platform the cluster runs, not the machine building it —
// the opposite of a local build.
func TestPushUsesConfiguredPlatform(t *testing.T) {
	cat := fixture(t, `
[project]
name = "acme"
platform = "linux/arm64"

[[images]]
name = "api"
dockerfile = "a/Dockerfile"
`, "a/Dockerfile")

	o := opts("api")
	o.Output = OutputPush
	o.Platform = "linux/arm64" // what the config says
	plans, err := Resolve(cat, o)
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].Platform != "linux/arm64" {
		t.Errorf("Platform = %q, want the configured target platform", plans[0].Platform)
	}
}

// The CLI route builds with --load and pushes separately; the buildkit route
// pushes from the build. Getting this backwards produces a confusing failure
// after a slow but successful build.
func TestArgsRespectPushRoute(t *testing.T) {
	p := samplePlan()
	p.Output = OutputPush

	p.Route = RouteBuildkit
	if !contains(p.Args(), "--push") || contains(p.Args(), "--load") {
		t.Errorf("buildkit route args = %v, want --push", p.Args())
	}

	p.Route = RouteCLI
	if !contains(p.Args(), "--load") || contains(p.Args(), "--push") {
		t.Errorf("cli route args = %v, want --load", p.Args())
	}
}

func TestChooseRoute(t *testing.T) {
	// Multi-platform can only go via buildkit; nothing can load two
	// architectures, so no host lookup is even needed.
	if got := ChooseRoute(t.Context(), "/nonexistent", "linux/amd64,linux/arm64"); got != RouteBuildkit {
		t.Errorf("multi-platform route = %v, want buildkit", got)
	}

	// With docker unreachable HostPlatform falls back to this machine, so a
	// matching target is native and a differing one is emulated.
	native := "linux/" + goArchToDocker(runtimeGOARCH())
	if got := ChooseRoute(t.Context(), "/nonexistent", native); got != RouteBuildkit {
		t.Errorf("native route = %v, want buildkit", got)
	}
	foreign := "linux/amd64"
	if native == foreign {
		foreign = "linux/arm64"
	}
	if got := ChooseRoute(t.Context(), "/nonexistent", foreign); got != RouteCLI {
		t.Errorf("emulated route = %v, want cli", got)
	}
}
