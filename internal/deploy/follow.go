package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// Follow tuning. A rollout that has not settled in ten minutes is stuck rather
// than slow, and three failed tasks is a service that cannot start rather than
// one unlucky placement.
const (
	DefaultTimeout       = 10 * time.Minute
	DefaultPollInterval  = 10 * time.Second
	DefaultFailThreshold = 3
)

// Phase is what a rollout is doing, named rather than left to be inferred from
// counts.
//
// The counts alone are ambiguous in both directions. "0/0" is printed both when
// ECS has not yet listed the deployment and when it has listed one wanting
// nothing, and "1/1" holds from the moment the new task is healthy until the old
// one has finished draining — so the question worth asking, "what is it waiting
// for", is the one the numbers do not answer.
type Phase string

const (
	// PhaseWaiting means ECS has not listed the deployment yet. Nothing was
	// read, so the counts are not zero — they are absent.
	PhaseWaiting Phase = "waiting"
	// PhaseStarting means the new revision has tasks that are not yet running.
	PhaseStarting Phase = "starting"
	// PhaseHealthy means the new revision is up to its desired count. The
	// rollout is NOT over: the outgoing revision is still draining, which is
	// why ECS keeps the state in progress.
	PhaseHealthy Phase = "healthy"
	// PhaseCompleted is ECS's own COMPLETED — the new revision is the only
	// deployment left.
	PhaseCompleted Phase = "completed"
	// PhaseFailed means ECS gave up, or enough tasks died to stop waiting.
	PhaseFailed Phase = "failed"
)

// Progress is one poll of a rollout.
//
// The counts describe the deployment of the revision being promoted and only
// that one — not the service, and not the outgoing revision, whose tasks are
// still up and still serving. Previous* covers those, because "the new task is
// healthy and the old one has not gone yet" is the state that reads as stuck
// when only the new numbers are shown.
type Progress struct {
	Elapsed time.Duration
	Phase   Phase

	Desired int32
	Running int32
	Pending int32
	Failed  int32

	// PreviousRunning is how many tasks the outgoing deployments still have up.
	// PreviousSeen stays true once one has been observed, so a finished rollout
	// can say the previous revision stopped rather than going quiet — while a
	// first-ever deploy, which never had one, says nothing.
	PreviousRunning int32
	PreviousSeen    bool

	// State is ECS's raw RolloutState, kept for callers that want it. Reason is
	// its RolloutStateReason, which carries the useful text on a failure.
	State  string
	Reason string

	// Event is the service's most recent event message, used to explain a
	// failure rather than printed on every line.
	Event string
}

// detailWidth pads the counts so the previous-revision column lines up down the
// output. A rollout is read by scanning it, and a column that moves every line
// has to be read instead.
const detailWidth = 32

func (p Progress) String() string {
	head := fmt.Sprintf("[%02d:%02d] %-9s", int(p.Elapsed.Minutes()), int(p.Elapsed.Seconds())%60, p.Phase)

	if p.Phase == PhaseWaiting {
		return head + " ECS has not registered the deployment yet"
	}

	detail := fmt.Sprintf("new %d/%d", p.Running, p.Desired)
	if p.Pending > 0 {
		detail += fmt.Sprintf("  starting %d", p.Pending)
	}
	if p.Failed > 0 {
		detail += fmt.Sprintf("  failed %d", p.Failed)
	}

	line := strings.TrimRight(head+" "+pad(detail, detailWidth)+p.previous(), " ")

	// The reason goes on its own line, indented under the counts. On a failure
	// it is the only thing worth reading, and appending it inline would push the
	// columns off the screen on every other line too.
	if p.Phase == PhaseFailed && p.Reason != "" {
		line += "\n" + strings.Repeat(" ", len(head)+1) + p.Reason
	}
	return line
}

// previous describes the outgoing revision — the answer to why a rollout that
// has reached its count is still in progress.
func (p Progress) previous() string {
	switch {
	case p.PreviousRunning > 0 && p.Phase == PhaseHealthy:
		return fmt.Sprintf("previous %d draining", p.PreviousRunning)
	case p.PreviousRunning > 0:
		return fmt.Sprintf("previous %d running", p.PreviousRunning)
	case p.PreviousSeen:
		return "previous stopped"
	}
	// Nothing was replaced — a service being filled for the first time.
	return ""
}

func pad(s string, w int) string {
	if len(s) >= w {
		return s + " "
	}
	return s + strings.Repeat(" ", w-len(s))
}

// FollowOptions tunes a rollout follow. Zero values take the defaults.
type FollowOptions struct {
	Timeout       time.Duration
	PollInterval  time.Duration
	FailThreshold int32

	// OnProgress is called for each poll.
	OnProgress func(Progress)
}

// Follow polls a service until the given revision is running, fails, or the
// timeout runs out.
func (c *Client) Follow(ctx context.Context, service, taskDefARN string, opts FollowOptions) error {
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.FailThreshold == 0 {
		opts.FailThreshold = DefaultFailThreshold
	}

	// Whether an outgoing revision was ever there is a fact about the rollout,
	// not about one poll: once it has drained there is nothing left to observe,
	// and a single poll cannot tell "it has gone" from "there was never one".
	var previousSeen bool

	start := time.Now()
	for {
		p, done, err := c.poll(ctx, service, taskDefARN, start)
		if err != nil {
			return err
		}

		if p.PreviousRunning > 0 {
			previousSeen = true
		}
		p.PreviousSeen = previousSeen

		// Enough dead tasks is a failure whatever ECS still calls the rollout,
		// so the phase is settled here — poll sees one tick and cannot apply a
		// threshold that counts across them.
		failed := p.State == string(ecstypes.DeploymentRolloutStateFailed) || p.Failed >= opts.FailThreshold
		if failed {
			p.Phase = PhaseFailed
		}

		if opts.OnProgress != nil {
			opts.OnProgress(p)
		}

		if done {
			return nil
		}
		if failed {
			return fmt.Errorf("rollout failed (failed tasks: %d): %s",
				p.Failed, c.whyStopped(ctx, service, p.Event))
		}
		if time.Since(start) >= opts.Timeout {
			return fmt.Errorf("rollout did not settle within %s: %s",
				opts.Timeout, c.whyStopped(ctx, service, p.Event))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opts.PollInterval):
		}
	}
}

// poll reads the deployment for one revision and decides whether it is done.
func (c *Client) poll(ctx context.Context, service, taskDefARN string, start time.Time) (Progress, bool, error) {
	out, err := c.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  &c.cluster,
		Services: []string{service},
	})
	if err != nil {
		return Progress{}, false, fmt.Errorf("describing %s: %w", service, err)
	}
	if len(out.Services) == 0 {
		return Progress{}, false, fmt.Errorf("service %q vanished from %s", service, c.cluster)
	}
	svc := out.Services[0]

	p := Progress{Elapsed: time.Since(start), Phase: PhaseWaiting}
	if len(svc.Events) > 0 {
		p.Event = aws.ToString(svc.Events[0].Message)
	}

	var found bool
	for _, d := range svc.Deployments {
		if aws.ToString(d.TaskDefinition) != taskDefARN {
			// Every other deployment is on its way out. Its tasks are still up
			// and still serving, and how many is the whole explanation for a
			// rollout that looks finished and is not.
			p.PreviousRunning += d.RunningCount
			continue
		}
		found = true
		p.Desired, p.Running, p.Pending = d.DesiredCount, d.RunningCount, d.PendingCount
		p.Failed = d.FailedTasks
		p.State = string(d.RolloutState)
		p.Reason = aws.ToString(d.RolloutStateReason)
	}
	if !found {
		// The deployment has not appeared yet, or has been superseded. Either
		// way there is nothing to judge this tick, and the counts stay absent
		// rather than being printed as zeros somebody has to interpret.
		return p, false, nil
	}

	// COMPLETED is the real signal. The fallback is for services whose
	// deployment controller reports no rollout state, and it requires
	// desired > 0: for the first few polls ECS reports 0/0, and a check of
	// "running == desired" alone declares victory before a task has started.
	if p.State == string(ecstypes.DeploymentRolloutStateCompleted) {
		p.Phase = PhaseCompleted
		return p, true, nil
	}
	if p.State == "" && p.Desired > 0 && p.Running == p.Desired && p.Pending == 0 && len(svc.Deployments) == 1 {
		p.Phase = PhaseCompleted
		return p, true, nil
	}

	// Up to count, but not over: ECS keeps a rollout in progress until the
	// outgoing revision has drained.
	if p.Desired > 0 && p.Running == p.Desired && p.Pending == 0 {
		p.Phase = PhaseHealthy
	} else {
		p.Phase = PhaseStarting
	}
	return p, false, nil
}

// whyStopped digs out the reason the most recent stopped task died: the
// task-level reason plus every container that gave one.
//
// A packed task fails as a unit and the culprit is often not the first
// container, so all of them are reported. Best-effort — a failure to explain a
// failure must not replace it.
func (c *Client) whyStopped(ctx context.Context, service, event string) string {
	var parts []string

	list, err := c.ecs.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster:       &c.cluster,
		ServiceName:   &service,
		DesiredStatus: ecstypes.DesiredStatusStopped,
	})
	if err == nil && len(list.TaskArns) > 0 {
		newest := list.TaskArns[len(list.TaskArns)-1]
		desc, err := c.ecs.DescribeTasks(ctx, &ecs.DescribeTasksInput{
			Cluster: &c.cluster,
			Tasks:   []string{newest},
		})
		if err == nil && len(desc.Tasks) > 0 {
			t := desc.Tasks[0]
			if r := aws.ToString(t.StoppedReason); r != "" {
				parts = append(parts, r)
			}
			for _, cont := range t.Containers {
				if r := aws.ToString(cont.Reason); r != "" {
					parts = append(parts, aws.ToString(cont.Name)+": "+r)
				}
			}
		}
	}

	if event != "" {
		parts = append(parts, event)
	}
	if len(parts) == 0 {
		return "no reason reported by ECS"
	}
	return strings.Join(parts, " | ")
}
