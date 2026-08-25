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
// live on acme dev1 before the Deployable/Running split below.
//
// What is packed into which task definition is read from the cluster, never from
// config: the container names inside a service's newest revision are the builds
// it carries. That is a per-cluster Terraform decision, so a copy in config
// would be a mirror that drifts.
//
// WHICH services are this target's is the opposite kind of fact, and it is
// declared. ECS has no notion of an environment, so nothing on the cluster says
// which of its services are production and which are staging. A target names
// them, and resolution is scoped to that list before any container name is
// matched.
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

// Client is one target: an ECS cluster, and the services in it this target
// addresses.
type Client struct {
	ecs     *ecs.Client
	cluster string

	// scope is the target's declared ECS service names. Empty means every
	// service on the cluster, which is right for a cluster carrying one
	// environment.
	scope []string
}

// New builds a client from an already-verified session. scope is the target's
// declared ECS services; nil or empty means the whole cluster.
func New(s *awsx.Session, cluster string, scope []string) *Client {
	return &Client{ecs: ecs.NewFromConfig(s.Config), cluster: cluster, scope: scope}
}

// Service is one ECS service on the cluster, and the containers it runs.
type Service struct {
	// Name is the ECS service — what UpdateService is called against. Family is
	// its task definition family, which RegisterTaskDefinition is called
	// against.
	//
	// Both are READ, neither is derived. Name comes from DescribeServices;
	// Family is parsed out of the task definition ARN that same call returns,
	// which is where ECS itself states it. They are the same string in every
	// cluster we run, because the Terraform here names both from one variable —
	// but that is a convention, not something ECS requires, and meimei no
	// longer relies on it.
	Name   string
	Family string

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
func (s *Service) Behind() bool { return s.Running != s.Deployable }

// Leaving lists containers that are running now and absent from the revision a
// deploy would register. They do not restart — they go away, which is a bigger
// change than a restart and the one most worth warning about.
func (s *Service) Leaving() []string {
	staying := make(map[string]bool, len(s.Containers))
	for _, c := range s.Containers {
		staying[c] = true
	}
	var out []string
	for _, c := range s.RunningContainers {
		if !staying[c] {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// Packing is what the cluster says about which task definition each image is
// packed into.
type Packing struct {
	Services []Service

	// byContainer maps a container name to the service carrying it, WITHIN this
	// target's scope.
	//
	// Scoped, not cluster-wide: two services in one cluster may carry a
	// container of the same name, which is what a staging service beside
	// production looks like, and the target says which of them is this one. A
	// collision inside one scope is a hard error rather than a silent
	// last-writer-wins — see Discover.
	byContainer map[string]*Service
}

// ServiceFor finds the service carrying a container.
func (p *Packing) ServiceFor(container string) (*Service, bool) {
	s, ok := p.byContainer[container]
	return s, ok
}

// ServiceWithFamily finds the service whose task definition family is the given
// one. Takes a family rather than a service name because that is what a caller
// mid-deploy has in hand — it registered a revision of it.
func (p *Packing) ServiceWithFamily(family string) (*Service, bool) {
	for i := range p.Services {
		if p.Services[i].Family == family {
			return &p.Services[i], true
		}
	}
	return nil, false
}

// Containers lists every container name in this target's scope, sorted — the
// answer to "what can I actually deploy here".
func (p *Packing) Containers() []string {
	var out []string
	for name := range p.byContainer {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Discover reads this target's current packing.
//
// Scope first, then match. With a declared scope, exactly those services are
// described and nothing else on the cluster is looked at; without one, every
// service on the cluster is in scope.
func (c *Client) Discover(ctx context.Context) (*Packing, error) {
	names, err := c.inScope(ctx)
	if err != nil {
		return nil, err
	}

	var found []Service

	// DescribeServices takes at most ten at a time.
	for start := 0; start < len(names); start += 10 {
		end := min(start+10, len(names))
		batch := names[start:end]
		out, err := c.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
			Cluster:  &c.cluster,
			Services: batch,
		})
		if err != nil {
			return nil, fmt.Errorf("describing services on %s: %w", c.cluster, err)
		}
		// A service the target names and the cluster does not have comes back
		// as a failure rather than an error. Naming it is the whole point:
		// silently deploying to the rest would be deploying to a target that is
		// not the one that was declared.
		if len(c.scope) > 0 {
			if err := describeFailures(out.Failures, c.cluster); err != nil {
				return nil, err
			}
		}
		for _, awsSvc := range out.Services {
			name := aws.ToString(awsSvc.ServiceName)

			// The family is READ, out of the task definition ARN the service is
			// pointed at, because that is where ECS states it. It is equal to
			// the service name under the Terraform convention here, but nothing
			// depends on that any more.
			family, err := familyFromARN(aws.ToString(awsSvc.TaskDefinition))
			if err != nil {
				return nil, fmt.Errorf("service %s on %s: %w", name, c.cluster, err)
			}

			// The family resolves to the newest revision. This is the one that
			// decides what can be deployed, because it is the one Promote
			// copies.
			newest, err := c.describeTaskDefinition(ctx, family)
			if err != nil {
				return nil, err
			}
			s := Service{
				Name:         name,
				Family:       family,
				Deployable:   aws.ToString(newest.TaskDefinitionArn),
				Running:      aws.ToString(awsSvc.TaskDefinition),
				Desired:      awsSvc.DesiredCount,
				RunningCount: awsSvc.RunningCount,
				Containers:   containerNamesInOrder(newest.ContainerDefinitions),
			}

			// Only fetch the running revision when it is a different one. Under
			// ignore_changes it usually is, but a service that is up to date
			// should not pay for a second call to learn nothing.
			if s.Running == s.Deployable {
				s.RunningContainers = s.Containers
			} else {
				running, err := c.describeTaskDefinition(ctx, s.Running)
				if err != nil {
					return nil, err
				}
				s.RunningContainers = containerNamesInOrder(running.ContainerDefinitions)
			}

			found = append(found, s)
		}
	}

	return NewPacking(found, c.cluster, len(c.scope) > 0)
}

// NewPacking indexes a scope's services by the containers they carry.
//
// Pure, and separate from Discover for that reason: the rule that has actually
// caused trouble is what happens when two services carry the same container
// name, and it is worth having under test without an ECS client. scoped says
// whether the target declared its services, which changes only the advice in
// the refusal.
func NewPacking(services []Service, cluster string, scoped bool) (*Packing, error) {
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	p := &Packing{Services: services, byContainer: make(map[string]*Service, len(services))}
	for i := range p.Services {
		for _, container := range p.Services[i].Containers {
			if prev, clash := p.byContainer[container]; clash {
				return nil, ambiguous(container, cluster, prev.Name, p.Services[i].Name, scoped)
			}
			p.byContainer[container] = &p.Services[i]
		}
	}
	return p, nil
}

// inScope lists the ECS services this target addresses, by name.
func (c *Client) inScope(ctx context.Context) ([]string, error) {
	if len(c.scope) > 0 {
		return c.scope, nil
	}

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
		return nil, fmt.Errorf("cluster %q has no services (is the name right, and has the "+
			"infrastructure been applied?)", c.cluster)
	}
	return arns, nil
}

// describeFailures turns DescribeServices' per-service failures into one error
// naming every service the target declared and the cluster does not have.
func describeFailures(failures []ecstypes.Failure, cluster string) error {
	var missing []string
	for _, f := range failures {
		name := aws.ToString(f.Arn)
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		missing = append(missing, fmt.Sprintf("%s (%s)", name, aws.ToString(f.Reason)))
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("cluster %s does not have %s named by this target: %s",
		cluster, plural(len(missing), "the service", "the services"), strings.Join(missing, ", "))
}

// ambiguous refuses a container name carried by two services in one scope.
//
// It is a refusal rather than a choice because there is no correct choice: both
// services are this target's, and a deploy naming that container cannot say
// which was meant. Before scoping existed this overwrote silently and the
// alphabetically last service won, which rolled the wrong environment and
// reported success.
func ambiguous(container, cluster, a, b string, scoped bool) error {
	if scoped {
		return fmt.Errorf("container %q is on two services this target names, %s and %s — "+
			"a deploy naming it cannot say which one you mean.\n"+
			"       Two services in one target's scope must not carry the same container name; "+
			"split them across two targets, or drop one from this target's `services`.",
			container, a, b)
	}
	return fmt.Errorf("container %q is on two services in cluster %s, %s and %s — "+
		"a deploy naming it cannot say which one you mean.\n\n"+
		"       This target has no `services`, so its scope is the whole cluster. Name the ECS\n"+
		"       services each target addresses to separate them:\n\n"+
		"           [[targets]]\n"+
		"           name     = \"...\"\n"+
		"           cluster  = %q\n"+
		"           services = [%q]\n",
		container, cluster, a, b, cluster, a)
}

// familyFromARN pulls the task definition family out of the reference a service
// is pointed at: arn:aws:ecs:<region>:<acct>:task-definition/<family>:<revision>.
//
// Read rather than assumed equal to the service name. The two are the same
// string under the Terraform convention in these repos, but ECS does not
// require it, and a cluster built by hand is free to pair them however it likes.
func familyFromARN(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("has no task definition")
	}
	rest := ref
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", fmt.Errorf("task definition %q has no family in it", ref)
	}
	return rest, nil
}

// plural picks a form. Duplicated from cmd rather than exported from it: this
// package must not import the command layer.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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

// Promote registers a new revision of one task definition with the given
// containers' images replaced, and points the ECS service at it. It returns the
// new ARN.
//
// Takes the family and the service separately because it calls
// RegisterTaskDefinition against one and UpdateService against the other. They
// are the same string in every cluster we run — see Service — and passing both
// means the caller states which is which rather than relying on that.
//
// Several containers at once by design: images are packed, and promoting them
// one at a time would register a revision and roll the task for each — where
// one revision and one rollout does the same job. On a cluster brought up fresh
// it is also the only thing that works, because Terraform seeds every container
// with an unpullable tag and they are all essential, so no task can start until
// every image is real.
func (c *Client) Promote(ctx context.Context, family, service string, swaps []Swap) (string, error) {
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
		Service:        &service,
		TaskDefinition: &arn,
	}); err != nil {
		return "", fmt.Errorf("pointing %s at %s: %w", service, arn, err)
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
