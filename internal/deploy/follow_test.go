package deploy

import (
	"strings"
	"testing"
	"time"
)

// The output exists to answer two questions the raw counts cannot: whether a
// zero means "nothing running" or "nothing read", and what a rollout that has
// reached its desired count is still waiting for. Both are asserted here.

func TestProgressWaitingSaysNothingWasRead(t *testing.T) {
	p := Progress{Elapsed: 3 * time.Second, Phase: PhaseWaiting}

	got := p.String()
	if !strings.Contains(got, "not registered") {
		t.Errorf("waiting line = %q, want it to say the deployment is not registered", got)
	}
	// The counts are absent, not zero. Printing "0/0" here is the thing that
	// made a rollout look finished before it had started.
	if strings.Contains(got, "0/0") {
		t.Errorf("waiting line = %q, want no counts at all", got)
	}
}

func TestProgressStartingShowsWhatIsComingUpAndWhatIsStillServing(t *testing.T) {
	p := Progress{
		Elapsed:         20 * time.Second,
		Phase:           PhaseStarting,
		Desired:         1,
		Pending:         1,
		PreviousRunning: 1,
	}

	got := p.String()
	for _, want := range []string{"[00:20]", "starting", "new 0/1", "starting 1", "previous 1 running"} {
		if !strings.Contains(got, want) {
			t.Errorf("starting line = %q, want it to contain %q", got, want)
		}
	}
}

func TestProgressHealthyNamesTheDrainingPrevious(t *testing.T) {
	p := Progress{
		Elapsed:         71 * time.Second,
		Phase:           PhaseHealthy,
		Desired:         1,
		Running:         1,
		PreviousRunning: 1,
		PreviousSeen:    true,
	}

	got := p.String()
	if !strings.Contains(got, "new 1/1") {
		t.Errorf("healthy line = %q, want the new revision up to count", got)
	}
	// This is the whole point of the phase: 1/1 and still in progress reads as
	// stuck unless the output says what it is waiting on.
	if !strings.Contains(got, "previous 1 draining") {
		t.Errorf("healthy line = %q, want it to say the previous revision is draining", got)
	}
}

func TestProgressCompletedSaysThePreviousStoppedOnlyIfThereWasOne(t *testing.T) {
	replaced := Progress{Phase: PhaseCompleted, Desired: 1, Running: 1, PreviousSeen: true}
	if got := replaced.String(); !strings.Contains(got, "previous stopped") {
		t.Errorf("completed line = %q, want it to report the previous revision stopped", got)
	}

	// A service being filled for the first time never had one, and saying it
	// stopped would invent a revision that never existed.
	fresh := Progress{Phase: PhaseCompleted, Desired: 1, Running: 1}
	if got := fresh.String(); strings.Contains(got, "previous") {
		t.Errorf("first-deploy line = %q, want no mention of a previous revision", got)
	}
}

func TestProgressFailedPutsTheReasonOnItsOwnLine(t *testing.T) {
	p := Progress{
		Elapsed: 40 * time.Second,
		Phase:   PhaseFailed,
		Desired: 1,
		Failed:  2,
		Reason:  "ECS deployment circuit breaker: task failed to start.",
	}

	got := p.String()
	if !strings.Contains(got, "failed 2") {
		t.Errorf("failed line = %q, want the failed count", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("failed output = %q, want the reason on a second line", got)
	}
	if !strings.Contains(lines[1], "circuit breaker") {
		t.Errorf("second line = %q, want the rollout reason", lines[1])
	}
}

func TestProgressPreviousColumnLinesUp(t *testing.T) {
	// A rollout is read by scanning down it, so the previous-revision column
	// has to sit at the same offset whether or not there are extra counts.
	starting := Progress{Phase: PhaseStarting, Desired: 1, Pending: 1, PreviousRunning: 1}
	healthy := Progress{Phase: PhaseHealthy, Desired: 1, Running: 1, PreviousRunning: 1}

	a := strings.Index(starting.String(), "previous")
	b := strings.Index(healthy.String(), "previous")
	if a != b {
		t.Errorf("previous column at %d and %d, want the same offset\n%s\n%s", a, b, starting, healthy)
	}
}

// Service's two container lists are the fix for a deploy that described the wrong
// task definition: what can be deployed comes from the newest revision, and what
// is going away can only be seen by comparing it with the running one.

func TestServiceBehindComparesRevisions(t *testing.T) {
	behind := Service{Running: "arn:…/jjc2-dev1-internal:5", Deployable: "arn:…/jjc2-dev1-internal:7"}
	if !behind.Behind() {
		t.Error("Behind() = false, want true when the service runs an older revision")
	}

	current := Service{Running: "arn:…/x:7", Deployable: "arn:…/x:7"}
	if current.Behind() {
		t.Error("Behind() = true, want false when the service is on the newest revision")
	}
}

func TestServiceLeavingFindsContainersTerraformDropped(t *testing.T) {
	// The real case from jjc2 dev1: the internal task definition ran api and
	// process-runner, and the revision a deploy would register carries only the runner.
	svc := Service{
		Containers:        []string{"process-runner"},
		RunningContainers: []string{"api", "process-runner"},
	}

	leaving := svc.Leaving()
	if len(leaving) != 1 || leaving[0] != "api" {
		t.Errorf("Leaving() = %v, want [api] — it is removed, not restarted", leaving)
	}
}

func TestServiceLeavingIgnoresContainersOnlyInTheNewRevision(t *testing.T) {
	// The mirror case: containers Terraform has added but nothing has run yet.
	// They are arriving, not leaving, and must not be reported as either.
	svc := Service{
		Containers:        []string{"api", "sysadmin-web-spa", "user-web-spa"},
		RunningContainers: []string{"api"},
	}

	if leaving := svc.Leaving(); len(leaving) != 0 {
		t.Errorf("Leaving() = %v, want nothing", leaving)
	}
}
