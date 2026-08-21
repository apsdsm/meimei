# Known issues

Things found in use that are not yet fixed. Each entry says what was observed, on what, and which
way the fix points — not a patch, so an entry stays true until someone writes one.

## The task-mate warning reads a different revision than the deploy writes

`Discover` builds the packing from each ECS **service's current** task definition
(`svc.TaskDefinition`). `CurrentImages` and `Promote` resolve the **family**, which is the newest
revision. When a service is several revisions behind, the two describe different tasks.

That gap is not an edge case. It is exactly what `ignore_changes = [task_definition]` produces, and
`Promote`'s own comment names it as the reason `Promote` reads the family rather than the running
revision. So the halves disagree precisely in the situation the design was written for.

Two consequences, both seen on jjc2 `dev1` on 2026-08-20:

**The `also restarts (shared task): …` line names containers that are not in the task being
registered.** `meimei deploy process-runner --to dev1 --dry-run` printed
`also restarts (shared task): api` while promoting from `jjc2-dev1-internal:7`, which has no `api`
container — the running revision 5 did. The api was not going to restart; it was going to be
*removed* from that task, which is a larger change than the warning describes and the opposite of
reassuring.

**`TaskFor` refuses a service that exists only in the newest revision.** It uses the same
running-revision packing, so `deploy` returns `no container %q on cluster` for a container
`Promote` would have handled. On the same cluster, before the external service was moved forward by
hand, the three `*-web-spa` containers existed only in the newest revision — a deploy of any of them
would have been refused with a message implying the cluster does not run them.

Fix direction: build the packing from the task definition `Promote` copies — the family's newest
revision — and keep the running revision for reporting drift only. "This service is N revisions
behind" is worth printing; it should not decide what can be deployed.

## `--all` deploys what the repo declares, not what the cluster runs

`selectServices` with `--all` walks the config's services. On a cluster mid-migration that includes
containers the cluster has never run, and excludes ones it still does. Not wrong — the doc comment
says it is deliberate — but combined with the issue above the failure arrives as a confusing
per-container error partway through a multi-task deploy, after earlier tasks have already rolled.

Worth considering: resolve every requested service against the packing *before* promoting anything,
so the run either starts clean or refuses whole.
