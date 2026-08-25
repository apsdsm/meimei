# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

meimei is a build and deploy CLI for containerised projects, written in Go. Run it inside a project
and it reads `.meimei.toml` to learn what that repository can build. It builds container images,
pushes them to ECR, and promotes them onto ECS.

Module path: `github.com/apsdsm/meimei`. Config format: version 2.

Commands: `meimei ls`, `meimei build`, `meimei deploy`. Still to build: `ls` subjects (`builds`,
`images`, `targets`, `services`), the TUI, and notifications.

`docs/index.md` indexes every page. `docs/decisions.md` records why the design is what it is;
`docs/deploys.md` describes what the two commands do and the AWS behaviour they encode. Read both
before changing the build or deploy path.

`v0.1.0` was a different tool — CodeDeploy, S3 bundles, a DynamoDB build catalogue, `.meimei.yaml`.
Its code is gone. "v2" in branch names is the name of that rewrite, not a module major version.

## Rules that shape the code

**Declare AWS names, never compute them.** Every identifier meimei sends to AWS is an explicit
field: the ECR repository is `[[builds]].repository`, the container definition is
`[[builds]].container`, the ECS services a target addresses are `[[targets]].services`. Where meimei
does not know a name it makes no move rather than guessing one. A name computed from parts is a
footgun — it rules out infrastructure that does not match the convention, and an error about it has
to explain a derivation instead of naming a string.

`[[builds]].container` is required rather than defaulting to the build's name. A default is a
derivation with a friendlier face, and it would put the footgun back for exactly the projects that
do not follow the convention.

Composing AWS's own **format** is a different thing and is fine: the registry host
`<account>.dkr.ecr.<region>.amazonaws.com` has one correct value and nobody chose it. Composing
someone's naming **convention** is what this rule forbids.

**Where AWS can be asked, ask.** That is neither computing nor declaring, and it is the best of the
three. The task definition family is read out of the service's task definition ARN.

**Nothing matches by prefix or substring.** Repository and container resolution are exact map keys,
and there is no `HasPrefix` or `Contains` on any AWS identifier. A build called `foo` must not be
able to reach `foobar`'s repository or task in either direction. Any new resolution keeps that.

**Where a fact lives.** `.meimei.toml` declares only what is true of the repository, the same for
every environment. Anything that varies per target and can be asked of AWS is read when it is
needed. Which ECS task definition a build is packed into is the case that set the rule: it is a
per-cluster Terraform decision made for that cluster's instance and ENI budget, so a copy in config
would be a global mirror of a per-target fact and would drift the first time production packed
differently from dev1.

The test to apply to a new field is not "does it vary per target" but **"could AWS be asked"**.
`[[targets]].services` is the exception that passes it: ECS has no notion of an environment, so
nothing on the cluster says which of its services are production and which are staging. It is a
scope, not an instruction — naming three services does not make a deploy roll three, it makes those
three the only ones a build may resolve against.

If a new field describes how something is *deployed*, it probably belongs in neither the config nor
meimei. Check whether Terraform already owns it and whether the AWS API can be asked.

**A flag names a choice; a fact belongs in config.** A flag names a choice that varies per
invocation. A fact that does not vary belongs in the config, and one that varies per target belongs
on the target — which is why the rollout timeout is `[[targets]].timeout` and not a flag.

**The subject is positional, everything else is a flag.** `meimei build api user-web --tag rel.001`.
No magic positional order, and no reserved positional values: `--all`, not a build literally named
`all`. The shell scripts this replaces take `<service> <tag> <target>`, three bare strings whose
order you must remember and two of which look alike. Keep new options as flags even when a
positional would be shorter — the call site is also the shell history someone reads back later.

**Every build pushes.** There is no point producing a build and holding onto it, and one
`.meimei.toml` is one registry. `--no-push` is a diagnostic that answers whether the thing builds.
No accidental push is possible: a dirty tree is refused, an existing tag is skipped, and the
registry account is asserted against the live caller identity first.

**`Build.Group` is presentational only** (`services/{api,web,worker}/`) and carries no deployment
meaning.

## Implementation constraints

AWS is reached through **aws-sdk-go-v2, not the `aws` CLI**, even though docker is shelled out to.
The deciding case is `RegisterTaskDefinition`: its input type IS the whitelist of registerable
fields, so read-only fields have nowhere to go and a registerable one cannot be dropped without the
compiler noticing. The shell scripts hand-maintain that list in jq and silently drop six fields.

meimei shells out to the `docker` CLI rather than driving buildkit directly, because buildx resolves
the Dockerfile frontend (every Dockerfile here opens `# syntax=docker/dockerfile:1`) and handles
auth, exporters and multi-platform manifests. **Success is the exit code, never parsed progress
output**, so a change in how buildx formats progress cannot fail a good build.

**Keep the pure parts pure.** `internal/build` is split so that resolving *what* to build
(`plan.go`) has no I/O and no side effects, and only `run.go` touches docker. That makes `--dry-run`
free and puts the rules that have caused trouble under test. Anything needing the daemon, the
network or the clock is passed in through `Options`, never read inside `Resolve`. `NewPacking` in
`internal/deploy` is separate from `Discover` for the same reason: the rule that has caused trouble
is what happens when two services carry the same container name, and it belongs under test without
an ECS client.

**Every build is told its own tag.** `--build-arg MEIMEI_BUILD_ID=<tag>` goes to every build,
carrying the same string the image is tagged with. It is the OCI `version` label's fact handed TO
the build instead of attached to the result: a label describes an image to whoever inspects it from
outside and cannot be read by the code inside it, so an app that wants to report which build it is
must be told while it is being built. A value the container set at start-up would describe the
deployment rather than identify the artefact. The case it was added for: a browser SPA bakes it into
its bundle and sends it back, so a server can tell a client running a retired build from one running
the deployed build.

It is passed to every build unconditionally, like the labels. A Dockerfile that does not declare the
`ARG` ignores it, and buildx warning about an unused build argument is not a failure.

The TUI is modelled on pairin's dash mode, and its grid component will be **copied** from
`pairin/internal/tui/grid.go` rather than shared — the two tools should be free to diverge.

## Development commands

```bash
go build -o meimei .    # Build binary
go run main.go          # Run directly
go install .            # Install to GOPATH/bin
go test ./...           # Run tests
```

## Versioning

The version is a `const` in `cmd/version.go`, and the tag is what the installer resolves — so the
const tracks the tag rather than leading it. When bumping:

1. Update the `Version` constant in `cmd/version.go`
2. Create a git tag matching the version (e.g. `git tag v1.0.0`)
3. Push the tag (e.g. `git push origin v1.0.0`)

**Stay on 1.x.** Go ignores `v2+` tags for a module whose path has no `/v2` suffix, so a `v2.0.0` tag
would leave `go install github.com/apsdsm/meimei@latest` on the newest 1.x — silently, with no error
to read. Going to 2.x means renaming the module and every internal import, and changing the install
command, for a CLI nothing imports as a library.

The config format version is separate and independent. A binary reads exactly one and refuses every
other by name. Once a release carries a config version, changing that shape costs a version bump.
