package config

import (
	"strings"
	"testing"
	"time"
)

const withTargets = `
[project]
name = "tc"
region = "ap-northeast-1"

[[builds]]
name = "manualbot"
repository = "tc-manualbot"
container = "manualbot"
dockerfile = "Dockerfile"

[registry]
account = "629585638685"
profile = "tcr"

[[targets]]
name = "public1"
account = "629585638685"
profile = "tcr"
cluster = "tc-public1"
`

func TestRegistryHostAndImageURI(t *testing.T) {
	cfg, err := LoadFrom(write(t, withTargets))
	if err != nil {
		t.Fatal(err)
	}

	// Region falls through from the project rather than being repeated.
	if cfg.Registry.Region != "ap-northeast-1" {
		t.Errorf("registry region = %q, want it inherited from the project", cfg.Registry.Region)
	}
	want := "629585638685.dkr.ecr.ap-northeast-1.amazonaws.com"
	if got := cfg.Registry.Host(); got != want {
		t.Errorf("Host() = %q, want %q", got, want)
	}
	if got := cfg.Registry.ImageURI("tc-manualbot", "sha-abc"); got != want+"/tc-manualbot:sha-abc" {
		t.Errorf("ImageURI() = %q", got)
	}
	if cfg.Targets[0].Region != "ap-northeast-1" {
		t.Errorf("target region = %q, want it inherited", cfg.Targets[0].Region)
	}
}

// One target means --to is noise, so it is optional. A second one makes it
// required, and the error names both.
func TestResolveTargetOptionalWhenSingular(t *testing.T) {
	cfg, err := LoadFrom(write(t, withTargets))
	if err != nil {
		t.Fatal(err)
	}

	got, err := cfg.ResolveTarget("")
	if err != nil {
		t.Fatalf("ResolveTarget(\"\") = %v, want the only target", err)
	}
	if got.Name != "public1" {
		t.Errorf("target = %q, want public1", got.Name)
	}

	if _, err := cfg.ResolveTarget("public1"); err != nil {
		t.Errorf("naming the only target should work: %v", err)
	}
}

func TestResolveTargetAmbiguous(t *testing.T) {
	cfg, err := LoadFrom(write(t, withTargets+`
[[targets]]
name = "public2"
account = "629585638685"
cluster = "tc-public2"
`))
	if err != nil {
		t.Fatal(err)
	}

	_, err = cfg.ResolveTarget("")
	if err == nil {
		t.Fatal("want an error when several targets exist and none is named")
	}
	for _, want := range []string{"--to", "public1", "public2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}

	if _, err := cfg.ResolveTarget("public9"); err == nil {
		t.Error("want an error for an unknown target")
	}
}

func TestNoTargets(t *testing.T) {
	cfg, err := LoadFrom(write(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ResolveTarget(""); err == nil {
		t.Error("want an error when the project declares no targets")
	}
	// A project with no registry still loads — building locally needs neither.
	if cfg.Registry != nil {
		t.Error("registry should be absent, not defaulted")
	}
}

func TestRegistryAndTargetValidation(t *testing.T) {
	base := "[project]\nname = \"tc\"\nregion = \"ap-northeast-1\"\n\n[[builds]]\nname = \"a\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"D\"\n"
	cases := []struct{ name, body, want string }{
		{"registry without account", base + "[registry]\nprofile = \"p\"\n", "registry.account is required"},
		{"target without cluster", base + "[[targets]]\nname = \"t\"\naccount = \"1\"\n", "has no cluster"},
		{"target without account", base + "[[targets]]\nname = \"t\"\ncluster = \"c\"\n", "has no account"},
		{"target without name", base + "[[targets]]\ncluster = \"c\"\naccount = \"1\"\n", "targets[0] has no name"},
		{"duplicate target", base + "[[targets]]\nname = \"t\"\ncluster = \"c\"\naccount = \"1\"\n\n[[targets]]\nname = \"t\"\ncluster = \"d\"\naccount = \"1\"\n", "duplicate target"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFrom(write(t, tc.body))
			if err == nil {
				t.Fatalf("want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
		})
	}
}

// A region has to come from somewhere; project.region is the usual source.
func TestRegistryNeedsARegion(t *testing.T) {
	_, err := LoadFrom(write(t, "[project]\nname = \"tc\"\n\n[[builds]]\nname = \"a\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"D\"\n\n[registry]\naccount = \"1\"\n"))
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("err = %v, want a complaint about the missing region", err)
	}
}

// A target's services are exact names and its own scope. Two targets in one
// cluster naming different services is the arrangement the field exists for.
func TestTargetServicesAreDeclared(t *testing.T) {
	cfg, err := LoadFrom(write(t, `
[project]
name = "tc"
region = "ap-northeast-1"

[[builds]]
name = "chatbot"
repository = "tc-chatbot"
container = "chatbot"
dockerfile = "D"

[[targets]]
name = "prod"
account = "1"
profile = "p"
cluster = "tc-public1"
services = ["tc-public1-chatbot"]

[[targets]]
name = "stg"
account = "1"
profile = "p"
cluster = "tc-public1"
services = ["tc-public1-chatbot-stg"]
timeout = "15m"
`))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := cfg.Targets[0].Services; len(got) != 1 || got[0] != "tc-public1-chatbot" {
		t.Errorf("prod services = %v, want the one declared name", got)
	}
	if cfg.Targets[0].Cluster != cfg.Targets[1].Cluster {
		t.Fatal("the fixture is meant to put both targets in one cluster")
	}

	// Omitted means the whole cluster, which is what every file written before
	// the field looked like.
	d, err := cfg.Targets[0].FollowTimeout(defaultForTest)
	if err != nil {
		t.Fatalf("FollowTimeout: %v", err)
	}
	if d != defaultForTest {
		t.Errorf("prod timeout = %s, want the default when unset", d)
	}
	d, err = cfg.Targets[1].FollowTimeout(defaultForTest)
	if err != nil {
		t.Fatalf("FollowTimeout: %v", err)
	}
	if d != 15*time.Minute {
		t.Errorf("stg timeout = %s, want its own 15m", d)
	}
}

const defaultForTest = 10 * time.Minute

func TestTargetRejects(t *testing.T) {
	base := "[project]\nname = \"tc\"\nregion = \"r\"\n\n[[builds]]\nname = \"a\"\nrepository = \"r\"\ncontainer = \"c\"\ndockerfile = \"D\"\n"
	cases := []struct{ name, body, want string }{
		{
			"duplicate service in one target",
			base + "\n[[targets]]\nname = \"t\"\naccount = \"1\"\ncluster = \"c\"\nservices = [\"s\", \"s\"]\n",
			`lists the service "s" twice`,
		},
		{
			"empty service name",
			base + "\n[[targets]]\nname = \"t\"\naccount = \"1\"\ncluster = \"c\"\nservices = [\"\"]\n",
			"services[0] is empty",
		},
		{
			"unparseable timeout",
			base + "\n[[targets]]\nname = \"t\"\naccount = \"1\"\ncluster = \"c\"\ntimeout = \"10 minutes\"\n",
			"is not a duration",
		},
		{
			"negative timeout",
			base + "\n[[targets]]\nname = \"t\"\naccount = \"1\"\ncluster = \"c\"\ntimeout = \"-5m\"\n",
			"must be positive",
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
