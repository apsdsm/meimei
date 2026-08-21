# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

meimei is a build and deploy CLI for containerised projects, written in Go. Run it inside a
project and it reads `.meimei.toml` to learn what that repo can build.

Module path: `github.com/apsdsm/meimei`

**The `v2` branch is a rewrite.** v1 deployed zip bundles to AWS CodeDeploy, cataloguing builds
in DynamoDB; it is superseded and its code has been removed on this branch (git remembers it).
v2 builds container images, pushes them to ECR, and promotes them onto ECS — replacing the
near-identical `scripts/{build,deploy,ecs-scale}.sh` suites duplicated across `acme_main` and
`other_main`.

Landed so far: `.meimei.toml` (config), the catalog, `meimei ls`, `meimei build` (local and
`--push` to ECR), and `meimei deploy` (promote onto ECS, follow the rollout). Still to come:
the TUI, and notifications. `docs/push-and-deploy-spec.md` is the worked example the push and
deploy paths were built from — read it before changing either.

AWS is reached through **aws-sdk-go-v2, not the `aws` CLI**, even though docker is shelled out to.
The deciding case is `RegisterTaskDefinition`: its input type IS the whitelist of registerable
fields, so read-only fields have nowhere to go and a registerable one cannot be dropped without
the compiler noticing. The shell scripts hand-maintain that list in jq and silently drop six
fields.

`internal/build` is split so that resolving *what* to build (`plan.go`) is pure — no I/O, no
side effects — and only `run.go` touches docker. That is what makes `--dry-run` free and puts
the rules that have actually caused trouble under test. Keep it that way: anything needing the
daemon, the network or the clock is passed in through `Options`, never read inside `Resolve`.

meimei shells out to the `docker` CLI rather than driving buildkit directly, because buildx
resolves the Dockerfile frontend (every Dockerfile here opens `# syntax=docker/dockerfile:1`),
handles auth, exporters and multi-platform manifests. **Success is the exit code, never parsed
progress output** — so a change in how buildx formats progress can never fail a good build.

The TUI is modelled on pairin's dash mode, and its grid component will be **copied** from
`pairin/internal/tui/grid.go` rather than shared — the two tools should be free to diverge.

## Command shape: subject positional, everything else a flag

`meimei build api user-web --label rel.001` — the services are positional, every modifier is a
named flag. No magic positional order, and no reserved positional values (`--all`, not a
service literally named `all`).

The scripts this replaces take `<service> <tag> <target>`: three bare strings whose order you
must remember, two of which look alike. Swapping the last two is caught by nothing. Keep new
options as flags even when a positional would be shorter — the call site is also the shell
history someone reads back later.

A corollary that settles the push default: **nothing leaves the machine unless a destination
is named.** `meimei build` is local; pushing will require `--push` (or a `--deploy-to`, which
implies it). So there is no default to flip when push lands, and no accidental push.

## Every build is told its own tag

`--build-arg MEIMEI_BUILD_ID=<tag>` goes to every build, carrying the same string the image is
tagged with. It is the OCI `version` label's fact handed TO the build instead of attached to the
result: a label describes an image to whoever inspects it from outside and cannot be read by the
code inside it, so an app that wants to report which build it is must be told while it is being
built. A value the container could set at start-up would describe the deployment rather than
identify the artifact.

The case it was added for: a browser SPA bakes it into its bundle and sends it back, so a server can
tell a client running a retired build from one running the deployed build.

Passed to every service unconditionally, like the labels. A Dockerfile that does not declare the
`ARG` ignores it, and buildx warning about an unused build argument is not a failure — success is
the exit code.

## Where a fact lives (read before adding a config field)

`.meimei.toml` declares only what is true of the **repository**, the same for every
environment: what images it builds, and — once deploy lands — which targets exist with their
account and SSO profile (an account you cannot authenticate to is not discoverable).

Anything that varies **per target** is read from AWS at the time it is needed, never mirrored
into config. The case that forced this rule: which ECS task a service is packed into is a
per-cluster Terraform decision made for that cluster's instance and ENI budget, so a copy in
config would be a global mirror of a per-target fact and would drift the first time production
packed differently from dev1. meimei asks the cluster instead — an ECS service's name is its
task family, its container names are the services in it.

`Service.Group` is presentational only (`services/{api,web,worker}/`) and carries no deployment
meaning. If a new field describes how something is *deployed*, it probably belongs in neither
the config nor meimei — check whether Terraform already owns it and whether the AWS API can be
asked.

## Development Commands

```bash
go build -o meimei .    # Build binary
go run main.go          # Run directly
go install .            # Install to GOPATH/bin
go test ./...           # Run tests
```

## Versioning

The version is defined as a `const` in `cmd/version.go`. When bumping the version:
1. Update the `Version` constant in `cmd/version.go`
2. Create a git tag matching the version (e.g. `git tag v0.1.0`)
3. Push the tag (e.g. `git push origin v0.1.0`)
