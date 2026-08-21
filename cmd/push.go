package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/apsdsm/meimei/internal/awsx"
	"github.com/apsdsm/meimei/internal/build"
	"github.com/apsdsm/meimei/internal/catalog"
	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/registry"
)

// pushContext is everything a push needs, resolved once.
type pushContext struct {
	registry *registry.ECR
	auth     string // DOCKER_CONFIG directory
	cleanup  func()
}

// openRegistry verifies the registry account and prepares docker credentials.
//
// The account is checked before anything is sent: repositories here have
// immutable tags, so an image pushed into the wrong account cannot be taken
// back, only worked around.
func openRegistry(ctx context.Context, cfg *config.Config) (*pushContext, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("no [registry] in %s — pushing needs an account to push to", cfg.Path)
	}

	sess, err := awsx.Open(ctx, cfg.Registry.Profile, cfg.Registry.Region, cfg.Registry.Account)
	if err != nil {
		return nil, err
	}
	reg := registry.New(sess)

	auth, err := reg.Login(ctx)
	if err != nil {
		return nil, err
	}
	dir, cleanup, err := build.WriteAuth(auth.Server, auth.Username, auth.Password)
	if err != nil {
		return nil, err
	}
	return &pushContext{registry: reg, auth: dir, cleanup: cleanup}, nil
}

// runPush builds and pushes each plan, skipping tags that are already there.
func runPush(ctx context.Context, pc *pushContext, cat *catalog.Catalog, plans []build.Plan) error {
	runner := build.Runner{Stdout: os.Stdout, Stderr: os.Stderr, ConfigDir: pc.auth}

	for i, p := range plans {
		exists, err := pc.registry.Exists(ctx, p.Repository, p.Tag)
		if err != nil {
			return err
		}
		if exists {
			// Not an error: tags are immutable, so a tag that is already there
			// is a build that already finished. Re-running the same command
			// should be quiet, not fatal.
			fmt.Fprintf(os.Stderr, "[%d/%d] %s — %s already pushed, skipping\n",
				i+1, len(plans), p.Service, p.Image)
			continue
		}

		fmt.Fprintf(os.Stderr, "\n[%d/%d] %s → %s (%s, push via %s)\n",
			i+1, len(plans), p.Service, p.Image, p.Platform, p.Route)

		if err := runner.Run(ctx, p); err != nil {
			return err
		}
		if p.Route == build.RouteCLI {
			if err := runner.Push(ctx, p.Image); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "pushed %s\n", p.Image)
	}
	return nil
}
