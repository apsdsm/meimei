# Decisions

Why meimei is shaped the way it is. Each entry states the decision and the reason. Entries are kept
because the reason is what stops a change from undoing one by accident.

Entries with no date predate this record.

## AWS names are declared, never computed

**Decided 2026-08-25.**

Every identifier meimei sends to AWS is an explicit field. Where meimei does not know a name it makes
no move rather than guessing one.

| Name | Where it comes from |
|---|---|
| ECR repository | `[[builds]].repository` |
| container definition | `[[builds]].container` |
| ECS cluster | `[[targets]].cluster` |
| ECS services in scope | `[[targets]].services` |
| task definition family | read from the service's task definition ARN |

A name computed from parts is a footgun. It cannot be used on infrastructure that does not happen to
match the convention, and an error about it has to explain a derivation instead of naming a string.
The composed form this replaced was `project.name + "-" + build.name`, implemented in two places
that did not call each other, with the container name taken to be the build's name.

`container` is required rather than defaulted to the build's name. A default is a derivation with a
friendlier face, and it would put the footgun back for exactly the projects that do not follow the
convention.

**Composing AWS's own format is a different thing and is fine.** The registry host
`<account>.dkr.ecr.<region>.amazonaws.com` has exactly one correct value for an account and region,
and nobody chose it. Composing someone's naming convention is what this rule forbids.

**Where AWS can be asked, ask.** That is neither computing nor declaring, and it is the best of the
three. The task definition family is read out of the service's task definition ARN.

**Nothing matches by prefix or substring**, and nothing may start. Repository and container
resolution are exact, and there is no `HasPrefix` or `Contains` on any AWS identifier. A build called
`foo` cannot reach `foobar`'s repository or task in either direction.

## A target is a cluster plus a scope inside it

**Decided 2026-08-25.**

`[[targets]].services` names the ECS services a target addresses. It is the one fact about a deploy
that cannot be discovered: ECS has no notion of an environment, so nothing on the cluster says which
of its services are production and which are staging.

The need was hit on `nova_chatbot`. `nova-public1` is one `t4g.small`; on ECS/EC2 the cost is the
instance rather than the task, so a staging service beside production is free. Both services carry a
container called `chatbot`, deliberately — staging exists to run the task definition production will
run, and naming its container for its environment would make the two differ in a field that has
nothing to do with the environment.

Without a scope, resolution is a flat container-name-to-service map over the whole cluster. A
collision in that map has no correct answer, so it is a hard error naming both services. The
alternative that was rejected is picking one: an unscoped map that overwrites silently rolls
whichever service sorts last, follows it to a healthy rollout and reports success, leaving the other
environment untouched and the operator told the deploy worked.

Three alternatives were rejected:

- **A per-build map of build name to ECS service.** Duplicates the packing decision, which must be
  read from the cluster, and breaks the first time Terraform repacks.
- **A suffix or name pattern per target.** Reintroduces prefix matching on the deploy path.
- **Discovering scope from ECS resource tags.** Genuinely discoverable and worth revisiting, but it
  requires every service to be tagged and a `ListTagsForResource` per service.

`services` is a list because an environment is often packed across several ECS services. acme dev1 is
five builds on two task definitions, so that one instance needs about two task-ENIs. One target
covers both. Omitted means the whole cluster, which is correct for a cluster carrying one
environment.

## Every build pushes

**Decided 2026-08-25.**

There is no point producing a build and holding onto it: the push is the end point of a build, not a
separate act. One `.meimei.toml` is one registry, so there is only one place a build can go.
`--no-push` is a diagnostic that answers whether the thing builds at all.

This replaced an earlier rule that nothing left the machine unless a destination was named. The
property that rule protected — no accidental push into an immutable-tag repository — is carried
instead by the dirty-tree refusal, by the existing-tag skip, and by the registry account assertion.

## A flag names a choice; a fact belongs in config

**Decided 2026-08-25.**

The rule for adding an option: a flag names a choice that varies per invocation. A fact that does not
vary belongs in the config, and a fact that varies per target belongs on the target.

Applying it removed three flags. `--platform` went because the config declares the platform and
`--no-push` now builds for it too. `--timeout` became `[[targets]].timeout`, because a slow
production service wants a different value from dev1 and that does not change between two deploys to
the same place. `ls --label` went because a named tag would just be echoed back in every row.

`--skip-image-check` and `--force` look droppable and are not. `--skip-image-check` is the only way
past the ambiguous-tag refusal, which the refusal message names. `--force` is needed because
`git status --porcelain` counts untracked files, so an untracked `.env` would otherwise block a build
of a perfectly good commit.

## The subject is positional, everything else is a flag

`meimei build api user-web --tag rel.001`. No magic positional order, and no reserved positional
values: `--all`, not a build literally named `all`.

The shell scripts this replaces take `<service> <tag> <target>` — three bare strings whose order you
must remember, two of which look alike. Swapping the last two is caught by nothing.

A second bare positional for the target was considered and rejected on 2026-08-25. It could be made
unambiguous, since build names and target names are both closed sets read from config, but it is
still an order to remember.

## The vocabulary follows AWS

**Decided 2026-08-21.**

Three of meimei's nouns meant something different in AWS, inherited from the CodeDeploy tool that
preceded it. A `Task` struct was a service plus a family plus a container list; a `Service` was a
buildable image; a `Target` was a cluster wearing CodeDeploy's word for the thing being deployed to.

The rule now is that if AWS has a name for the thing, use AWS's name, and invent a word only where
AWS has no object to name. [terms.md](terms.md) is the list.

`[[builds]]` rather than `[[images]]`, decided 2026-08-25: a config entry holds a Dockerfile, a
context and a platform, which are inputs to a build rather than properties of an image, and one entry
produces many images over its life. That frees *image* to mean what AWS means by it.

`[[targets]]` kept its name rather than becoming `[[clusters]]`, because a target is a cluster plus a
scope inside it and two targets can point at one cluster. The collision with ELB's *target* and
*target group* is accepted: no name for this concept avoids every collision.

## Config format versions are an integer

**Decided 2026-08-24.**

`.meimei.toml` carries a `version` key, checked before anything else is read. A binary reads exactly
one version and refuses every other by name, in both directions: an older file needs editing, a newer
one needs a newer meimei.

An integer rather than sniffing for a known-old key, because sniffing only recognises renames already
made. It cannot see a breaking change that *adds* a required key — `toml.Unmarshal` ignores what it
does not know, so an old file missing a new key looks exactly like a new file whose author left it
out.

Config versions are independent of the binary version. Version 2 is released, so the next breaking
change to the file format is version 3.

## AWS through the SDK, docker through the CLI

AWS is reached with aws-sdk-go-v2 rather than the `aws` CLI. The deciding case is
`RegisterTaskDefinition`, whose input type is itself the whitelist of registerable fields.

docker is shelled out to rather than driving buildkit directly, because buildx resolves the
Dockerfile frontend, and handles auth, exporters and multi-platform manifests.

## Resolving what to build is pure

`internal/build` is split so that `plan.go` decides what to build with no I/O and no side effects,
and only `run.go` touches docker. That makes `--dry-run` free and puts the rules that have caused
trouble under test. Anything needing the daemon, the network or the clock is passed in through
`Options`.

`NewPacking` in `internal/deploy` is separate from `Discover` for the same reason: the rule that has
caused trouble is what happens when two services carry the same container name, and it belongs under
test without an ECS client.

## Every build is told its own tag

`--build-arg MEIMEI_BUILD_ID=<tag>` goes to every build, carrying the same string the image is tagged
with.

It is the OCI `version` label's fact handed to the build instead of attached to the result. A label
describes an image to whoever inspects it from outside and cannot be read by the code inside it, so
an app that wants to report which build it is has to be told while it is being built. A value the
container set at start-up would describe the deployment rather than identify the artefact.

The case it was added for: a browser SPA bakes it into its bundle and sends it back, so a server can
tell a client running a retired build from one running the deployed build.

It is passed unconditionally. A Dockerfile that does not declare the `ARG` ignores it, and buildx
warning about an unused build argument is not a failure.

## `sha-` is reserved for generated tags

A tag meimei generates from a commit is `sha-<short sha>`. ECR's lifecycle policy expires all but the
most recent images carrying that prefix, so a release tag in the same namespace would be swept some
builds later, taking away an image a live task definition still points at. A `--tag` starting with
`sha-` is therefore refused rather than discouraged.
