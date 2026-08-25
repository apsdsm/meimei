package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/apsdsm/meimei/internal/awsx"
	"github.com/apsdsm/meimei/internal/catalog"
	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/deploy"
	"github.com/apsdsm/meimei/internal/registry"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [name...]",
	Short: "Promote images onto a target's ECS services",
	Long: "Point a target's ECS services at a different image.\n\n" +
		"A deploy copies the task definition that is registered, swaps the image on the\n" +
		"named containers, registers the result and rolls the ECS service. It never\n" +
		"changes anything else about the task definition — whatever manages your\n" +
		"infrastructure owns the shape.\n\n" +
		"A target names which of its cluster's ECS services it addresses, so two targets\n" +
		"may point into one cluster. What is IN each service is read from the cluster and\n" +
		"never from config: the container names in a service's newest revision are the\n" +
		"builds it carries. Builds sharing a task definition are promoted in ONE revision\n" +
		"and ONE rollout, which is also the only thing that works on a cluster brought up\n" +
		"fresh, where no task can start until every image in its definition is real.",
	Example: "  meimei deploy chatbot --tag sha-eba96de\n" +
		"  meimei deploy --all --tag acme.2026_010.001 --target dev1\n" +
		"  meimei deploy api --tag sha-abc1234 --target dev1 --dry-run",
	RunE: runDeploy,
}

func init() {
	deployCmd.Flags().Bool("all", false, "Deploy everything in this target's scope")
	deployCmd.Flags().String("tag", "", "Image tag to promote (default: the current commit)")
	deployCmd.Flags().String("target", "", "Target to deploy to (optional when the project declares one)")
	deployCmd.Flags().Bool("no-follow", false, "Trigger the rollout and return without waiting")
	deployCmd.Flags().Bool("dry-run", false, "Print what would be promoted, and change nothing")
	deployCmd.Flags().Bool("skip-image-check", false,
		"Promote without asking the registry whether the images are there")
	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.Registry == nil {
		return fmt.Errorf("no [registry] in %s — a deploy needs to know where images live", cfg.Path)
	}

	all, _ := cmd.Flags().GetBool("all")
	tag, _ := cmd.Flags().GetString("tag")
	to, _ := cmd.Flags().GetString("target")
	noFollow, _ := cmd.Flags().GetBool("no-follow")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	skipCheck, _ := cmd.Flags().GetBool("skip-image-check")

	target, err := cfg.ResolveTarget(to)
	if err != nil {
		return err
	}

	// How long to follow a rollout belongs to the target: a slow production
	// service wants a different value from dev1, and it does not change between
	// two deploys to the same place. Validate has already checked it parses.
	timeout, err := target.FollowTimeout(deploy.DefaultTimeout)
	if err != nil {
		return err
	}

	cat := catalog.Load(cfg, "")

	// No tag means "what this checkout is", which is the common case right
	// after a build. It is never inferred from the registry — deploying
	// whatever happens to be newest there is how you ship something nobody
	// meant to.
	if tag == "" {
		tag = cat.Git.Tag("")
		if tag == "" {
			return fmt.Errorf("no --tag given and no git commit to infer one from")
		}
		fmt.Fprintf(os.Stderr, "no --tag given, using this checkout: %s\n", tag)
	}

	ctx := cmd.Context()

	sess, err := awsx.Open(ctx, target.Profile, target.Region, target.Account)
	if err != nil {
		return err
	}
	client := deploy.New(sess, target.Cluster, target.Services)

	packing, err := client.Discover(ctx)
	if err != nil {
		return err
	}

	// Selection needs the packing, because --all means everything in THIS
	// TARGET'S scope rather than everything the repository declares. A build
	// this repo can make but this target does not run is not part of "all" here.
	builds, err := selectBuilds(cat, packing, args, all)
	if err != nil {
		return err
	}

	// Group the requested images by the task definition carrying them, so each
	// one gets a single revision and a single rollout however many of its
	// containers changed.
	type work struct {
		family  string
		service string
		swaps   []deploy.Swap
	}
	var order []string
	byFamily := map[string]*work{}
	var refs []registry.Ref

	for _, e := range builds {
		svc, ok := packing.ServiceFor(e.Build.Container)
		if !ok {
			return fmt.Errorf("build %q wants a container named %q, and target %s does not run one — "+
				"its scope has %s",
				e.Build.Name, e.Build.Container, target.Name, strings.Join(packing.Containers(), ", "))
		}
		w, seen := byFamily[svc.Family]
		if !seen {
			w = &work{family: svc.Family, service: svc.Name}
			byFamily[svc.Family] = w
			order = append(order, svc.Family)
		}
		// The repository is the build's own declaration, never composed from
		// the project and build names.
		refs = append(refs, registry.Ref{Name: e.Build.Name, Repo: e.Repository, Tag: tag})
		w.swaps = append(w.swaps, deploy.Swap{
			Container: e.Build.Container,
			Image:     cfg.Registry.ImageURI(e.Repository, tag),
		})
	}

	if skipCheck {
		fmt.Fprintf(os.Stderr, "\nWARNING: --skip-image-check — promoting without checking the registry\n")
	} else if err := preflight(ctx, cfg, refs); err != nil {
		return err
	}

	for _, family := range order {
		w := byFamily[family]
		current, err := client.CurrentImages(ctx, family)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\n%s on %s (%s)\n", family, target.Cluster, target.Name)
		for _, s := range w.swaps {
			was := current[s.Container]
			switch {
			case was == s.Image:
				fmt.Fprintf(os.Stderr, "  %s  %s (unchanged)\n", s.Container, shortImage(s.Image))
			default:
				fmt.Fprintf(os.Stderr, "  %s  %s → %s\n", s.Container, shortImage(was), shortImage(s.Image))
			}
		}
		// A "from" side still on the placeholder means the registered revision is the
		// one Terraform wrote and no real image was ever promoted onto it — Terraform
		// owns the task definition shape and never the image, so it seeds a tag it cannot fill.
		// Worth saying, because it is exactly the state where a deploy is most needed
		// and where the newest revision must not be pointed at directly.
		if fromPlaceholder(current, w.swaps) {
			fmt.Fprintf(os.Stderr, "  first real image since a Terraform apply (was on the %s placeholder)\n",
				placeholderTag)
		}

		// Containers nobody asked to change still restart: a revision is
		// registered for the whole task definition and ECS replaces the running
		// tasks, not one container. Worth saying before it happens rather than after.
		if mates := untouched(packing, family, w.swaps); len(mates) > 0 {
			fmt.Fprintf(os.Stderr, "  also restarts (same task definition): %s\n", strings.Join(mates, ", "))
		}

		// A container in the running revision and not in the one being
		// registered is not restarting — it is going. That is what a Terraform
		// repacking looks like the moment a deploy finally carries it, and it
		// deserves louder billing than the restart line above.
		if svc, ok := packing.ServiceWithFamily(family); ok {
			if leaving := svc.Leaving(); len(leaving) > 0 {
				fmt.Fprintf(os.Stderr, "  REMOVED from this task definition (Terraform dropped it): %s\n",
					strings.Join(leaving, ", "))
			}
			if svc.Behind() {
				fmt.Fprintf(os.Stderr, "  service is on %s, deploying from %s\n",
					revision(svc.Running), revision(svc.Deployable))
			}
		}

		if dryRun {
			continue
		}

		arn, err := client.Promote(ctx, family, w.service, w.swaps)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  registered %s\n", arn[strings.LastIndex(arn, "/")+1:])

		if noFollow {
			fmt.Fprintf(os.Stderr, "  not following (--no-follow)\n")
			continue
		}

		start := time.Now()
		err = client.Follow(ctx, w.service, arn, deploy.FollowOptions{
			Timeout:    timeout,
			OnProgress: func(p deploy.Progress) { fmt.Fprintf(os.Stderr, "  %s\n", p) },
		})
		if err != nil {
			return fmt.Errorf("%s: %w", family, err)
		}
		fmt.Fprintf(os.Stderr, "  deployed in %s\n", time.Since(start).Round(time.Second))
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "\nDry run — nothing was changed.\n")
	}
	return nil
}

// selectBuilds picks which of the repo's builds to deploy.
//
// It deliberately mirrors the build selection rather than sharing it: a build
// whose Dockerfile is missing here may still be running on the cluster, and
// deploying it is legitimate.
//
// --all means every build THIS TARGET RUNS, not every build the repository
// declares. On a cluster mid-migration those differ in both directions, and a
// repo-wide --all used to fail partway through a multi-service deploy with a
// per-container error, after earlier services had already rolled.
func selectBuilds(cat *catalog.Catalog, p *deploy.Packing, names []string, all bool) ([]catalog.Entry, error) {
	if all && len(names) > 0 {
		return nil, fmt.Errorf("--all cannot be combined with a build name")
	}
	if !all && len(names) == 0 {
		return nil, fmt.Errorf("no build named (name one or more builds, or pass --all)")
	}

	if all {
		var out []catalog.Entry
		for _, e := range cat.Entries {
			if e.Status == catalog.Disabled {
				continue
			}
			if _, ok := p.ServiceFor(e.Build.Container); !ok {
				continue
			}
			out = append(out, e)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("nothing to deploy: no build this repo declares runs on this target "+
				"(it runs %s)", strings.Join(p.Containers(), ", "))
		}
		return out, nil
	}

	var out []catalog.Entry
	var unknown []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		e, ok := cat.Find(n)
		if !ok {
			unknown = append(unknown, n)
			continue
		}
		out = append(out, e)
	}
	if len(unknown) > 0 {
		var known []string
		for _, e := range cat.Entries {
			known = append(known, e.Build.Name)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("unknown: %s (known: %s)",
			strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	return out, nil
}

// untouched lists containers in a task definition that nobody asked to change.
func untouched(p *deploy.Packing, family string, swaps []deploy.Swap) []string {
	changing := map[string]bool{}
	for _, s := range swaps {
		changing[s.Container] = true
	}
	var out []string
	for _, s := range p.Services {
		if s.Family != family {
			continue
		}
		for _, c := range s.Containers {
			if !changing[c] {
				out = append(out, c)
			}
		}
	}
	sort.Strings(out)
	return out
}

// revision trims a task definition ARN to family:number, which is what anybody
// reading a rollout actually says out loud.
func revision(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

// shortImage trims the registry host, which is the same for every line and the
// least interesting part of a long reference.
func shortImage(image string) string {
	if image == "" {
		return "(none)"
	}
	if i := strings.LastIndex(image, "/"); i >= 0 {
		return image[i+1:]
	}
	return image
}

// preflight asks the registry about every image this deploy would promote, and
// refuses the deploy if any of them cannot be promoted safely.
//
// Before any mutation, deliberately: no task definition registered, no service
// updated, nothing to clean up afterwards. A dry run checks too — a dry run
// that reports a promotion which cannot happen is worse than none, because
// gaining confidence is the only reason to run one.
//
// The registry is usually a different account from the cluster, so this opens
// its own session. Failing to open it is an Unknown rather than a refusal: a
// missing read permission must not be able to make a working deploy impossible.
func preflight(ctx context.Context, cfg *config.Config, refs []registry.Ref) error {
	sess, err := awsx.Open(ctx, cfg.Registry.Profile, cfg.Registry.Region, cfg.Registry.Account)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nWARNING: cannot reach the registry to check these images (%v)\n", err)
		fmt.Fprintf(os.Stderr, "         continuing without the check\n")

		return nil
	}

	reg := registry.New(sess)
	where := fmt.Sprintf("%s, %s", cfg.Registry.Account, cfg.Registry.Region)

	warnings, problem := reviewFindings(
		reg.Preflight(ctx, refs),
		where,
		func(repo string) string { return suggestTag(ctx, reg, repo) },
	)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "\nWARNING: %s\n", w)
	}

	return problem
}

// reviewFindings decides whether a deploy may go ahead, and says why not.
//
// Pure, and the rendering is here rather than at the call site, because the
// message IS the feature: an operator who forgot to push needs to be told what
// is missing, where it was looked for, and what to run. `suggest` is passed in
// so that naming an alternative tag stays a registry call the caller owns.
func reviewFindings(
	findings []registry.Finding,
	where string,
	suggest func(repo string) string,
) (warnings []string, err error) {
	var missing, ambiguous []registry.Finding

	for _, f := range findings {
		switch f.Status {
		case registry.Missing:
			missing = append(missing, f)
		case registry.Ambiguous:
			ambiguous = append(ambiguous, f)
		case registry.Unknown:
			warnings = append(warnings, fmt.Sprintf("could not check %s:%s — %s", f.Repo, f.Tag, f.Why))
		}
	}

	if len(missing) == 0 && len(ambiguous) == 0 {
		return warnings, nil
	}

	var b strings.Builder

	if len(missing) > 0 {
		fmt.Fprintf(&b, "%s not in ECR (%s):\n", plural(len(missing), "image is", "images are"), where)
		for _, f := range missing {
			fmt.Fprintf(&b, "\n  %s:%s", f.Repo, f.Tag)
			if f.Why != "" {
				fmt.Fprintf(&b, "\n      %s", f.Why)
			}
		}

		names := make([]string, 0, len(missing))
		for _, f := range missing {
			names = append(names, f.Name)
		}
		fmt.Fprintf(&b, "\n\n  build it first:\n      meimei build %s\n",
			strings.Join(names, " "))

		if alt := suggest(missing[0].Repo); alt != "" {
			fmt.Fprintf(&b, "\n  or promote a tag that is there:\n      --tag %s\n", alt)
		}
	}

	for _, f := range ambiguous {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s:%s is one of several tags on the same image (also: %s)\n",
			f.Repo, f.Tag, strings.Join(f.Aliases, ", "))
		b.WriteString("\n  meimei pushes one tag per build, so it cannot tell which of those names this\n" +
			"  image was built as — and the one it was built as is what the code inside it\n" +
			"  reports about itself. Promote the tag the build produced, or pass\n" +
			"  --skip-image-check if you know this is it.\n")
	}

	b.WriteString("\nnothing was changed.")

	return warnings, errors.New(b.String())
}

// suggestTag names a tag that does exist, for somebody who just asked for one
// that does not. A hint, so failing to fetch it is not worth reporting on top of
// the failure it is decorating.
func suggestTag(ctx context.Context, reg *registry.ECR, repo string) string {
	recent, err := reg.Recent(ctx, repo, 1)
	if err != nil || len(recent) == 0 || len(recent[0].Tags) == 0 {
		return ""
	}

	return fmt.Sprintf("%s      (pushed %s)",
		recent[0].Tags[0], recent[0].PushedAt.Local().Format("2006-01-02 15:04"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return fmt.Sprintf("%d %s", n, many)
}

// placeholderTag is the image tag Terraform seeds into a task definition it
// cannot fill: it owns the shape of a task and never the image in it, so the
// first apply for a task definition names something unpullable on purpose.
//
// A convention rather than a fact meimei can discover, and it is the same one in
// every cluster we run. A tag it does not recognise is simply not reported on.
const placeholderTag = "bootstrap"

// fromPlaceholder reports whether any container being changed is currently on
// the placeholder.
func fromPlaceholder(current map[string]string, swaps []deploy.Swap) bool {
	for _, s := range swaps {
		if strings.HasSuffix(current[s.Container], ":"+placeholderTag) {
			return true
		}
	}

	return false
}
