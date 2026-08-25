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
	Use:   "build [name...]",
	Short: "Build container images and push them to the registry",
	Long: "Build one or more of this repository's builds, or every buildable one with --all,\n" +
		"and push the result to the registry.\n\n" +
		"A build ends at the registry. There is no point producing an image and keeping\n" +
		"it on the machine that made it, so pushing is not a flag — one .meimei.toml is\n" +
		"one registry, and that is where a build goes.\n\n" +
		"Images are tagged with --tag if given, otherwise with the current commit as\n" +
		"sha-<gitsha>.\n\n" +
		"--no-push builds without sending anything, for answering \"does this build at\n" +
		"all\". It builds the same image a push would have sent — the platform the config\n" +
		"declares, not the host's — and loads it into the local docker image store.",
	Example: "  meimei build api\n" +
		"  meimei build api user-web\n" +
		"  meimei build --all\n" +
		"  meimei build api --tag jjc2.2026_010.001\n" +
		"  meimei build api --no-push                   # does it build?\n" +
		"  meimei build --all --dry-run",
	RunE: runBuild,
}

func init() {
	buildCmd.Flags().Bool("all", false, "Build everything this repo declares")
	buildCmd.Flags().String("tag", "", "Tag images with this release or ticket id instead of the commit")
	buildCmd.Flags().Bool("no-push", false, "Build without pushing — a diagnostic, not a way to keep the image")
	buildCmd.Flags().Bool("force", false, "Build from a dirty working tree")
	buildCmd.Flags().Bool("dry-run", false, "Print what would be built, and build nothing")
	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	all, _ := cmd.Flags().GetBool("all")
	tag, _ := cmd.Flags().GetString("tag")
	noPush, _ := cmd.Flags().GetBool("no-push")
	force, _ := cmd.Flags().GetBool("force")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	push := !noPush

	// Checked before anything else touches the disk or the daemon: a bad tag is
	// the caller's typo, and there is no reason to make them wait for a
	// platform lookup to hear about it.
	if err := build.ValidateTag(tag); err != nil {
		return err
	}

	// A build pushes, so it needs somewhere to push to. Refused up front rather
	// than after the image is built, which would leave the caller holding an
	// artefact with nowhere to go.
	if push && cfg.Registry == nil {
		return fmt.Errorf("no [registry] in %s — every build pushes, so meimei needs an account "+
			"to push to\n       add a [registry], or pass --no-push to build without sending anything",
			cfg.Path)
	}

	ctx := cmd.Context()
	if !dryRun {
		if err := build.CheckDocker(ctx, ""); err != nil {
			return err
		}
	}

	cat := catalog.Load(cfg, tag)

	// A dirty tree makes a commit tag lie, and a build pushes, so the lie would
	// be permanent — tags are immutable, so sha-<x> would name an image that
	// does not correspond to commit x for as long as the repository keeps it.
	// Naming a tag yourself makes it a warning: the tag no longer claims to be
	// a commit.
	if cat.Git.Dirty {
		switch {
		case push && tag == "" && !force:
			return fmt.Errorf("working tree is dirty: this build would be tagged sha-%s, which matches no "+
				"commit, and the tag cannot be replaced once pushed\n"+
				"       commit first, or --tag to name it, or --force to push anyway, "+
				"or --no-push to just check it builds", cat.Git.SHA)
		case push && !force:
			fmt.Fprintf(os.Stderr, "warning: working tree is dirty — %s will not match a clean commit\n", tag)
		default:
			fmt.Fprintf(os.Stderr, "warning: working tree is dirty — the image will match no commit\n")
		}
	}

	// The platform is the config's either way. A --no-push build is a
	// diagnostic, and it is only worth running if it builds the thing a push
	// would have sent.
	opts := build.Options{
		Builds:   args,
		All:      all,
		Tag:      tag,
		Now:      time.Now(),
		Output:   build.OutputLoad,
		Platform: cfg.Project.Platform,
	}

	if push {
		opts.Output = build.OutputPush
		opts.RegistryHost = cfg.Registry.Host()
		opts.Route = build.ChooseRoute(ctx, "", cfg.Project.Platform)
	}

	plans, err := build.Resolve(cat, opts)
	if err != nil {
		return err
	}

	if dryRun {
		for _, p := range plans {
			fmt.Printf("%s\n  %s\n", p.Name, p.String())
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
		fmt.Fprintf(os.Stderr, "\nPushed %s to %s\n", plural(len(plans), "image", "images"), cfg.Registry.Host())
		return nil
	}

	runner := build.Runner{Stdout: os.Stdout, Stderr: os.Stderr}

	// Sequential and fail-fast, matching the script this replaces. Building
	// several at once is worth doing, but it needs a concurrency limit and
	// somewhere for several streams of output to go, and neither belongs in the
	// step that proves the build works at all.
	for i, p := range plans {
		fmt.Fprintf(os.Stderr, "\n[%d/%d] %s → %s (%s)\n", i+1, len(plans), p.Name, p.Image, p.Platform)
		if err := runner.Run(ctx, p); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "\nBuilt %s locally (--no-push). Nothing was sent to the registry.\n",
		plural(len(plans), "image", "images"))
	return nil
}
