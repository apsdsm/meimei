package deploy

import (
	"strings"
	"testing"
)

// The behaviour these tests exist for: two ECS services in one cluster may
// carry a container of the same name — that is what a staging service beside
// production looks like — and meimei used to resolve it silently to whichever
// sorted last, roll that one, and report success.

func svc(name string, containers ...string) Service {
	return Service{Name: name, Family: name, Containers: containers}
}

// The arrangement the scope field exists for: each target sees only its own
// service, so the same container name resolves to a different family per target.
func TestScopedPackingSeparatesTwoEnvironmentsInOneCluster(t *testing.T) {
	prod, err := NewPacking([]Service{svc("tc-public1-chatbot", "chatbot")}, "tc-public1", true)
	if err != nil {
		t.Fatalf("prod: %v", err)
	}
	stg, err := NewPacking([]Service{svc("tc-public1-chatbot-stg", "chatbot")}, "tc-public1", true)
	if err != nil {
		t.Fatalf("stg: %v", err)
	}

	p, ok := prod.ServiceFor("chatbot")
	if !ok || p.Family != "tc-public1-chatbot" {
		t.Errorf("prod resolved to %v, want tc-public1-chatbot", p)
	}
	s, ok := stg.ServiceFor("chatbot")
	if !ok || s.Family != "tc-public1-chatbot-stg" {
		t.Errorf("stg resolved to %v, want tc-public1-chatbot-stg", s)
	}
}

// The old silent misdeploy, now a refusal. Both services are named, because
// picking one is exactly the bug.
func TestDuplicateContainerInOneScopeIsRefused(t *testing.T) {
	_, err := NewPacking([]Service{
		svc("tc-public1-chatbot", "chatbot"),
		svc("tc-public1-chatbot-stg", "chatbot"),
	}, "tc-public1", true)

	if err == nil {
		t.Fatal("two services carrying `chatbot` were accepted, want a refusal")
	}
	for _, want := range []string{"chatbot", "tc-public1-chatbot", "tc-public1-chatbot-stg"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q:\n%v", want, err)
		}
	}
}

// With no declared scope the whole cluster is in scope, so the same collision
// is still a refusal — and the message has to name the way out, because adding
// `services` is what fixes it.
func TestDuplicateWithNoScopeSaysToDeclareServices(t *testing.T) {
	_, err := NewPacking([]Service{
		svc("tc-public1-chatbot", "chatbot"),
		svc("tc-public1-chatbot-stg", "chatbot"),
	}, "tc-public1", false)

	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{"services", "[[targets]]", "tc-public1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q:\n%v", want, err)
		}
	}
}

// Several services in one target is the normal shape — an environment packed
// across two task definitions — and must keep working.
func TestOneScopeMayHoldSeveralServices(t *testing.T) {
	p, err := NewPacking([]Service{
		svc("jjc2-dev1-internal", "api", "process-runner"),
		svc("jjc2-dev1-external", "user-web-spa"),
	}, "jjc2-dev1", true)
	if err != nil {
		t.Fatalf("NewPacking: %v", err)
	}

	for container, family := range map[string]string{
		"api":            "jjc2-dev1-internal",
		"process-runner": "jjc2-dev1-internal",
		"user-web-spa":   "jjc2-dev1-external",
	} {
		got, ok := p.ServiceFor(container)
		if !ok {
			t.Errorf("%s resolved to nothing", container)
			continue
		}
		if got.Family != family {
			t.Errorf("%s resolved to %s, want %s", container, got.Family, family)
		}
	}
}

// Nothing matches by prefix or substring, in either direction. This is a
// regression test for behaviour that is already correct and that any change to
// resolution could quietly lose.
func TestResolutionIsExactNotByPrefix(t *testing.T) {
	p, err := NewPacking([]Service{
		svc("svc-foo", "foo"),
		svc("svc-foobar", "foobar"),
	}, "c", true)
	if err != nil {
		t.Fatalf("NewPacking: %v", err)
	}

	foo, _ := p.ServiceFor("foo")
	bar, _ := p.ServiceFor("foobar")
	if foo.Family != "svc-foo" || bar.Family != "svc-foobar" {
		t.Errorf("foo -> %s, foobar -> %s; want each to reach only its own", foo.Family, bar.Family)
	}
	if _, ok := p.ServiceFor("fo"); ok {
		t.Error("a prefix of a container name resolved to something")
	}
	if _, ok := p.ServiceFor("foobarbaz"); ok {
		t.Error("a string extending a container name resolved to something")
	}
}

// The family is read out of the ARN ECS returns rather than assumed equal to
// the service name, so a cluster that pairs them differently still works.
func TestFamilyFromARN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:ecs:ap-northeast-1:123456789012:task-definition/tc-public1-chatbot:7", "tc-public1-chatbot"},
		{"arn:aws:ecs:ap-northeast-1:123456789012:task-definition/chatbot-task:112", "chatbot-task"},
		{"chatbot-task:3", "chatbot-task"},
		{"chatbot-task", "chatbot-task"},
	}
	for _, tc := range cases {
		got, err := familyFromARN(tc.in)
		if err != nil {
			t.Errorf("familyFromARN(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("familyFromARN(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if _, err := familyFromARN(""); err == nil {
		t.Error("an empty task definition was accepted, want an error")
	}
}
