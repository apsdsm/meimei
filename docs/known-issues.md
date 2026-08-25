# Known issues

Things that are wrong or missing in the code as it stands. Each entry says what the behaviour is,
where it lives, and which way a fix points.

## The missing-repository error blames Terraform

`internal/registry/ecr.go` answers a `RepositoryNotFoundException` with "it is created by Terraform,
not by meimei". That is true of the repositories meimei was built for and false in general — meimei
works against a cluster built by hand, and there the sentence sends the reader looking for Terraform
that does not exist.

The accurate statement is that meimei does not create repositories, whatever does.

## `registry.Recent` does not paginate

It asks `DescribeImages` for `MaxResults: 100` and sorts what comes back, with no `NextToken` loop.
ECR returns images in no particular order, so on a repository holding more than 100 images the
"newest" list can miss recent tags.

Harmless for its only caller today: `suggestTag` asks for one and it is a hint on an error path. It
has to be fixed before `ls images` lands, because that is a listing people would trust.

## A deploy promotes by tag, not by digest

`Swap.Image` is always `repository:tag`. The property that staging and production run the same bytes
rests on ECR immutable tags plus the ambiguous-tag preflight, rather than on pinning a digest.

That is sound while every repository has immutable tags. Making it independent of that would mean
resolving the tag to a digest at deploy time and writing the digest into the container definition.

## The image's platform is never checked against the task definition

`platform` defaults to `linux/arm64` and can be overridden per build, but nothing compares it with
the `runtimePlatform` of the task definition being registered. A mismatch surfaces as tasks that
will not start, part way through a rollout.

The preflight is the natural place: it already fetches the image and the task definition before
anything is registered.

## The deployment controller is never read

`Promote` always calls `UpdateService` with a task definition. A service using the `CODE_DEPLOY`
(blue/green) controller rejects that call, and meimei would surface the raw AWS error with no
explanation. `follow.go` already tolerates a service reporting no `rolloutState`, so half the code
anticipates a controller the other half does not check for.

Not observed in use — every service meimei deploys to uses the ECS controller.

## Builds run one at a time

`cmd/build.go` builds sequentially and fails fast. Building several at once is worth doing, but it
needs a concurrency limit and somewhere for several streams of docker output to go.

## Not yet built

`ls` has no subjects. The plan is four — `ls builds`, `ls images`, `ls targets`, `ls services` —
which together are the data model the TUI renders. `ls services` needs per-target degradation so
that one expired SSO session reports itself rather than failing the command.

After that: the TUI, and notifications.
