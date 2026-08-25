// Package config reads .meimei.toml — the per-project declaration of what this
// repository can build.
//
// The file is found by walking up from the working directory, so meimei can be
// run from anywhere inside a project. The directory holding it is the BUILD
// ROOT: every path in the file is relative to that directory, and it is the
// docker build context unless a service overrides it. That matches how the
// shell scripts this replaces work (they `cd` to the repo root first), without
// making meimei depend on git to resolve a path.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// FileName is the config file meimei looks for.
const FileName = ".meimei.toml"

// ConfigVersion is the .meimei.toml format this binary reads. A file must say
// `version = <this>` as its first line and no other value is accepted: there is
// no compatibility shim and none is planned.
//
// It is an integer rather than a sniff for a known-old key because sniffing only
// recognises renames already made. It cannot see a breaking change that ADDS a
// required key — toml ignores what it does not know, so an old file missing a new
// key looks exactly like a new file whose author left it out. Bumping this and
// writing one message is what the next breaking change costs.
const ConfigVersion = 2

// DefaultPlatform is what images are built for when nothing says otherwise.
// Every ECS target we deploy to runs on Graviton, so an amd64 image would be
// dead weight nothing schedules.
const DefaultPlatform = "linux/arm64"

// Config is a whole .meimei.toml.
type Config struct {
	// Version is the format version, checked before anything else is read. See
	// ConfigVersion.
	Version int `toml:"version"`

	Project  Project   `toml:"project"`
	Builds   []Build   `toml:"builds"`
	Registry *Registry `toml:"registry"`
	Targets  []Target  `toml:"targets"`

	// Path is the absolute path of the file that was loaded, and Root the
	// directory holding it. Populated by Load; not parsed from TOML.
	Path string `toml:"-"`
	Root string `toml:"-"`
}

// Project is the repo-wide header.
type Project struct {
	// Name is a label for this repository, used in output. It names NOTHING in
	// AWS: every AWS identifier is declared, never composed from parts, so a
	// project can be called anything without deciding what a repository is
	// called.
	Name string `toml:"name"`

	// Region is the AWS region. Unused while meimei only reads local state,
	// but it belongs to the project rather than to any one command.
	Region string `toml:"region"`

	// Platform is the default build platform for every build.
	Platform string `toml:"platform"`
}

// Registry is where images are pushed. Optional: a project that only ever
// builds locally needs none, and `meimei build` works without it.
type Registry struct {
	// Account is the AWS account id owning the repositories. Declared rather
	// than discovered, and checked against the live caller identity before any
	// registry call — under immutable tags a push into the wrong account
	// cannot be undone.
	Account string `toml:"account"`

	// Profile is the AWS shared-config profile to use. Empty falls back to
	// ambient credentials, which is what CI has.
	Profile string `toml:"profile"`

	// Region defaults to the project's.
	Region string `toml:"region"`
}

// Target is somewhere images get deployed to: a cluster, and a scope inside it.
//
// It holds only what cannot be discovered — which account and ECS cluster a
// target name refers to, which of that cluster's services are this target's,
// and how to authenticate. What is IN each service is NOT here: which build is
// packed into which task definition is a per-cluster decision, read from the
// cluster at deploy time.
type Target struct {
	// Name is what you type: `--to dev1`. A local label with no counterpart in
	// AWS.
	Name string `toml:"name"`

	// Account is the AWS account holding the cluster, asserted before any
	// call. Often, but not always, the registry's.
	Account string `toml:"account"`

	// Profile is the AWS shared-config profile for that account.
	Profile string `toml:"profile"`

	// Cluster is the ECS cluster name. Declared because naming conventions
	// differ per product (`jjc2-dev1-ecs-cluster-dev1` against `tc-public1`),
	// and a convention that holds for one repo is not one meimei can assume.
	Cluster string `toml:"cluster"`

	// Region defaults to the project's.
	Region string `toml:"region"`

	// Services are the ECS services in this target's scope, by exact name.
	//
	// This is the one fact about a deploy that cannot be discovered: ECS has no
	// notion of an environment, so nothing on the cluster says which of its
	// services are production and which are staging.
	//
	// A SCOPE, not an instruction. Naming three services here does not make a
	// deploy roll three services — it makes those three the only ones a build
	// may resolve against. Which of them roll is decided by the builds named on
	// the command line.
	//
	// Empty means every service on the cluster, which is right for a cluster
	// with one environment on it. Two targets naming disjoint services in one
	// cluster is how a staging service beside production is addressed.
	Services []string `toml:"services"`

	// Timeout is how long a rollout is followed before giving up, as a Go
	// duration ("10m"). It belongs to the target rather than to an invocation:
	// a slow production service wants a different value from dev1, and the
	// value does not change between two deploys to the same place.
	//
	// A string because TOML has no duration type. Empty means
	// deploy.DefaultTimeout, which is why this does not hold a time.Duration —
	// zero would be indistinguishable from "not set".
	Timeout string `toml:"timeout"`
}

// Build is one buildable container image, and the two AWS names it is
// deployed under.
type Build struct {
	// Name is meimei's own label for this build: what you type at the CLI and
	// what appears in output. It names NOTHING in AWS and is free to be
	// anything — Repository and Container carry the AWS-facing names.
	Name string `toml:"name"`

	// Repository is the ECR repository this build is pushed to, exactly as it
	// is named in ECR. Required.
	//
	// Declared rather than composed from project and build names. A name
	// computed from parts is a footgun: it cannot be used on a repository that
	// does not happen to match the convention, and an error about it has to
	// explain a derivation instead of naming a string. meimei never creates a
	// repository, so this is always a name that already exists.
	Repository string `toml:"repository"`

	// Container is the name of the container definition inside the ECS task
	// definition that runs this build. Required, and matched exactly.
	//
	// Not defaulted to Name for the same reason Repository is not composed: a
	// default is a derivation with a friendlier face, and it would put the
	// footgun back for exactly the projects that do not follow the convention.
	Container string `toml:"container"`

	// Short is an optional abbreviation, for a future keyboard shortcut.
	Short string `toml:"short"`

	// Dockerfile is the path to build from, relative to the build root.
	Dockerfile string `toml:"dockerfile"`

	// Context is the docker build context, relative to the build root.
	// Defaults to the build root itself, which is what every image we have
	// needs — Dockerfiles copy across directory boundaries (the api's go.work
	// replace, the web workspace's shared install).
	Context string `toml:"context"`

	// Group is how THIS REPOSITORY organises its builds — "api", "web",
	// "worker" — and is used only to arrange them on screen. It says nothing
	// about how they are deployed.
	//
	// Deliberately not the ECS task definition an image is packed into. Packing
	// is a per-cluster Terraform decision made for that cluster's box (dev1
	// packs everything onto two task definitions so an m7g.medium needs only ~2
	// task-ENIs; a production cluster sized for real traffic will split them
	// differently), so a copy here would be a global mirror of a per-target
	// fact — one that can only ever drift, and drift silently. meimei reads the
	// packing from the cluster instead: an ECS service's name is its task
	// definition family, and its container names are the images in it, so two
	// API calls give the authoritative mapping for whichever target is being
	// deployed to.
	Group string `toml:"group"`

	// Platform overrides Project.Platform for this build alone.
	Platform string `toml:"platform"`

	// Color is the accent used for this build's name on screen.
	Color string `toml:"color"`

	// Disabled keeps a build in the file but out of every run. One that is
	// being brought up, or one that has been retired but whose definition is
	// not ready to delete, is better declared than forgotten.
	Disabled bool `toml:"disabled"`
}

// Load finds .meimei.toml by walking up from the working directory and reads it.
func Load() (*Config, error) {
	path, err := Find()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a specific .meimei.toml.
func LoadFrom(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", path, err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", abs, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", abs, err)
	}

	// Before the defaults and before Validate: a v1 file decodes to no images at
	// all, so without this it fails with "no images defined" and says nothing
	// about the rename that actually happened.
	if err := checkVersion(cfg.Version); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	cfg.Path = abs
	cfg.Root = filepath.Dir(abs)

	if cfg.Project.Platform == "" {
		cfg.Project.Platform = DefaultPlatform
	}
	for i := range cfg.Builds {
		if cfg.Builds[i].Platform == "" {
			cfg.Builds[i].Platform = cfg.Project.Platform
		}
		if cfg.Builds[i].Context == "" {
			cfg.Builds[i].Context = "."
		}
	}

	if cfg.Registry != nil && cfg.Registry.Region == "" {
		cfg.Registry.Region = cfg.Project.Region
	}
	for i := range cfg.Targets {
		if cfg.Targets[i].Region == "" {
			cfg.Targets[i].Region = cfg.Project.Region
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	return &cfg, nil
}

// Validate reports the first problem that would make the file unusable.
//
// It checks only what the file can be wrong about on its own. Whether a
// Dockerfile actually exists is deliberately NOT checked here: that is a fact
// about the working tree, it changes between branches, and reporting it as a
// load failure would mean a single missing file stops you seeing the other four
// builds. The catalog reports it per build instead.
func (c *Config) Validate() error {
	if c.Project.Name == "" {
		return fmt.Errorf("project.name is required (it labels this repository in output)")
	}
	if len(c.Builds) == 0 {
		return fmt.Errorf("no builds defined")
	}

	seen := make(map[string]bool, len(c.Builds))
	shorts := make(map[string]string, len(c.Builds))
	for i, b := range c.Builds {
		if b.Name == "" {
			return fmt.Errorf("builds[%d] has no name", i)
		}
		if seen[b.Name] {
			return fmt.Errorf("duplicate build %q", b.Name)
		}
		seen[b.Name] = true

		// Required, and never derived from the build's name. Two builds MAY
		// share a repository — that is how one image reaches two environments
		// under different container names — so there is no uniqueness check
		// here.
		if b.Repository == "" {
			return fmt.Errorf("build %q has no repository (the ECR repository name, "+
				"declared exactly — meimei does not compose one from project and build names)", b.Name)
		}
		if b.Container == "" {
			return fmt.Errorf("build %q has no container (the name of its container definition "+
				"in the ECS task definition, matched exactly)", b.Name)
		}

		if b.Short != "" {
			if prev, clash := shorts[b.Short]; clash {
				return fmt.Errorf("builds %q and %q share the short name %q", prev, b.Name, b.Short)
			}
			shorts[b.Short] = b.Name
		}

		if b.Dockerfile == "" {
			return fmt.Errorf("build %q has no dockerfile", b.Name)
		}
		if filepath.IsAbs(b.Dockerfile) {
			return fmt.Errorf("build %q: dockerfile must be relative to the build root, got %q", b.Name, b.Dockerfile)
		}
		if escapes(b.Dockerfile) {
			return fmt.Errorf("build %q: dockerfile %q escapes the build root", b.Name, b.Dockerfile)
		}
		if filepath.IsAbs(b.Context) {
			return fmt.Errorf("build %q: context must be relative to the build root, got %q", b.Name, b.Context)
		}
		if escapes(b.Context) {
			return fmt.Errorf("build %q: context %q escapes the build root", b.Name, b.Context)
		}
	}
	return c.validateRegistryAndTargets()
}

// Find walks up from the working directory looking for .meimei.toml.
func Find() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}
	return findFrom(dir)
}

func findFrom(dir string) (string, error) {
	start := dir
	for {
		candidate := filepath.Join(dir, FileName)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s found (searched from %s up to the filesystem root)", FileName, start)
		}
		dir = parent
	}
}

// escapes reports whether a relative path climbs out of the build root. A
// Dockerfile outside the project is not a project the config can describe, and
// letting one through would mean the paths on screen no longer say where a
// build came from.
func escapes(rel string) bool {
	if rel == "" {
		return false
	}
	clean := filepath.Clean(rel)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// AbsDockerfile is the build's Dockerfile as an absolute path.
func (c *Config) AbsDockerfile(b Build) string {
	return filepath.Join(c.Root, b.Dockerfile)
}

// AbsContext is the build's docker context as an absolute path.
func (c *Config) AbsContext(b Build) string {
	return filepath.Join(c.Root, b.Context)
}

// validateRegistryAndTargets checks the deployment half of the file. Called
// from Validate.
func (c *Config) validateRegistryAndTargets() error {
	if c.Registry != nil {
		if c.Registry.Account == "" {
			return fmt.Errorf("registry.account is required (it is checked against the live " +
				"caller identity before any push, and an image pushed to the wrong account " +
				"cannot be taken back under immutable tags)")
		}
		if c.Registry.Region == "" {
			return fmt.Errorf("registry.region is required (set it, or project.region)")
		}
	}

	seen := make(map[string]bool, len(c.Targets))
	for i, t := range c.Targets {
		if t.Name == "" {
			return fmt.Errorf("targets[%d] has no name", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("duplicate target %q", t.Name)
		}
		seen[t.Name] = true

		if t.Cluster == "" {
			return fmt.Errorf("target %q has no cluster", t.Name)
		}
		if t.Account == "" {
			return fmt.Errorf("target %q has no account (it is asserted before the deploy runs)", t.Name)
		}
		if t.Region == "" {
			return fmt.Errorf("target %q has no region (set it, or project.region)", t.Name)
		}

		svcSeen := make(map[string]bool, len(t.Services))
		for j, svc := range t.Services {
			if svc == "" {
				return fmt.Errorf("target %q: services[%d] is empty", t.Name, j)
			}
			if svcSeen[svc] {
				return fmt.Errorf("target %q lists the service %q twice", t.Name, svc)
			}
			svcSeen[svc] = true
		}

		if _, err := t.FollowTimeout(0); err != nil {
			return fmt.Errorf("target %q: %w", t.Name, err)
		}
	}
	return nil
}

// FollowTimeout is how long a rollout on this target is followed, falling back
// to def when the target says nothing.
//
// Parsed rather than stored as a duration because TOML has no duration type,
// and returning the error means Validate can reject "10 minutes" at load rather
// than at the end of a deploy.
func (t Target) FollowTimeout(def time.Duration) (time.Duration, error) {
	if t.Timeout == "" {
		return def, nil
	}
	d, err := time.ParseDuration(t.Timeout)
	if err != nil {
		return 0, fmt.Errorf("timeout %q is not a duration (try \"10m\")", t.Timeout)
	}
	if d <= 0 {
		return 0, fmt.Errorf("timeout %q must be positive", t.Timeout)
	}
	return d, nil
}

// RegistryHost is the ECR registry hostname images are pushed to.
func (r Registry) Host() string {
	return r.Account + ".dkr.ecr." + r.Region + ".amazonaws.com"
}

// ImageURI is the full reference for a repository and tag in this registry.
func (r Registry) ImageURI(repo, tag string) string {
	return r.Host() + "/" + repo + ":" + tag
}

// ResolveTarget finds a target by name.
//
// An empty name is allowed only when the project has exactly one target:
// requiring a flag that can take a single value is noise, and a project that
// grows a second target starts requiring it automatically — at which point the
// error names both.
func (c *Config) ResolveTarget(name string) (*Target, error) {
	if len(c.Targets) == 0 {
		return nil, fmt.Errorf("no targets defined in %s", filepath.Base(c.Path))
	}

	if name == "" {
		if len(c.Targets) == 1 {
			return &c.Targets[0], nil
		}
		return nil, fmt.Errorf("more than one target — name one with --to (%s)",
			strings.Join(c.TargetNames(), ", "))
	}

	for i := range c.Targets {
		if c.Targets[i].Name == name {
			return &c.Targets[i], nil
		}
	}
	return nil, fmt.Errorf("unknown target %q (known: %s)", name, strings.Join(c.TargetNames(), ", "))
}

// TargetNames lists every declared target, sorted.
func (c *Config) TargetNames() []string {
	names := make([]string, 0, len(c.Targets))
	for _, t := range c.Targets {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}

// checkVersion refuses a file this binary does not read, naming the way out.
//
// Two directions, because a shared repo has people on either side of an
// upgrade: an older file needs editing, a newer one needs a newer meimei.
func checkVersion(v int) error {
	switch {
	case v == ConfigVersion:
		return nil

	case v > ConfigVersion:
		return fmt.Errorf("this file is version %d, and this meimei reads version %d\n\n"+
			"  upgrade:  go install github.com/apsdsm/meimei@latest",
			v, ConfigVersion)

	// Zero means the key is absent, which is what every file written before the
	// key existed looks like. Same edits either way.
	default:
		return fmt.Errorf("this file is version 1 (%s)\n\n"+
			"  meimei reads version %d only. Three edits:\n"+
			"      add     version = %d      as the first line\n"+
			"      rename  [[services]]  →  [[builds]]\n"+
			"      add     repository and container to every [[builds]]\n\n"+
			"  repository is the ECR repository this build is pushed to, and container the\n"+
			"  name of its container definition in the ECS task definition. Both are declared\n"+
			"  exactly: meimei no longer composes either from the project and build names.",
			versionSaid(v), ConfigVersion, ConfigVersion)
	}
}

func versionSaid(v int) string {
	if v == 0 {
		return "it has no version key"
	}
	return fmt.Sprintf("it says version = %d", v)
}
