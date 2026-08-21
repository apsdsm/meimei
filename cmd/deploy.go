package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/apsdsm/meimei/internal/awsx"
	"github.com/apsdsm/meimei/internal/catalog"
	"github.com/apsdsm/meimei/internal/deploy"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [service...]",
	Short: "Promote images onto a target's ECS cluster",
	Long: "Point a target's ECS services at a different image.\n\n" +
		"A deploy copies the task definition Terraform registered, swaps the image on the\n" +
		"named containers, registers the result and rolls the service. It never changes\n" +
		"anything else about the task — Terraform owns the shape.\n\n" +
		"Which services are packed into which task is read from the cluster, never from\n" +
		"config: a service's name is its task family and its container names are the\n" +
		"services it carries. Services sharing a task are promoted in ONE revision and\n" +
		"ONE rollout, which is also the only thing that works on a cluster brought up\n" +
		"fresh, where no container can start until every image in its task is real.",
	Example: "  meimei deploy manualbot --tag sha-eba96de\n" +
		"  meimei deploy --all --tag jjc2.2026_010.001 --to dev1\n" +
		"  meimei deploy api --tag sha-abc1234 --to dev1 --dry-run",
	RunE: runDeploy,
}

func init() {
	deployCmd.Flags().Bool("all", false, "Deploy every service this repo declares")
	deployCmd.Flags().String("tag", "", "Image tag to promote (default: the current commit)")
	deployCmd.Flags().String("to", "", "Target to deploy to (optional when the project declares one)")
	deployCmd.Flags().Bool("no-follow", false, "Trigger the rollout and return without waiting")
	deployCmd.Flags().Bool("dry-run", false, "Print what would be promoted, and change nothing")
	deployCmd.Flags().Duration("timeout", deploy.DefaultTimeout, "How long to follow a rollout before giving up")
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
	to, _ := cmd.Flags().GetString("to")
	noFollow, _ := cmd.Flags().GetBool("no-follow")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	target, err := cfg.ResolveTarget(to)
	if err != nil {
		return err
	}

	cat := catalog.Load(cfg, "")
	services, err := selectServices(cat, args, all)
	if err != nil {
		return err
	}

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
	client := deploy.New(sess, target.Cluster)

	packing, err := client.Discover(ctx)
	if err != nil {
		return err
	}

	// Group the requested services by the task carrying them, so each task gets
	// one revision and one rollout however many of its containers changed.
	type work struct {
		family string
		swaps  []deploy.Swap
	}
	var order []string
	byFamily := map[string]*work{}

	for _, name := range services {
		task, ok := packing.TaskFor(name)
		if !ok {
			return fmt.Errorf("no container %q on cluster %s — it runs %s",
				name, target.Cluster, strings.Join(packing.Containers(), ", "))
		}
		w, seen := byFamily[task.Service]
		if !seen {
			w = &work{family: task.Service}
			byFamily[task.Service] = w
			order = append(order, task.Service)
		}
		w.swaps = append(w.swaps, deploy.Swap{
			Container: name,
			Image:     cfg.Registry.ImageURI(cfg.Project.Name+"-"+name, tag),
		})
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
		// Task-mates that nobody asked to change still restart: a revision is
		// registered for the whole task and ECS replaces the task, not one
		// container. Worth saying before it happens rather than after.
		if mates := untouched(packing, family, w.swaps); len(mates) > 0 {
			fmt.Fprintf(os.Stderr, "  also restarts (shared task): %s\n", strings.Join(mates, ", "))
		}

		// A container in the running revision and not in the one being
		// registered is not restarting — it is going. That is what a Terraform
		// repacking looks like the moment a deploy finally carries it, and it
		// deserves louder billing than the restart line above.
		if task, ok := packing.TaskNamed(family); ok {
			if leaving := task.Leaving(); len(leaving) > 0 {
				fmt.Fprintf(os.Stderr, "  REMOVED from this task (Terraform dropped it): %s\n",
					strings.Join(leaving, ", "))
			}
			if task.Behind() {
				fmt.Fprintf(os.Stderr, "  service is on %s, deploying from %s\n",
					revision(task.Running), revision(task.Deployable))
			}
		}

		if dryRun {
			continue
		}

		arn, err := client.Promote(ctx, family, w.swaps)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  registered %s\n", arn[strings.LastIndex(arn, "/")+1:])

		if noFollow {
			fmt.Fprintf(os.Stderr, "  not following (--no-follow)\n")
			continue
		}

		start := time.Now()
		err = client.Follow(ctx, family, arn, deploy.FollowOptions{
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

// selectServices picks which of the repo's services to deploy. It deliberately
// mirrors the build selection rather than sharing it: deploying is about what
// this repo declares, and a service with no Dockerfile here may still be
// running on the cluster.
func selectServices(cat *catalog.Catalog, names []string, all bool) ([]string, error) {
	if all && len(names) > 0 {
		return nil, fmt.Errorf("--all cannot be combined with a service name")
	}
	if !all && len(names) == 0 {
		return nil, fmt.Errorf("no service named (name one or more services, or pass --all)")
	}

	if all {
		var out []string
		for _, e := range cat.Entries {
			if e.Status != catalog.Disabled {
				out = append(out, e.Service.Name)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("nothing to deploy: every service is disabled")
		}
		return out, nil
	}

	var out, unknown []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if _, ok := cat.Find(n); !ok {
			unknown = append(unknown, n)
			continue
		}
		out = append(out, n)
	}
	if len(unknown) > 0 {
		var known []string
		for _, e := range cat.Entries {
			known = append(known, e.Service.Name)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("unknown: %s (known: %s)",
			strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	return out, nil
}

// untouched lists containers in a task that nobody asked to change.
func untouched(p *deploy.Packing, family string, swaps []deploy.Swap) []string {
	changing := map[string]bool{}
	for _, s := range swaps {
		changing[s.Container] = true
	}
	var out []string
	for _, t := range p.Tasks {
		if t.Service != family {
			continue
		}
		for _, c := range t.Containers {
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
