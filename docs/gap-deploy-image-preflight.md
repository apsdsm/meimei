# Gap: `deploy` rolls a tag without checking the image exists

**Status:** not implemented. Observed against a live cluster on 2026-08-20, twice.

`meimei deploy` registers a task definition and rolls a service without ever asking the registry
whether the image it names is there. When it is not, the failure arrives about two minutes later as a
task that will not start, and the message names neither the tag nor the cause in terms a reader
connects to "you forgot to build".

## What it looks like

```
$ meimei deploy manualbot
no --tag given, using this checkout: sha-7c624c5

tc-public1-manualbot on tc-public1 (public1)
  manualbot  tc-manualbot:bootstrap → tc-manualbot:sha-7c624c5
  registered tc-public1-manualbot:19
  [00:00] running 0/0  pending 0  failed 0  state=IN_PROGRESS
  [00:10] running 0/0  pending 0  failed 0  state=IN_PROGRESS
  ...
  [02:02] running 0/1  pending 1  failed 1  state=IN_PROGRESS
```

The task's real reason, which the operator has to go and find:

```
stoppedReason: Task failed to start
manualbot: CannotPullImageManifestError: Error response from daemon:
           manifest unknown: Requested image not found
```

## Why it happens, and why it will keep happening

Three design choices intersect:

1. **`deploy` defaults the tag to the current checkout** — `sha-<gitsha>` — which is the right
   default and the reason the mistake is easy. The tag always *looks* plausible.
2. **`deploy` does not build.** Correct: promoting and building are separate verbs, and a deploy that
   silently built would be a surprising side effect on a production path.
3. **ECR tags here are immutable.** So `build --push` is a *separate act* somebody has to remember
   between committing and deploying.

So the natural sequence — commit, deploy — is broken by default, and the tag being derived from the
commit makes it look like it should work.

It is not hypothetical. Both occurrences in one afternoon were the same shape: work committed, image
not pushed, `meimei deploy` run, two minutes of a rollout that could never succeed. In both cases
the previous revision kept serving, so nothing broke; the cost was time and a confusing error.

## The fix

**Before registering anything, ask the registry whether every image to be promoted exists.** If any
does not, print what is missing and what to run, and change nothing.

```
$ meimei deploy manualbot
no --tag given, using this checkout: sha-7c624c5

error: tc-manualbot:sha-7c624c5 is not in ECR (629585638685, ap-northeast-1)

  build and push it first:
      meimei build manualbot --push

  or promote a tag that exists:
      meimei deploy manualbot --tag sha-a84bc2f      (pushed 2026-08-20 05:37)

nothing was changed.
```

The inverse check already exists. `build --push` calls `DescribeImages` to skip a tag that is
already present, so re-runs are idempotent — the same call, read the other way round, is this
feature. `internal/registry/ecr.go` is where it lives.

### Requirements

- **Check before any mutation.** No `RegisterTaskDefinition`, no `UpdateService`. A failed preflight
  must leave the cluster exactly as it was, including no orphan task-definition revision.
- **Check every image in the roll**, not the first. `--all`, and packed tasks where one revision
  carries several containers, mean a deploy can name several tags; report *all* the missing ones in
  one go rather than making the operator discover them one deploy at a time.
- **`--dry-run` must check too.** A dry run that reports a promotion which cannot happen is worse
  than no dry run, because it is the thing an operator reaches for to gain confidence.
- **Name the registry**, account and region included. "not in ECR" is ambiguous in a repo whose
  images live in a different account from its cluster.
- **Suggest a real alternative.** The most recent existing tag is a cheap and genuinely useful
  suggestion — `DescribeImages` already returns `imagePushedAt`.
- **Do not auto-build.** Tempting, and wrong: `deploy` acquiring a side effect that pushes an
  immutable artefact to a shared registry is a surprise on a production path. Tell the operator what
  to run.
- **Allow an escape hatch** — `--skip-image-check` or similar — for the case where the image is
  pushed by something meimei cannot see, and for not making a cross-account permission problem
  undeployable. It should be loud in the output.

### Where it goes

| File | Change |
|---|---|
| `internal/registry/ecr.go` | an `ImageExists(ctx, repo, tag) (bool, time.Time, error)` and a `LatestTags(ctx, repo, n)` for the suggestion |
| `internal/deploy/deploy.go` | preflight over the resolved container→tag set, before registering |
| `cmd/deploy.go` | the flag, and rendering the failure |

### Tests worth having

- Missing tag → no AWS mutation calls made at all (assert against a fake, not by inspecting output)
- Several missing tags → all reported in one error
- Present tag → unchanged behaviour
- `--dry-run` with a missing tag → the same error, still no mutation
- `--skip-image-check` → proceeds, and says so
- `DescribeImages` failing for a *permission* reason → treated as unknown, not as absent, so a
  read-permission gap cannot block a deploy that would otherwise work

## An adjacent gap, same root

`deploy`'s output line reads:

```
manualbot  tc-manualbot:bootstrap → tc-manualbot:sha-7c624c5
```

`:bootstrap` is the placeholder Terraform seeds into a task definition and never replaces, because
Terraform owns the shape and never the image. Seeing it as the "from" side means **the currently
registered revision is Terraform's, and no real image was ever promoted onto it.**

That is worth saying out loud, because it is precisely the state in which a deploy is most necessary
and in which the newest revision must not be pointed at directly. A one-line note in the output —
*"previous revision carries the :bootstrap placeholder, so this is the first real image since a
Terraform apply"* — turns an odd-looking line into information.

## What the tool already gets right, and should keep

Worth stating so an implementer does not regress it while adding the above:

- **It copies the current task definition and swaps only the image.** That is what preserved
  `CHAT_MODEL=global.…` across a deploy where doing it by hand lost it — pointing a service at
  Terraform's newest revision gets the env right and the image wrong, which is a subtler failure than
  this one.
- **`--dry-run` reports `(unchanged)`** when the tag already matches, instead of performing a
  pointless rollout.
- **`build --push` takes the emulated path.** On an x86 host building arm64, buildkit's `--push`
  fails after a slow successful build with `no active session for <id>`, which reads like a network
  fault. `--load` then `docker push` avoids it.
