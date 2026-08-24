# One image, several environments in the same cluster

**Status:** not built. The need was hit on `jjc_manualchatbot` on 2026-08-21; nothing was written
against it, because the naive shape misdeploys silently and that is worse than not having it.

`meimei deploy` cannot address two environments of the same service in one ECS cluster. It handles
many services in one cluster, and one service across many clusters, but not one service twice in one
cluster — which is what a staging environment on shared hardware is.

## What was wanted

`tc-public1` is one `t4g.small` running the chatbot. On ECS/EC2 the cost is the instance, not the
task, so a second task on that box is free — the goal was a staging service beside production:

```
cluster tc-public1
  service tc-public1-chatbot        host chat.jinjicrew.jp        (production)
  service tc-public1-chatbot-test   host test.chat.jinjicrew.jp   (staging)
```

Both pull from **one** repository, `tc-chatbot`, because the point of staging is to run the exact
image production will run. Build once, deploy that tag to staging, then promote the same digest:

```bash
meimei build chatbot --push                  # once
meimei deploy chatbot --to public1-test      # the tester sees it
meimei deploy chatbot --to public1           # the same digest, promoted
```

That is the workflow immutable tags exist for. A second build for staging gives production a
different image than staging verified, so "it worked in test" stops meaning anything.

## Why it does not work today, and why it fails badly

Three facts in `internal/deploy/deploy.go`:

1. **Resolution is a flat map from container name to task, cluster-wide.** `byContainer` is built by
   listing every service in the cluster and recording each of its container names (`:200-204`). It is
   not scoped per service or per target.
2. **A collision overwrites silently.** `p.byContainer[name] = &p.Tasks[i]` — no existence check, no
   error, no warning.
3. **Which one survives is alphabetical.** `p.Tasks` is sorted ascending by service name (`:199`)
   immediately before that loop, so the last writer wins: the alphabetically last service name.

So with both tasks carrying a container named `chatbot`:

`tc-public1-chatbot` sorts before `tc-public1-chatbot-test`, so `byContainer["chatbot"]` ends up
pointing at the **staging** task. `meimei deploy chatbot` then registers a revision of the staging
family, rolls the staging service, follows it to a healthy rollout and reports success — **and
production is never touched.** No error, no warning, and the output names a family the operator did
not ask for in a line they have no reason to read closely.

**That is the reason this is a gap and not a workaround.** Standing up the second service without a
tool change does not produce a missing feature, it produces a deploy command that silently addresses
the wrong environment.

## Why renaming the container is not the answer

`Service.Name` is one string doing three jobs, which `internal/config/config.go:104` states plainly:

> Name is the service's identity everywhere: the image repository suffix, the container name inside
> its ECS task, and what you type at the CLI.

The repository is `Project.Name + "-" + Service.Name` (`config.go:301`, `cmd/deploy.go:125`). So
giving staging a distinct container name — `chatbot-test`, which resolution *would* handle — also
gives it a distinct repository, `tc-chatbot-test`. That means a second build and a second push, which
is exactly the property the staging environment exists to avoid.

There is no field to decouple the three. That coupling is the actual gap; the silent overwrite is how
it presents.

**And the coupling is a naming problem before it is a feature gap** — see
[gap-vocabulary.md](gap-vocabulary.md). meimei has no word for the level it is missing: `Target` is
holding the place an ECS service name would go, which is why "which service does `--to` point at?"
answers "the cluster". Rename `[[services]]` to `[[images]]` and `[[targets]]` to `[[clusters]]` and
the hole names itself.

## What is already right, and must not regress

Checked while diagnosing this, because the obvious fix would be to start matching loosely:

**Nothing matches by prefix or substring.** The repository name is exact concatenation passed
straight to `RepositoryName` (`internal/registry/ecr.go:70`, `preflight.go:127`), and container
resolution is an exact map key. There is no `--family-prefix`, and no `HasPrefix` or `Contains` on any
AWS identifier — the only `HasPrefix` calls are on a Dockerfile argument and a tag label. So a service
`foo` cannot pick up `foobar`'s repository or task, in either direction.

Any fix must keep that. Resolving a target to "the service whose name starts with…" would reintroduce
precisely the class of bug this tool currently does not have, and it would do it on the deploy path.

## The fix, as proposed

Make resolution **target-aware**, so the pair `(target, container)` selects a task rather than the
container name alone.

### Requirements

1. **A target can name the ECS services it deploys to**, independently of the service's identity. The
   smallest version is one optional field on `Target` — a suffix appended to the derived ECS service
   name, or an explicit service-name template. Two targets may then point into the same cluster.
2. **The repository is unaffected.** `chatbot` deployed to `public1-test` still pulls
   `tc-chatbot:<tag>`. This is the requirement; everything else is mechanism.
3. **Container names need not be unique cluster-wide any more** — they only need to be unique within
   one target's scope. Two tasks carrying a container called `chatbot` becomes the expected shape.
4. **A genuine collision is a hard error naming both services.** Once scope is per target, a duplicate
   container name *within* that scope is unambiguously a mistake, and it must fail with both service
   names printed rather than picking one. Note this cannot simply be added ahead of requirement 1: on
   its own it would refuse the very topology being asked for.
5. **`--to` stays required when more than one target could match**, rather than defaulting to the
   first. A default target is only safe while there is one.

### Where it goes

`Target` in `internal/config/config.go` gains the field. `Packing`/`byContainer` in
`internal/deploy/deploy.go` gains the target's scope, so `ListServices` results are filtered before
the map is built rather than after. `cmd/deploy.go` already resolves the repository from
`Project.Name + name`, and should not change.

### Tests worth having

- Two services in one cluster carrying the same container name, with two targets: each `--to`
  reaches its own family, and neither reaches the other's.
- The same topology with **no** `--to`: refuses, and names both candidates.
- A duplicate container name inside one target's scope: refuses, naming both services.
- `foo` and `foobar` coexisting: `deploy foo` touches neither `foobar`'s repository nor its task. This
  is a regression test for behaviour that is already correct.
- The promotion path: build once, deploy the tag to two targets, and assert both services end up
  pointing at the **same image digest** — the property the whole feature is for.

## Until it lands

**The colliding second service has now been stood up, deliberately.** An earlier draft of this
section said that must not happen while `meimei deploy` is still in use against the cluster. The call
went the other way, and the reasoning is worth recording because it sets the priority of the fix:

`tc-public1-chatbot-stg` runs a container called `chatbot`, the same name production's carries. It was
briefly named `chatbot-stg` to keep resolution unambiguous, and that was rejected on the grounds that
it shapes infrastructure around a tool bug — staging exists to run the task definition production will
run, and a container named for its environment makes the two differ in a field that has nothing to do
with the environment. Nick's words: *"don't build in workarounds for meimei, if meimei isn't working
right we fix meimei."*

So the collision is live. `meimei deploy chatbot` against `tc-public1` will roll one of the two
services and report a healthy rollout either way. **The interim path is not meimei**: the consuming
repository deploys with `scripts/deploy.sh --service <name>`, which has no default and refuses to run
without a target. That script is a stopgap whose whole purpose is to be deleted once resolution is
target-aware, and it is documented as such in `jjc_manualchatbot/docs/container.md`.

That makes this gap the thing blocking meimei from being usable against that cluster at all, rather
than a limitation to plan around.
