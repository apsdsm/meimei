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

	"github.com/BurntSushi/toml"
)

// FileName is the config file meimei looks for.
const FileName = ".meimei.toml"

// DefaultPlatform is what images are built for when nothing says otherwise.
// Every ECS target we deploy to runs on Graviton, so an amd64 image would be
// dead weight nothing schedules.
const DefaultPlatform = "linux/arm64"

// Config is a whole .meimei.toml.
type Config struct {
	Project  Project   `toml:"project"`
	Services []Service `toml:"services"`
	Registry *Registry `toml:"registry"`
	Targets  []Target  `toml:"targets"`

	// Path is the absolute path of the file that was loaded, and Root the
	// directory holding it. Populated by Load; not parsed from TOML.
	Path string `toml:"-"`
	Root string `toml:"-"`
}

// Project is the repo-wide header.
type Project struct {
	// Name prefixes every image repository: service "api" in project "acme"
	// builds acme-api. It is the `PRODUCT` of the shell scripts.
	Name string `toml:"name"`

	// Region is the AWS region. Unused while meimei only reads local state,
	// but it belongs to the project rather than to any one command.
	Region string `toml:"region"`

	// Platform is the default build platform for every service.
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

// Target is somewhere images get deployed to.
//
// It holds only what cannot be discovered: which account and cluster a target
// name refers to, and how to authenticate. Which task each service is packed
// into is NOT here — that is a per-cluster Terraform decision, read from the
// cluster at deploy time.
type Target struct {
	// Name is what you type: `--to dev1`.
	Name string `toml:"name"`

	// Account is the AWS account holding the cluster, asserted before any
	// call. Often, but not always, the registry's.
	Account string `toml:"account"`

	// Profile is the AWS shared-config profile for that account.
	Profile string `toml:"profile"`

	// Cluster is the ECS cluster name. Declared because naming conventions
	// differ per product (`acme-dev1-cluster` against `nova-public1`),
	// and a convention that holds for one repo is not one meimei can assume.
	Cluster string `toml:"cluster"`

	// Region defaults to the project's.
	Region string `toml:"region"`
}

// Service is one buildable container image.
type Service struct {
	// Name is the service's identity everywhere: the image repository suffix,
	// the container name inside its ECS task, and what you type at the CLI.
	Name string `toml:"name"`

	// Short is an optional abbreviation, for a future keyboard shortcut.
	Short string `toml:"short"`

	// Dockerfile is the path to build from, relative to the build root.
	Dockerfile string `toml:"dockerfile"`

	// Context is the docker build context, relative to the build root.
	// Defaults to the build root itself, which is what every service we have
	// needs — Dockerfiles copy across service boundaries (the api's go.work
	// replace, the web workspace's shared install).
	Context string `toml:"context"`

	// Group is how THIS REPOSITORY organises its services — "api", "web",
	// "worker" — and is used only to arrange them on screen. It says nothing
	// about how they are deployed.
	//
	// Deliberately not the ECS task a service is packed into. Packing is a
	// per-cluster Terraform decision made for that cluster's box (dev1 packs
	// everything onto two tasks so an m7g.medium needs only ~2 task-ENIs; a
	// production cluster sized for real traffic will split them differently),
	// so a copy here would be a global mirror of a per-target fact — one that
	// can only ever drift, and drift silently. meimei reads the packing from
	// the cluster instead: an ECS service's name is its task family, and its
	// container names are the services in it, so two API calls give the
	// authoritative mapping for whichever target is being deployed to.
	Group string `toml:"group"`

	// Platform overrides Project.Platform for this service alone.
	Platform string `toml:"platform"`

	// Color is the accent used for this service's name on screen.
	Color string `toml:"color"`

	// Disabled keeps a service in the file but out of every build. A service
	// that is being brought up, or one that has been retired but whose
	// definition is not ready to delete, is better declared than forgotten.
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
	cfg.Path = abs
	cfg.Root = filepath.Dir(abs)

	if cfg.Project.Platform == "" {
		cfg.Project.Platform = DefaultPlatform
	}
	for i := range cfg.Services {
		if cfg.Services[i].Platform == "" {
			cfg.Services[i].Platform = cfg.Project.Platform
		}
		if cfg.Services[i].Context == "" {
			cfg.Services[i].Context = "."
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
// services. The catalog reports it per service instead.
func (c *Config) Validate() error {
	if c.Project.Name == "" {
		return fmt.Errorf("project.name is required (it prefixes every image repository)")
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("no services defined")
	}

	seen := make(map[string]bool, len(c.Services))
	shorts := make(map[string]string, len(c.Services))
	for i, s := range c.Services {
		if s.Name == "" {
			return fmt.Errorf("services[%d] has no name", i)
		}
		if seen[s.Name] {
			return fmt.Errorf("duplicate service %q", s.Name)
		}
		seen[s.Name] = true

		if s.Short != "" {
			if prev, clash := shorts[s.Short]; clash {
				return fmt.Errorf("services %q and %q share the short name %q", prev, s.Name, s.Short)
			}
			shorts[s.Short] = s.Name
		}

		if s.Dockerfile == "" {
			return fmt.Errorf("service %q has no dockerfile", s.Name)
		}
		if filepath.IsAbs(s.Dockerfile) {
			return fmt.Errorf("service %q: dockerfile must be relative to the build root, got %q", s.Name, s.Dockerfile)
		}
		if escapes(s.Dockerfile) {
			return fmt.Errorf("service %q: dockerfile %q escapes the build root", s.Name, s.Dockerfile)
		}
		if filepath.IsAbs(s.Context) {
			return fmt.Errorf("service %q: context must be relative to the build root, got %q", s.Name, s.Context)
		}
		if escapes(s.Context) {
			return fmt.Errorf("service %q: context %q escapes the build root", s.Name, s.Context)
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

// AbsDockerfile is the service's Dockerfile as an absolute path.
func (c *Config) AbsDockerfile(s Service) string {
	return filepath.Join(c.Root, s.Dockerfile)
}

// AbsContext is the service's build context as an absolute path.
func (c *Config) AbsContext(s Service) string {
	return filepath.Join(c.Root, s.Context)
}

// Repository is the image repository name for a service: project-service, the
// same derivation the shell scripts use (`${PRODUCT}-${SERVICE}`).
func (c *Config) Repository(s Service) string {
	return c.Project.Name + "-" + s.Name
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
	}
	return nil
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
