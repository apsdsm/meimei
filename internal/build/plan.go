// Package build turns a catalog into builds.
//
// It is split in two on purpose. Resolving WHAT to build — which builds, what
// image name, which tag, which platform, which labels — is a pure function over
// the catalog and the flags, with no I/O and no side effects. Running the build
// is separate, and is the only part that touches docker.
//
// The split is what makes --no-push --dry-run free, and it puts the rules that
// have actually caused trouble (the reserved sha- prefix, tag validity, which
// builds a name selects) under test instead of inside a shell script.
package build

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/apsdsm/meimei/internal/catalog"
)

// Output is where a finished image goes.
type Output int

const (
	// OutputLoad puts the image in the local docker image store instead of
	// sending it anywhere. This is --no-push, a diagnostic: it answers "does
	// this build" and nothing else. Single-platform only — nothing can load two
	// architectures.
	OutputLoad Output = iota
	// OutputPush uploads it to the registry. The default: a build that is kept
	// on the machine that made it is not finished.
	OutputPush
)

// Plan is one resolved build: everything needed to run it, and nothing that
// needed asking docker or AWS to work out.
type Plan struct {
	Name string

	// Image is the full reference the image is tagged with. For a local build
	// that is repository:tag with no registry host, which is a valid local
	// name.
	Image      string
	Repository string
	Tag        string

	Platform   string
	Dockerfile string // absolute
	Context    string // absolute

	// Labels are OCI annotations as "key=value", in a stable order so that two
	// resolutions of the same inputs produce the same command.
	Labels []string

	// BuildArgs are docker build arguments as "key=value", stable-ordered for
	// the same reason Labels are.
	BuildArgs []string

	Output Output

	// Route is who uploads the image when Output is OutputPush. Chosen by the
	// caller, because deciding it means asking docker what it is running on.
	Route Route
}

// Options are the choices a caller makes; everything else comes from the
// catalog.
type Options struct {
	// Builds names what to build. Empty with All set means everything.
	Builds []string

	// All selects every buildable build.
	//
	// A flag rather than a reserved name: "all" as a magic positional value is
	// one naming collision away from ambiguity, and it reads the same as a
	// build name at the call site while meaning something entirely different.
	All bool

	// Tag names the image tag verbatim instead of tagging by commit.
	Tag string

	// Platform is the target platform. Required — the caller resolves it,
	// because asking docker what the host is counts as I/O and this package
	// stays pure.
	Platform string

	// Now stamps org.opencontainers.image.created.
	Now time.Time

	Output Output

	// RegistryHost prefixes the image reference for a push. Empty names the
	// image locally, which is what a --load build wants.
	RegistryHost string

	// Route is who uploads a pushed image; see ChooseRoute.
	Route Route
}

// tagPattern is docker's tag grammar, narrowed: a leading alphanumeric then
// alphanumerics, dots, underscores and hyphens, up to 128 characters.
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// commitTagPrefix is reserved for tags generated from a commit.
//
// The registry's lifecycle policy expires all but the most recent images whose
// tags carry this prefix. A release tag that wandered into the same namespace
// would be swept by that rule some builds later — losing the image a deployed
// task definition still refers to. So the prefix is refused for tags the caller
// names, rather than merely discouraged.
const commitTagPrefix = "sha-"

// ValidateTag checks a tag the caller named. An empty tag is fine: it means
// "tag by commit", and that tag is generated rather than typed.
func ValidateTag(tag string) error {
	if tag == "" {
		return nil
	}
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf("invalid tag %q: letters, digits, dots, underscores and hyphens only, "+
			"starting with a letter or digit, up to 128 characters", tag)
	}
	if strings.HasPrefix(tag, commitTagPrefix) {
		return fmt.Errorf("invalid tag %q: the %q prefix is reserved for tags meimei generates from "+
			"a commit, and the registry's lifecycle rule expires all but the most recent of them",
			tag, commitTagPrefix)
	}
	return nil
}

// Resolve turns a catalog and a set of options into one plan per image.
//
// It reports every problem it can rather than the first, because the usual
// cause of a bad invocation is a typo in a build name and a caller who
// mistyped one may well have mistyped two.
func Resolve(cat *catalog.Catalog, opts Options) ([]Plan, error) {
	if err := ValidateTag(opts.Tag); err != nil {
		return nil, err
	}
	if opts.Platform == "" {
		return nil, fmt.Errorf("no platform resolved")
	}

	entries, err := selectEntries(cat, opts.Builds, opts.All)
	if err != nil {
		return nil, err
	}

	tag := cat.Git.Tag(opts.Tag)
	if tag == "" {
		return nil, fmt.Errorf("cannot tag this build: no --tag given and no git commit to name " +
			"(pass --tag, or build from a git checkout)")
	}

	labels := ociLabels(cat, opts, tag)
	args := buildArgs(tag)

	plans := make([]Plan, 0, len(entries))
	for _, e := range entries {
		plans = append(plans, Plan{
			Name:       e.Build.Name,
			Image:      imageRef(opts.RegistryHost, e.Repository, tag),
			Repository: e.Repository,
			Tag:        tag,
			Platform:   platformFor(e, opts),
			Dockerfile: e.Dockerfile,
			Context:    e.Context,
			Labels:     labels,
			BuildArgs:  args,
			Output:     opts.Output,
			Route:      opts.Route,
		})
	}
	return plans, nil
}

// platformFor lets a build override the project's platform.
//
// It applies to --no-push too. A diagnostic build is only worth running if it
// builds the thing a push would have sent, so it targets the platform the
// config declares rather than the host's. The consequence is that on an x86
// host an arm64 image loaded locally will not run without emulation, which is
// accepted: --no-push answers "does this build", not "give me something to
// run".
func platformFor(e catalog.Entry, opts Options) string {
	if e.Build.Platform != "" {
		return e.Build.Platform
	}
	return opts.Platform
}

// BuildIDArg is the build argument every image is given, carrying the tag the
// image is being built as.
//
// The same fact as org.opencontainers.image.version, handed TO the build rather
// than attached to the result. A label describes an image to whoever inspects
// it from outside and cannot be read by the code inside it, so an app that
// wants to report which build it is has to be told while it is being built — a
// value the container could set at start-up would be a fact about the
// deployment instead, and would no longer identify the artifact.
//
// The case it was added for: a browser SPA bakes this into its bundle and sends
// it back, so a server can tell a client running a retired build from one
// running the deployed build.
const BuildIDArg = "MEIMEI_BUILD_ID"

// buildArgs are the arguments handed to every build.
//
// Declared for every image rather than opted into per image, like the
// labels: a Dockerfile that does not name the ARG ignores it, and buildx
// warning about an unused build argument is not a failure — success is the exit
// code, never the progress output.
func buildArgs(tag string) []string {
	return []string{BuildIDArg + "=" + tag}
}

// ociLabels records where an image came from. They are attached to every build
// so that an image is traceable to its commit whatever tag it ends up carrying
// — a tag can be a release name that says nothing about the source.
//
// "Label" here is the OCI sense, an annotation on the image. It has nothing to
// do with the --label flag that used to name a tag; that flag is now --tag.
func ociLabels(cat *catalog.Catalog, opts Options, tag string) []string {
	var labels []string
	add := func(k, v string) {
		if v != "" {
			labels = append(labels, k+"="+v)
		}
	}

	add("org.opencontainers.image.revision", cat.Git.FullSHA)
	add("org.opencontainers.image.created", opts.Now.UTC().Format(time.RFC3339))
	add("org.opencontainers.image.source", cat.Git.RemoteURL)
	// Only a tag the caller named goes in `version`; a commit tag is already in
	// `revision`, and repeating it says nothing new.
	if opts.Tag != "" {
		add("org.opencontainers.image.version", opts.Tag)
	}
	return labels
}

// selectEntries turns the request into catalog entries.
func selectEntries(cat *catalog.Catalog, names []string, all bool) ([]catalog.Entry, error) {
	if all && len(names) > 0 {
		return nil, fmt.Errorf("--all cannot be combined with a build name")
	}
	if !all && len(names) == 0 {
		return nil, fmt.Errorf("no build named (name one or more builds, or pass --all)")
	}

	if all {
		var out []catalog.Entry
		for _, e := range cat.Entries {
			// --all means every build that CAN run. A disabled one is excluded
			// by declaration, and a broken one is reported below.
			if e.Status == catalog.Buildable {
				out = append(out, e)
			}
		}
		if broken := brokenNames(cat); len(broken) > 0 {
			return nil, fmt.Errorf("cannot build --all: %s cannot be built — run `meimei ls` for details",
				strings.Join(broken, ", "))
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("nothing to build: no build is buildable")
		}
		return out, nil
	}

	var out []catalog.Entry
	var unknown, broken, disabled []string
	seen := map[string]bool{}

	for _, name := range names {
		if seen[name] {
			continue // naming a build twice is harmless, so build it once
		}
		seen[name] = true

		e, ok := cat.Find(name)
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		switch e.Status {
		case catalog.Broken:
			broken = append(broken, name)
		case catalog.Disabled:
			// Named explicitly rather than swept up by `all`, so say no rather
			// than silently doing nothing.
			disabled = append(disabled, name)
		default:
			out = append(out, e)
		}
	}

	var problems []string
	if len(unknown) > 0 {
		problems = append(problems, fmt.Sprintf("unknown: %s (known: %s)",
			strings.Join(unknown, ", "), strings.Join(knownNames(cat), ", ")))
	}
	if len(broken) > 0 {
		problems = append(problems, fmt.Sprintf("cannot be built: %s — run `meimei ls` for details",
			strings.Join(broken, ", ")))
	}
	if len(disabled) > 0 {
		problems = append(problems, fmt.Sprintf("disabled in %s: %s",
			filepath.Base(cat.Config.Path), strings.Join(disabled, ", ")))
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return out, nil
}

func knownNames(cat *catalog.Catalog) []string {
	names := make([]string, 0, len(cat.Entries))
	for _, e := range cat.Entries {
		names = append(names, e.Build.Name)
	}
	sort.Strings(names)
	return names
}

func brokenNames(cat *catalog.Catalog) []string {
	var names []string
	for _, e := range cat.Entries {
		if e.Status == catalog.Broken {
			names = append(names, e.Build.Name)
		}
	}
	return names
}

// imageRef is the full reference an image is tagged with. Without a registry
// host it is a plain local name, which is what a --load build wants; with one
// it is the remote reference docker pushes to.
func imageRef(host, repo, tag string) string {
	if host == "" {
		return repo + ":" + tag
	}
	return host + "/" + repo + ":" + tag
}
