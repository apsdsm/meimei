// Package catalog answers one question about a project: what can be built here,
// right now?
//
// It reads the config, the working tree and the Dockerfiles, and nothing else.
// No docker daemon, no AWS, no network — so it always answers, it answers
// instantly, and it answers with no credentials. Everything it reports is a
// fact about the checkout in front of you.
package catalog

import (
	"os"
	"sort"

	"github.com/apsdsm/meimei/internal/config"
)

// Status is whether a build's DEFINITION is sound — whether meimei could
// start a build for it at all. It is deliberately not a build state: what a
// build is currently doing is a separate, moving axis that belongs to the
// command running it, and overlaying the two here would mean the catalog could
// not be read without a build in flight.
type Status int

const (
	// Buildable: the definition resolves to a Dockerfile that is there.
	Buildable Status = iota
	// Broken: the config points at a Dockerfile that is not there. Almost
	// always a path typo or a build that moved, and worth seeing beside its
	// working siblings rather than as a fatal error.
	Broken
	// Disabled: declared, deliberately excluded.
	Disabled
)

func (s Status) String() string {
	switch s {
	case Broken:
		return "broken"
	case Disabled:
		return "disabled"
	default:
		return "buildable"
	}
}

// Entry is one build, as the catalog found it.
type Entry struct {
	Build  config.Build
	Status Status

	// Repository is the ECR repository this build is pushed to, copied from
	// the build's declaration. Never composed from anything.
	Repository string
	// Tag is the tag a build would produce right now. Empty without git.
	Tag string

	// Dockerfile and Context are absolute paths.
	Dockerfile string
	Context    string

	// Info is the Dockerfile summary. Zero when the file is missing or
	// unreadable — Status and Problem say which.
	Info Dockerfile

	// Problem explains a Broken status, and is empty otherwise.
	Problem string
}

// Buildable reports whether this entry would be included in a build.
func (e Entry) Buildable() bool { return e.Status == Buildable }

// Catalog is a whole project's worth of entries.
type Catalog struct {
	Config *config.Config
	Git    Git
	// Tag is the tag the entries were resolved against when one was named. It
	// changes every entry's Tag, so it is recorded with them.
	Tag     string
	Entries []Entry
}

// Load reads the catalog for a config. Tag is the tag a build would be given;
// pass "" to tag by commit.
func Load(cfg *config.Config, tag string) *Catalog {
	git := ReadGit(cfg.Root)
	resolved := git.Tag(tag)

	entries := make([]Entry, 0, len(cfg.Builds))
	for _, b := range cfg.Builds {
		e := Entry{
			Build:      b,
			Repository: b.Repository,
			Tag:        resolved,
			Dockerfile: cfg.AbsDockerfile(b),
			Context:    cfg.AbsContext(b),
		}

		switch {
		case b.Disabled:
			e.Status = Disabled
		default:
			e.Status, e.Problem, e.Info = inspect(e.Dockerfile)
		}
		entries = append(entries, e)
	}

	return &Catalog{Config: cfg, Git: git, Tag: tag, Entries: entries}
}

// inspect classifies an image by what is actually on disk at its Dockerfile
// path. A disabled image never gets here — it is excluded by declaration, so
// a missing file is not a problem worth reporting for it.
func inspect(path string) (Status, string, Dockerfile) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Broken, "no Dockerfile at " + path, Dockerfile{}
		}
		return Broken, err.Error(), Dockerfile{}
	}
	if st.IsDir() {
		return Broken, path + " is a directory", Dockerfile{}
	}

	info, err := ReadDockerfile(path)
	if err != nil {
		return Broken, err.Error(), Dockerfile{}
	}
	if len(info.Stages) == 0 {
		// A file with no FROM is not something docker will build, and the
		// commonest way to get one is pointing at the wrong file entirely.
		return Broken, path + " has no FROM instruction", info
	}
	return Buildable, "", info
}

// Groups returns the entries arranged by their declared group, in first-seen
// order, with ungrouped images last.
//
// First-seen rather than sorted: the config's order is the author's, and
// related images are written together. Sorting would scatter them.
//
// This is the repository's own organisation, not a deployment shape — see
// config.Build.Group.
func (c *Catalog) Groups() []Group {
	var order []string
	byGroup := map[string][]Entry{}
	for _, e := range c.Entries {
		g := e.Build.Group
		if _, ok := byGroup[g]; !ok {
			order = append(order, g)
		}
		byGroup[g] = append(byGroup[g], e)
	}

	// The unnamed group is a leftover rather than a group, so it goes last
	// however early it first appeared.
	sort.SliceStable(order, func(i, j int) bool {
		return order[i] != "" && order[j] == ""
	})

	groups := make([]Group, 0, len(order))
	for _, name := range order {
		groups = append(groups, Group{Name: name, Entries: byGroup[name]})
	}
	return groups
}

// Group is a named set of builds, as this repository arranges them.
type Group struct {
	Name    string
	Entries []Entry
}

// Find returns the entry for a build by name.
func (c *Catalog) Find(name string) (Entry, bool) {
	for _, e := range c.Entries {
		if e.Build.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Counts summarises the catalog for a header line.
func (c *Catalog) Counts() (buildable, broken, disabled int) {
	for _, e := range c.Entries {
		switch e.Status {
		case Buildable:
			buildable++
		case Broken:
			broken++
		case Disabled:
			disabled++
		}
	}
	return
}
