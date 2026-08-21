package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/apsdsm/meimei/internal/build"
	"github.com/apsdsm/meimei/internal/catalog"
	"github.com/spf13/cobra"
)

// Command shape: the SUBJECT is positional, every modifier is a named flag.
//
// The scripts this replaces take `<service> <tag> <target>` — three bare
// strings in an order you have to remember, two of which look alike. Swapping
// the last two is not caught by anything: it deploys to a target named after a
// tag, or worse reaches a real target carrying a tag that means nothing there.
// A flag cannot be given in the wrong order, and it says what it is at the call
// site, which is also where anyone reading the shell history needs it.
var buildCmd = &cobra.Command{
	Use:   "build [service...]",
	Short: "Build service container images",
	Long: "Build one or more services, or every buildable service with --all.\n\n" +
		"Images are tagged with --label if given, otherwise with the current commit as\n" +
		"sha-<gitsha>.\n\n" +
		"Nothing leaves this machine unless a destination is named. Without --push the\n" +
		"image is built for this host's platform and loaded into the local docker image\n" +
		"store; with --push it is built for the platform the config declares and sent to\n" +
		"the registry.",
	Example: "  meimei build api\n" +
		"  meimei build api user-web\n" +
		"  meimei build --all\n" +
		"  meimei build api --label jjc2.2026_010.001\n" +
		"  meimei build api --platform linux/arm64      # target platform, still local\n" +
		"  meimei build --all --push\n" +
		"  meimei build --all --dry-run",
	RunE: runBuild,
}

func init() {
	buildCmd.Flags().Bool("all", false, "Build every buildable service")
	buildCmd.Flags().String("label", "", "Tag images with this release or ticket id instead of the commit")
	buildCmd.Flags().Bool("push", false, "Push to the registry after building")
	buildCmd.Flags().String("platform", "", "Build for this platform instead of the host's (local builds only)")
	buildCmd.Flags().Bool("force", false, "Build from a dirty working tree even when pushing")
	buildCmd.Flags().Bool("dry-run", false, "Print what would be built, and build nothing")
	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	all, _ := cmd.Flags().GetBool("all")
	label, _ := cmd.Flags().GetString("label")
	push, _ := cmd.Flags().GetBool("push")
	platform, _ := cmd.Flags().GetString("platform")
	force, _ := cmd.Flags().GetBool("force")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Checked before anything else touches the disk or the daemon: a bad label
	// is the caller's typo, and there is no reason to make them wait for a
	// platform lookup to hear about it.
	if err := build.ValidateLabel(label); err != nil {
		return err
	}

	ctx := cmd.Context()
	if !dryRun {
		if err := build.CheckDocker(ctx, ""); err != nil {
			return err
		}
	}

	cat := catalog.Load(cfg, label)

	// A dirty tree makes a commit tag lie. Locally that is a warning, because
	// the image stays on this machine and can be rebuilt. Pushed, the lie is
	// permanent — tags are immutable, so sha-<x> would name an image that does
	// not correspond to commit x for as long as the repository keeps it.
	if cat.Git.Dirty {
		switch {
		case push && label == "" && !force:
			return fmt.Errorf("working tree is dirty: pushing would tag an image sha-%s that matches no "+
				"commit, and the tag cannot be replaced afterwards\n"+
				"       commit first, or pass --label to name the build, or --force to push anyway", cat.Git.SHA)
		case push && !force:
			fmt.Fprintf(os.Stderr, "warning: working tree is dirty — %s will not match a clean commit\n", label)
		default:
			fmt.Fprintf(os.Stderr, "warning: working tree is dirty — the image will match no commit\n")
		}
	}

	opts := build.Options{
		Services: args,
		All:      all,
		Label:    label,
		Now:      time.Now(),
		Output:   build.OutputLoad,
		Platform: platform,
	}

	if push {
		if platform != "" {
			return fmt.Errorf("--platform cannot be used with --push: a pushed image must be built for " +
				"the platform the config declares, which is what the cluster runs")
		}
		if cfg.Registry == nil {
			return fmt.Errorf("no [registry] in %s — --push needs an account to push to", cfg.Path)
		}
		opts.Output = build.OutputPush
		opts.RegistryHost = cfg.Registry.Host()
		// A pushed image targets the config's platform, so the host's is only a
		// fallback for services that declare none.
		opts.Platform = cfg.Project.Platform
		opts.Route = build.ChooseRoute(ctx, "", cfg.Project.Platform)
	} else if opts.Platform == "" {
		opts.Platform = build.HostPlatform(ctx, "")
	}

	plans, err := build.Resolve(cat, opts)
	if err != nil {
		return err
	}

	if dryRun {
		for _, p := range plans {
			fmt.Printf("%s\n  %s\n", p.Service, p.String())
			if p.Output == build.OutputPush && p.Route == build.RouteCLI {
				fmt.Printf("  docker push %s\n", p.Image)
			}
			fmt.Println()
		}
		return nil
	}

	if push {
		pc, err := openRegistry(ctx, cfg)
		if err != nil {
			return err
		}
		defer pc.cleanup()
		if err := runPush(ctx, pc, cat, plans); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\nPushed %d image(s) to %s\n", len(plans), cfg.Registry.Host())
		return nil
	}

	runner := build.Runner{Stdout: os.Stdout, Stderr: os.Stderr}

	// Sequential and fail-fast, matching the script this replaces. Building
	// several at once is worth doing, but it needs a concurrency limit and
	// somewhere for several streams of output to go, and neither belongs in the
	// step that proves the build works at all.
	for i, p := range plans {
		fmt.Fprintf(os.Stderr, "\n[%d/%d] %s → %s (%s)\n", i+1, len(plans), p.Service, p.Image, p.Platform)
		if err := runner.Run(ctx, p); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "\nBuilt %d image(s).\n", len(plans))
	return nil
}
