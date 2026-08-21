// Package deploy promotes images onto an ECS cluster.
//
// A deploy never applies Terraform. Terraform owns the shape of a task
// definition and carries `ignore_changes = [task_definition]` so that deploys
// can change the one thing they are about — the image — without fighting it. So
// promoting is: read the NEWEST revision of the family, swap the image on the
// named containers, register the result, and point the service at it.
//
// The newest revision, not the one the service is running, and the difference is
// the whole reason `ignore_changes` exists: Terraform registers a revision and
// leaves the service where it was, so the shape a deploy should carry forward is
// the registered one. Reading the running revision instead would silently
// discard every shape change since.
//
// That has to hold for the packing too, and it is the easier half to get wrong.
// A container Terraform has just added exists ONLY in the newest revision, so a
// packing read from what is running does not know it can be deployed — and a
// container Terraform has just removed is still in the running one, so such a
// packing believes it will survive a deploy that in fact drops it. Both were
// live on jjc2 dev1; see docs/known-issues.md.
//
// What is packed into which task is read from the cluster, never from config. A
// service's name is its task definition family, and the container names inside
// it are the services it carries. That is a per-cluster Terraform decision, so
// a copy in config would be a mirror that drifts.
package deploy

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/apsdsm/meimei/internal/awsx"
)

// Client is an ECS cluster meimei can deploy to.
type Client struct {
	ecs     *ecs.Client
	cluster string
}

// New builds a client from an already-verified session.
func New(s *awsx.Session, cluster string) *Client {
	return &Client{ecs: ecs.NewFromConfig(s.Config), cluster: cluster}
}

// Task is one ECS service on the cluster, and the containers it runs.
//
// The service name and the task definition family are the same string — that is
// how ECS is set up here, and it is what lets a container name be traced back
// to the thing that has to be rolled to change it.
type Task struct {
	Service string // == the task definition family

	// Deployable is the newest revision of the family — the one Promote copies
	// — and Containers are its containers. What a deploy may name comes from
	// here, never from what is running: a container Terraform registered a
	// moment ago is deployable before it has ever run.
	Deployable string
	Containers []string

	// Running is the revision the service actually runs, and RunningContainers
	// its containers. Behind Deployable whenever Terraform has registered a
	// shape the service has not picked up. For REPORTING only — it must not
	// decide what can be deployed, which is the bug this split fixes.
	Running           string
	RunningContainers []string

	Desired      int32
	RunningCount int32
}

// Behind reports whether the service is running an older revision than the one
// a deploy would copy — the normal state under `ignore_changes`, and worth
// saying out loud because it is what makes the two container lists differ.
func (t *Task) Behind() bool { return t.Running != t.Deployable }

// Leaving lists containers that are running now and absent from the revision a
// deploy would register. They do not restart — they go away, which is a bigger
// change than a restart and the one most worth warning about.
func (t *Task) Leaving() []string {
	staying := make(map[string]bool, len(t.Containers))
	for _, c := range t.Containers {
		staying[c] = true
	}
	var out []string
	for _, c := range t.RunningContainers {
		if !staying[c] {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// Packing is what the cluster says about where each service lives.
type Packing struct {
	Tasks []Task

	// byContainer maps a container name to the task carrying it.
	byContainer map[string]*Task
}

// TaskFor finds the task carrying a container.
func (p *Packing) TaskFor(container string) (*Task, bool) {
	t, ok := p.byContainer[container]
	return t, ok
}

// TaskNamed finds a task by its family.
func (p *Packing) TaskNamed(family string) (*Task, bool) {
	for i := range p.Tasks {
		if p.Tasks[i].Service == family {
			return &p.Tasks[i], true
		}
	}
	return nil, false
}

// Containers lists every container name on the cluster, sorted — the answer to
// "what can I actually deploy here".
func (p *Packing) Containers() []string {
	var out []string
	for name := range p.byContainer {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Discover reads the cluster's current packing.
func (c *Client) Discover(ctx context.Context) (*Packing, error) {
	var arns []string
	pager := ecs.NewListServicesPaginator(c.ecs, &ecs.ListServicesInput{Cluster: &c.cluster})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing services on %s: %w", c.cluster, err)
		}
		arns = append(arns, page.ServiceArns...)
	}
	if len(arns) == 0 {
		return nil, fmt.Errorf("cluster %q has no services (is the name right, and has Terraform been applied?)", c.cluster)
	}

	p := &Packing{byContainer: map[string]*Task{}}

	// DescribeServices takes at most ten at a time.
	for start := 0; start < len(arns); start += 10 {
		end := min(start+10, len(arns))
		out, err := c.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
			Cluster:  &c.cluster,
			Services: arns[start:end],
		})
		if err != nil {
			return nil, fmt.Errorf("describing services on %s: %w", c.cluster, err)
		}
		for _, svc := range out.Services {
			family := aws.ToString(svc.ServiceName)

			// The family resolves to the newest revision. This is the one that
			// decides what can be deployed, because it is the one Promote
			// copies.
			newest, err := c.describeTaskDefinition(ctx, family)
			if err != nil {
				return nil, err
			}
			t := Task{
				Service:      family,
				Deployable:   aws.ToString(newest.TaskDefinitionArn),
				Running:      aws.ToString(svc.TaskDefinition),
				Desired:      svc.DesiredCount,
				RunningCount: svc.RunningCount,
				Containers:   containerNamesInOrder(newest.ContainerDefinitions),
			}

			// Only fetch the running revision when it is a different one. Under
			// ignore_changes it usually is, but a service that is up to date
			// should not pay for a second call to learn nothing.
			if t.Running == t.Deployable {
				t.RunningContainers = t.Containers
			} else {
				running, err := c.describeTaskDefinition(ctx, t.Running)
				if err != nil {
					return nil, err
				}
				t.RunningContainers = containerNamesInOrder(running.ContainerDefinitions)
			}

			p.Tasks = append(p.Tasks, t)
		}
	}

	sort.Slice(p.Tasks, func(i, j int) bool { return p.Tasks[i].Service < p.Tasks[j].Service })
	for i := range p.Tasks {
		for _, name := range p.Tasks[i].Containers {
			p.byContainer[name] = &p.Tasks[i]
		}
	}
	return p, nil
}

func (c *Client) describeTaskDefinition(ctx context.Context, ref string) (*ecstypes.TaskDefinition, error) {
	out, err := c.ecs.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{TaskDefinition: &ref})
	if err != nil {
		return nil, fmt.Errorf("describing task definition %s: %w", ref, err)
	}
	return out.TaskDefinition, nil
}

// Swap names an image to put on a container.
type Swap struct {
	Container string
	Image     string
}

// Promote registers a new revision of one task with the given containers'
// images replaced, and points the service at it. It returns the new ARN.
//
// Several containers at once by design: services are packed, and promoting them
// one at a time would register a revision and roll the task for each — where
// one revision and one rollout does the same job. On a cluster brought up fresh
// it is also the only thing that works, because Terraform seeds every container
// with an unpullable tag and they are all essential, so the task cannot start
// until every image is real.
func (c *Client) Promote(ctx context.Context, family string, swaps []Swap) (string, error) {
	// The family, not the service's current ARN: the service can be several
	// revisions behind what Terraform has registered (that is what
	// ignore_changes produces), and deploying from the running revision would
	// silently discard the shape changes in between. The family resolves to the
	// newest revision, which is the one Terraform means.
	td, err := c.describeTaskDefinition(ctx, family)
	if err != nil {
		return "", err
	}

	containers := td.ContainerDefinitions
	for _, s := range swaps {
		found := false
		for i := range containers {
			if aws.ToString(containers[i].Name) == s.Container {
				containers[i].Image = aws.String(s.Image)
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("no container %q in task definition %q — the cluster has %s",
				s.Container, family, strings.Join(containerNames(containers), ", "))
		}
	}

	// Every registerable field is carried over. The input type is the
	// whitelist: describe returns read-only fields (arn, revision, status,
	// requiresAttributes, compatibilities, registeredAt, registeredBy) that
	// register refuses, and they simply have nowhere to go here. Copying field
	// by field rather than filtering means a task using ephemeral storage, a
	// proxy configuration or a pid namespace keeps them — a hand-written
	// whitelist that forgot one would drop it silently.
	in := &ecs.RegisterTaskDefinitionInput{
		Family:                  td.Family,
		ContainerDefinitions:    containers,
		Cpu:                     td.Cpu,
		Memory:                  td.Memory,
		NetworkMode:             td.NetworkMode,
		TaskRoleArn:             td.TaskRoleArn,
		ExecutionRoleArn:        td.ExecutionRoleArn,
		Volumes:                 td.Volumes,
		PlacementConstraints:    td.PlacementConstraints,
		RequiresCompatibilities: td.RequiresCompatibilities,
		RuntimePlatform:         td.RuntimePlatform,
		EphemeralStorage:        td.EphemeralStorage,
		IpcMode:                 td.IpcMode,
		PidMode:                 td.PidMode,
		ProxyConfiguration:      td.ProxyConfiguration,
		InferenceAccelerators:   td.InferenceAccelerators,
		EnableFaultInjection:    td.EnableFaultInjection,
	}

	reg, err := c.ecs.RegisterTaskDefinition(ctx, in)
	if err != nil {
		return "", fmt.Errorf("registering a revision of %s: %w", family, err)
	}
	arn := aws.ToString(reg.TaskDefinition.TaskDefinitionArn)

	if _, err := c.ecs.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:        &c.cluster,
		Service:        &family,
		TaskDefinition: &arn,
	}); err != nil {
		return "", fmt.Errorf("pointing %s at %s: %w", family, arn, err)
	}
	return arn, nil
}

// CurrentImages reports what each container in a family runs today, so a deploy
// can say what is changing.
func (c *Client) CurrentImages(ctx context.Context, family string) (map[string]string, error) {
	td, err := c.describeTaskDefinition(ctx, family)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, cd := range td.ContainerDefinitions {
		out[aws.ToString(cd.Name)] = aws.ToString(cd.Image)
	}
	return out, nil
}

func containerNames(cds []ecstypes.ContainerDefinition) []string {
	out := containerNamesInOrder(cds)
	sort.Strings(out)
	return out
}

// containerNamesInOrder keeps the task definition's own order, which is the
// order Terraform wrote and therefore the one a reader recognises.
func containerNamesInOrder(cds []ecstypes.ContainerDefinition) []string {
	out := make([]string, 0, len(cds))
	for _, cd := range cds {
		out = append(out, aws.ToString(cd.Name))
	}
	return out
}
