# How a build and a deploy work

What the two commands actually do, and the AWS behaviour they encode. Written from a manual ECR push
and ECS deploy run against `nova_chatbot` on 2026-08-19, and kept current with the code.

## `meimei build api`

1. Resolve the plan: which builds, what tag, which platform, which labels. Pure — no docker, no AWS,
   no clock. This is what makes `--dry-run` free.
2. Ask ECR whether the tag is already in the repository. If it is, skip: tags are immutable, so a tag
   already there is a build that already finished. Re-running the same command is quiet, not fatal.
3. `docker buildx build` for the platform the config declares, with the OCI labels attached and
   `MEIMEI_BUILD_ID=<tag>` handed in as a build argument.
4. Push to the repository the build declares.

`--no-push` stops after step 3 and loads the image into the local docker store. It builds the same
image a push would have sent, so on an x86 host an arm64 image loaded there will not run without
emulation. It answers "does this build", not "give me something to run".

## `meimei deploy api --tag T --target dev1`

1. **Resolve the target** and open a session for its account, asserting the account against the live
   caller identity.
2. **Discover the scope.** With `services` declared, describe exactly those; without it, list the
   cluster. For each service, read the family out of its task definition ARN, then describe that
   family's newest revision.
3. **Index by container name**, refusing any name carried by two services in the scope.
4. **Preflight**: ask ECR whether every image to be promoted is there. Refuse before touching ECS if
   one is not.
5. **Copy the newest revision** of the family, changing one field: the `image` on each named
   container definition.
6. **`RegisterTaskDefinition`** — a new revision. The previous one still exists, untouched.
7. **`UpdateService`** pointing the service at the new revision. ECS creates a deployment.
8. **Follow** until `rolloutState` is `COMPLETED`, or the target's timeout expires.

Builds sharing a task definition are promoted in one revision and one rollout. That is also the only
thing that works on a cluster brought up fresh, where every container is essential and no task can
start until every image in the definition is real.

## The newest revision, not the running one

A deploy copies the family's newest revision, which is usually not the one the service is running.

Terraform owns the shape of a task definition and carries `ignore_changes = [task_definition]`, so
when it changes the shape it registers a revision and leaves the service where it was. The newest
revision is therefore the shape Terraform means. Copying the running revision instead would silently
discard every shape change since the last deploy.

This has to hold for the packing too, and that is the easier half to get wrong. A container just
added exists only in the newest revision, so a packing read from what is running does not know it can
be deployed. A container just removed is still in the running one, so such a packing believes it will
survive a deploy that in fact drops it. Both were observed on acme dev1 on 2026-08-20.

`Service.Deployable` and `Service.Containers` carry the newest revision and decide what can be
deployed. `Service.Running` and `Service.RunningContainers` carry what is running and are used only
for reporting: whether the service is behind, and which containers a deploy would remove.

## The preflight

Before registering anything, `deploy` asks ECR about every image it would promote. Four answers:

| Answer | What it means | What happens |
|---|---|---|
| Present | the tag is there, and it is the only tag on that image | proceed |
| Missing | the registry answered, and the tag is not there | refuse, naming what is missing, where it was looked for, and what to run |
| Ambiguous | the tag is there and the same image carries other tags | refuse, naming the other tags |
| Unknown | the registry could not be asked | warn and proceed |

**Unknown is never folded into Missing.** A deploy that would otherwise work must not become
impossible because a read permission is absent.

**Ambiguous is a refusal because the tag is baked into the image.** meimei pushes exactly one tag per
build, and that tag goes into the image as `MEIMEI_BUILD_ID`. A second tag on the same image was put
there by something else, so a task definition naming it would describe the right bytes by a name the
code inside does not report. `--skip-image-check` is the way past when you know the tag is right.

The gap this closes was observed twice in one afternoon on 2026-08-20: work committed, image not
pushed, `meimei deploy` run, two minutes of a rollout that could never succeed, ending in a
`CannotPullImageManifestError` naming neither the tag nor the cause. The previous revision kept
serving both times, so the cost was time rather than an outage.

Every missing image is reported at once. Reporting the first would make an operator discover the rest
one deploy at a time.

## AWS behaviour this encodes

**ECR tags are immutable here.** Skip an existing tag rather than failing. A broken image needs a new
commit, not a re-push. Never push without verifying the account first, because a push into the wrong
account cannot be taken back.

**A dirty tree makes a `sha-` tag lie permanently.** Every build pushes, so this is a refusal rather
than a warning unless the caller names a tag, passes `--force`, or uses `--no-push`.

**Buildx must not push when the builder emulates the target.** meimei detects host architecture
against target architecture and switches to `--load` followed by `docker push`.

**`RegisterTaskDefinition` rejects what `DescribeTaskDefinition` returns.** Describe returns
read-only fields — arn, revision, status, requiresAttributes, compatibilities, registeredAt,
registeredBy — that register refuses. meimei copies field by field into the register input, whose
type is itself the whitelist, so a read-only field has nowhere to go and a registerable one cannot be
dropped without the compiler noticing. The shell scripts this replaces hand-maintain that list in jq
and silently drop six fields.

**Success is the exit code, never parsed progress output.** A change in how buildx formats progress
cannot fail a good build.

**Verify a deploy from a genuinely different source address.** `nova_chatbot` sits behind an ALB
listener-rule IP allowlist. A hosted fetching service reported `200` for a path that should have been
`403`; a probe from the EC2 instance's own public IP returned the truth. `aws ssm send-command`
against a box you already own is a reliable off-allowlist vantage point.
