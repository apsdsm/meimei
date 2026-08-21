# Push and deploy, done by hand — a spec for `meimei`

A real ECR push and ECS deploy, run manually on 2026-08-19, recorded command by command so the
`--push` and `deploy` work has a worked example rather than a description.

**Status:** the deploy described here is real and live. Everything under
[What meimei needs to do](#what-meimei-needs-to-do) is now **built** — `meimei build --push` and
`meimei deploy` implement it, and the traps below are what their tests assert. The push path has
been run against this project end to end; the deploy path has been dry-run against the live
cluster but not yet used to roll it.

## Why this project

`jjc_manualchatbot` is the simplest possible ECS target, which is what makes it a good first case:

| | jjc2 / lgc | here |
|---|---|---|
| Containers per task | several, packed | **one** |
| Accounts | shared ECR + per-target cluster | **one** |
| Deploy targets | a `TARGETS` map | **one** |

So none of `promote_task`'s packing logic is exercised, and the account-juggling collapses. What
*is* exercised is the arm64-on-x86 build, which is the trap that matters most.

## The topology it ran against

```
account   629585638685            region  ap-northeast-1     profile  tcr
ECR repo  tc-manualbot            IMMUTABLE tags, scanOnPush
cluster   tc-public1
service   tc-public1-manualbot    == the task definition family
container manualbot               port 3300, bridge mode, dynamic host port
platform  linux/arm64             t4g.small (Graviton)
```

Config was 16 lines of `.meimei.toml`. `project.name = "tc"` plus service `manualbot` gives
`tc-manualbot`, matching the repo Terraform created — the derivation needs no extra field.

## What was actually run

### 0. Make the tag honest

`meimei ls` reported `working tree is dirty, an image built now matches no commit`. Tags are
immutable, so a `sha-` tag that matches no commit is permanent. Committed first, then:

```
SHA: eba96de   →   tag sha-eba96de
```

### 1. Verify the account, then log in

Account check **before** touching the registry — a push into the wrong account is not undoable
under immutable tags.

```bash
aws sts get-caller-identity --query Account --output text     # 629585638685, matched
aws ecr get-login-password --region ap-northeast-1 \
  | docker login --username AWS --password-stdin 629585638685.dkr.ecr.ap-northeast-1.amazonaws.com
```

### 2. Skip if the tag already exists

Makes a re-run idempotent instead of an error:

```bash
aws ecr describe-images --region ap-northeast-1 --repository-name tc-manualbot \
  --image-ids imageTag=sha-eba96de   # non-zero exit = not present, proceed
```

### 3. Build arm64, and do NOT push from buildkit

```bash
docker buildx build \
  --platform linux/arm64 \
  --provenance=false --sbom=false \
  --label org.opencontainers.image.revision=$(git rev-parse HEAD) \
  --label org.opencontainers.image.created=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  --label org.opencontainers.image.source=$(git remote get-url origin) \
  --tag "$REGISTRY/tc-manualbot:sha-eba96de" \
  --load .

docker push "$REGISTRY/tc-manualbot:sha-eba96de"      # 61 MB, 9.9s
```

`--load` then `docker push`, not `--push`. This is `jjc2_main/scripts/build.sh`'s
`--push-via-cli` path and the reason it exists: buildkit pushes using a credential session that
lives only as long as the build command, and an emulated build is slow enough to outlive it. The
push then fails with `no active session for <id>: context deadline exceeded`, which reads like a
network fault even though the image built fine.

`meimei` currently has `OutputPush` appending `--push` (`internal/build/run.go:38`), which is the
wrong default on any host whose architecture differs from the target.

### 4. Smoke-test the image before spending the tag

Immutable tags make a bad push permanent, so this is worth the seconds:

```bash
docker run -d -m 512m -p 13401:3300 "$IMG"
curl -s localhost:13401/api/health     # 200, index loaded
docker exec $CID uname -m              # aarch64
```

x86 hosts can run arm64 containers under the same QEMU that built them.

### 5. Register a revision with the image swapped

Terraform owns the task definition's shape and carries `ignore_changes = [task_definition]`, so a
deploy never applies Terraform — it copies the current revision and changes one field.

```bash
TD=$(aws ecs describe-task-definition --task-definition tc-public1-manualbot --query taskDefinition)

NEW=$(echo "$TD" | jq --arg img "$IMG" '
  (.containerDefinitions |= map(.image = $img))
  | {family, taskRoleArn, executionRoleArn, networkMode, containerDefinitions,
     volumes, placementConstraints, requiresCompatibilities, cpu, memory, runtimePlatform}
  | with_entries(select(.value != null))')

aws ecs register-task-definition --cli-input-json "$NEW" \
  --query taskDefinition.taskDefinitionArn --output text
# → arn:...:task-definition/tc-public1-manualbot:5
```

**That jq projection is not cosmetic.** `describe-task-definition` returns read-only fields
(`taskDefinitionArn`, `revision`, `status`, `requiresAttributes`, `compatibilities`,
`registeredAt`, `registeredBy`) that `register-task-definition` rejects. The projection is a
whitelist, and `with_entries(select(.value != null))` matters because the API also rejects
explicit nulls.

### 6. Update the service and follow the rollout

```bash
aws ecs update-service --cluster tc-public1 --service tc-public1-manualbot \
  --task-definition "$NEW_ARN" --force-new-deployment
```

Polling every 10s, filtering `deployments[]` by the new task definition ARN:

```
running 0/0  pending 0  failed 0  state=IN_PROGRESS     ← desired is 0 at first, not 1
running 0/0  pending 0  failed 0  state=IN_PROGRESS
running 0/1  pending 1  failed 0  state=IN_PROGRESS
running 0/1  pending 1  failed 0  state=IN_PROGRESS
running 0/1  pending 1  failed 0  state=IN_PROGRESS
running 1/1  pending 0  failed 0  state=COMPLETED        ← ~50s total
```

The first ticks report `0/0`. A poller that treats `running == desired` as success without also
requiring `desired > 0` declares victory immediately.

### 7. Verify

```
target group          port 32768  healthy
GET  /api/health      200  index: 164 pages / 1329 sections
GET  /api/search      200  ranked results
GET  /                200
GET  /widget.js       200  18346 B
GET  /team-guide.html 404  (excluded from the image on purpose)
http://              301  → https
```

## Traps worth encoding

**ECR tags are immutable here.** Skip an existing tag rather than failing; a broken image needs a
new commit, not a re-push. Never push without verifying the account first.

**A dirty tree makes a `sha-` tag lie, permanently.** `meimei build` already warns. For a push it
should arguably refuse without `--force`, because the consequence is no longer local.

**`--push` is wrong when the builder emulates the target.** Detect host arch ≠ target arch and
switch to `--load` + `docker push`. `jjc2_main/scripts/build.sh` decides this from the builder's
endpoint and `docker version --format '{{.Server.Arch}}'`.

**Never assume the service is on the newest revision.** Before this deploy the service referenced
revision **1** while the newest was **4** — three phases of Terraform work sat in revisions nothing
ran, exactly as `ignore_changes` intends. Worse, revision 1's container health check called
`/health`, a path that has never existed. A deploy must resolve the revision it means explicitly.

**`register-task-definition` rejects what `describe-task-definition` returns.** Project to a
whitelist and drop nulls.

**Verify from a genuinely different source address.** This deploy sits behind an ALB listener-rule
IP allowlist. A `WebFetch` probe reported `200` for a path that should have been `403`; a controlled
probe from the EC2 instance's own public IP (`18.183.50.250`, not allowlisted) returned the truth:

```
health: 403     ← blocked
root:   403     ← blocked
reindex:401     ← the allowlist's path exemption, then the app's own auth
```

`aws ssm send-command` against a box you already own is a reliable off-allowlist vantage point.
A hosted fetching service is not.

## What meimei needs to do

### Already there

- `.meimei.toml`, the catalog, `meimei ls`, `meimei build` (local) — all worked here unchanged
- `internal/build/plan.go` separates resolution from execution, so platform and tag choice are
  already pure and testable
- `OutputPush` exists as an enum with `--push` wired in `run.go`

### For `--push`

1. Resolve the registry host. `<account>.dkr.ecr.<region>.amazonaws.com`, account from STS. Repo
   name is `project.name + "-" + service.name`, which already holds.
2. Verify the account before any registry call, against a value in config. Getting this wrong is
   permanent under immutable tags.
3. `aws ecr get-login-password | docker login`, or the SDK's `GetAuthorizationToken`.
4. Skip when the tag exists — idempotent re-runs.
5. Force the target platform, not the host's. A local build correctly uses the host platform; a
   push must not inherit that.
6. Choose the push path from whether the builder is native to the target. Getting this wrong
   produces a confusing failure after a slow successful build.
7. Refuse a dirty tree without an override.

### For `deploy`

1. Read the current task definition for the family.
2. Swap the named container's image; project to the register-safe field whitelist; drop nulls.
3. `RegisterTaskDefinition`, then `UpdateService` with the returned ARN.
4. Poll `deployments[]` filtered by ARN. Success is `rolloutState == COMPLETED`, or
   `running == desired && desired > 0 && pending == 0`. Failure is `FAILED` or `failedTasks`
   over a threshold.
5. On failure, report **why**: the last STOPPED task's `stoppedReason` plus every container
   `reason`. `jjc2_main/scripts/deploy.sh:latest_stopped_reason` is the shape.
6. Discover packing from the cluster, never from config — a service's name is its task family and
   its container names are the services in it. Not exercised here (one container), so this project
   cannot validate it.

### Open questions, as answered

- **Does `deploy` need a `--target`?** Optional when the project declares exactly one, required
  when it declares more — and the error then names them. A flag that can only take one value is
  noise, and a project that grows a second target starts requiring it automatically.
- **Where does the Slack webhook come from?** Still open; notifications are not built. The
  conclusion stands: a webhook URL is a credential, so read it from Secrets Manager rather than
  inheriting the committed one in `scripts/lib/targets.sh`.
- **Does push imply build?** `meimei build --push` builds and pushes. The smoke-test path is
  served instead by `meimei build <svc> --platform linux/arm64`, which builds the TARGET platform
  locally and loads it — run it, then re-run with `--push` and the buildkit cache makes the second
  build near-instant. That avoids a second verb whose only job is to push something that might
  have been built from different source.

## What is still broken in the deployed service, and is not meimei's problem

`/api/chat` and `/api/contact` fail. The app still constructs an Anthropic client
(`api/chat.js:117`) and calls Mailgun, while Terraform supplies `LLM_PROVIDER=bedrock` and
`EMAIL_DRIVER=ses` that nothing reads. Retrieval works and the failure is graceful — sources
stream, then an `event: error` with a Japanese message and `answered:false` logged, which is what
opens the contact form:

```
chat error: AnthropicError: Could not resolve authentication method.
```

Also note `index-store: S3 load failed (NoSuchKey); falling back to disk` on startup. That is the
cold-account path working as designed: the index baked into the image serves until a reindex writes
one to S3.

## What the implementation found that the manual run did not

**The jq whitelist drops fields.** `register-task-definition` accepts eighteen fields;
`jjc2_main/scripts/deploy.sh`'s projection lists eleven. A task using `ephemeralStorage`,
`ipcMode`, `pidMode`, `proxyConfiguration`, `inferenceAccelerators` or `enableFaultInjection`
would have it **silently dropped** on the next deploy — the revision registers cleanly and the
task comes back subtly different. None of the current tasks use them, so it has never fired.

`meimei` copies field by field into the SDK's `RegisterTaskDefinitionInput`, which *is* the
whitelist: read-only fields have nowhere to go, and a registerable one cannot be forgotten
without the compiler noticing.

**Deploy from the family, not from the running revision.** The spec's own note that the service
sat on revision 1 while 4 existed argues for this and the manual run did the opposite —
`describe-task-definition --task-definition <family>` resolves to the *newest* revision, so
deploying from it carries whatever shape Terraform has registered since. Copying the revision the
service happens to be running would re-discard exactly the Terraform work `ignore_changes` parked
there.

**`docker login` is avoidable.** It rewrites `~/.docker/config.json` and replaces whatever
registry session was there. `meimei` writes a throwaway `DOCKER_CONFIG` directory holding one
credential for one run and removes it afterwards.

**`--force-new-deployment` is not needed when the task definition changes.** The manual run passed
it; pointing a service at a new ARN is already a new deployment. It is only required when nothing
about the service changed and you want the tasks replaced anyway — which is the `restart` case,
not this one.
