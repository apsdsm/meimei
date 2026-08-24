# Rename plan: meimei's nouns to AWS's

**Status:** in progress. Written 2026-08-24 from the finding in
[gap-vocabulary.md](gap-vocabulary.md); every decision is settled. **Steps 0-2 are done** — the next one is step 3.

This is a rename plus a version gate. It fixes no bug — including the silent-overwrite bug in
`byContainer`, which stays exactly as it is. Steps 1-5 change no behaviour at all; step 6 changes
user-facing wording and one `ls --json` key deliberately, and nothing else. The
target-scoping field from [gap-many-environments-one-cluster.md](gap-many-environments-one-cluster.md)
is the change after this one.

Two pages are the standing reference this plan writes against, and both are updated in the same commit
as any step that changes them — a reference that lags the code is worse than none, because it is what
someone copies from:

- [config-reference.md](config-reference.md) — what a correct `.meimei.toml` looks like. The shape
  this plan produces is **version 2**.
- [terms.md](terms.md) — one word per concept, in config, in code, and in everything meimei prints.

**Why rename first.** Both changes break `.meimei.toml`, and there are two real ones in use
(`jjc2_main`, `jjc_manualchatbot`). Taking one breaking change instead of two costs one migration
rather than two. And the field the next change adds cannot be named while `Target` is holding the
slot an ECS service name goes in and `Service` means an image. The version key means the change after
this one costs one integer rather than another round of guessing what a file is.

## The name mapping

### Config keys — this is the breaking part

| Now | After | Why |
|---|---|---|
| *(absent)* | `version = 2` | New, required, and the first line. Gates every future breaking change on one integer instead of a guess. |
| `[[services]]` | `[[images]]` | ECS has a `service` and this is not one. The struct's own doc comment already says "one buildable container image". |
| `[[services]].name` | `[[images]].name` | Unchanged. Still the repository suffix, the container name, and the CLI word — decoupling those three is not this change. |
| `[[targets]]` | `[[targets]]` — **unchanged** | See the decision below. |

Everything else in the file is untouched: `[project]`, `[registry]`, and every field inside
`[[images]]` and `[[targets]]`.

### Go identifiers

| Now | After | Package |
|---|---|---|
| *(new)* | `Config.Version int` + `ConfigVersion = 2` const | `internal/config` |
| `config.Service` | `config.Image` | `internal/config` |
| `Config.Services` | `Config.Images` | " |
| `Config.Repository(s Service)` | `Config.Repository(i Image)` | " (also `AbsDockerfile`, `AbsContext`) |
| `config.Target` | `config.Target` — unchanged | " |
| `deploy.Task` | `deploy.Service` | `internal/deploy` |
| `Task.Service` | `Service.Name` **and** `Service.Family` | " — split, see below |
| `Packing.Tasks` | `Packing.Services` | " |
| `Packing.TaskFor(container)` | `Packing.ServiceFor(container)` | " |
| `Packing.TaskNamed(family)` | `Packing.ServiceNamed(family)` | " |
| `Packing` | `Packing` — unchanged | " |
| `catalog.Entry.Service` | `catalog.Entry.Image` | `internal/catalog` |
| `build.Options.Services` | `build.Options.Images` | `internal/build` |
| `build.Plan.Service` | `build.Plan.Image` | " |
| `registry.Ref.Service` | `registry.Ref.Image` | `internal/registry` |

`deploy.Task` becoming `deploy.Service` is only possible because `config.Service` is vacating the
name. That is the whole point of doing them in one plan: the struct whose comment has to talk the
reader out of the AWS meaning ("Task is one ECS service on the cluster") gets to be called what it is.

## Decisions

### Settled: `[[targets]]` stays `targets`

gap-vocabulary.md proposes `[[targets]]` → `[[clusters]]`. Do not do it.

The next change gives `Target` a field naming which ECS services in that cluster it deploys to. After
that a target is a cluster **plus a scope inside it** — which is precisely not a cluster, and two
targets will point at the same one. Renaming it to `clusters` now means renaming it back then.

The doc's complaint about `Target` is that it sits one level too high, holding the slot an ECS service
name belongs in. The next change fills that slot. `Target` then means "a place I deploy to", which is
what it says. What it needs from this change is an honest doc comment, not a new name.

The ELB `target` / `target group` collision remains and is accepted: it is a different service's
vocabulary, and no name for this concept avoids every collision.

### Settled: split `Task.Service` into `Name` and `Family`

`Task.Service` is documented as `== the task definition family`. That is true because the Terraform in
these repos names both from one string, not because ECS requires it. A deploy needs both — it calls
`RegisterTaskDefinition` against the family and `UpdateService` against the service.

Both fields get assigned from `svc.ServiceName` in `Discover`, so behaviour is identical. The comment
records that they are equal by convention rather than by requirement. Doing it here means the next
change does not have to reopen `Discover` to tell the two apart.

### Settled: `Packing` keeps its name

It brushes ECS's `binpack` placement strategy, which gap-vocabulary.md calls minor. It is meimei's own
index with no AWS counterpart, so there is no correct AWS name to move to, and renaming it widens the
diff for no gain. Revisit if it ever reads ambiguously in the new code.

### Settled: a `version` key, and no backwards compatibility

`.meimei.toml` gains `version = 2` as its first line. `LoadFrom` reads it before anything else and
refuses any other value by name. There is no compatibility shim, no dual-name acceptance, and no
deprecation window — that bridge is already burned and the two files in the wild get migrated by hand.

| File says | meimei does |
|---|---|
| `version = 2` | reads it |
| no `version`, or `version = 1` | refuses, naming the two v1 → v2 edits |
| `version = 3` or higher | refuses, saying to upgrade meimei |

The exact wording of both refusals is in [config-reference.md](config-reference.md), so there is one
place to change it.

**This replaces the `MetaData.Undecoded()` sniffing an earlier draft of this plan proposed.** Sniffing
for a leftover `[[services]]` answers "is this the file I expected" only for the renames already made.
It cannot see a breaking change that *adds* a required key: `toml.Unmarshal` silently ignores what
it does not know, so an old file missing a new key looks the same as a new file whose author left it
out. A version integer answers it for every change after this one.

Two consequences worth stating:

- **`version` is checked before `Validate`, not inside it.** A v1 file must fail saying "this is a v1
  file", not `no images defined` — which is what a v1 file otherwise produces, since `[[services]]`
  decodes into nothing.
- **Bare top-level keys must precede the first `[table]` in TOML**, so `version` puts itself at the
  top of the file with no convention to enforce. That is where a format version belongs: it decides
  how everything below is read and is not a property of the project, the registry or any target.

### Settled: the CLI's phrasing changes too

Signed off 2026-08-24, and wider than the original question. The rule is one word per concept
everywhere meimei speaks — config, code, help text, error messages and output — with no synonyms left
over. The canonical list is [terms.md](terms.md), and the strings it costs are in "Step 6 in full"
below.

The internal rename on its own would leave `--help` teaching the vocabulary the rename exists to
retire. Two collisions make it more than tidiness:

- **`meimei deploy` prints `service` in both senses.** `no service named` means "you did not name an
  image"; `service is on jjc2-dev1-internal:5, deploying from :7` means the ECS service. One command,
  one word, two levels of the hierarchy.
- **`container` is being used for a buildable image**, in `meimei --help` and `meimei ls --help`. That
  is a third word for the thing that already had two, and it collides with the container definition a
  deploy actually swaps.

It stays a separate commit (step 6) so a regression in wording and a regression in mechanism are
separately bisectable. It is no longer droppable.

`--to` does not change. It names a target and `Target` is staying.

## Migrating the two real config files

Both live in other repos, so they are separate commits in separate places. Two edits each, and nothing
else — the full v1 → v2 diff is in [config-reference.md](config-reference.md).

| Repo | Edit |
|---|---|
| `jjc2_main/.meimei.toml` | `version = 2` as the first line; five `[[services]]` → `[[images]]`. Plus a prose pass: the header comment and the `# TARGETS #` block use "service" throughout in the old sense, including a paragraph that already has to say "an ECS service's name is its task family" in the AWS sense in the same sentence. |
| `jjc_manualchatbot/.meimei.toml` | `version = 2` as the first line; one `[[services]]` → `[[images]]`. Header comment says "One service, one image, one ECR repo", which becomes one sentence shorter. |

**Order matters, and now both directions fail well.** New binary against an old file gives the v1
refusal. Old binary against a new file ignores both `version` and `[[images]]` and reports
`no services defined` — which is unhelpful but harmless, and is the last time it can happen: from
version 2 onward the version key is there to be read. So: land the rename, bump to 1.1.0, tag,
install, then edit the two config files. Anyone still on 1.0.0 upgrades before pulling those repos.

## Hazards

**AWS owns some of these words. Do not run a blind `sed`.** 19 occurrences of `DescribeServices`,
`ListServices`, `out.Services`, and the `aws-sdk-go-v2/service/…` import paths must not change, and 14
occurrences of `TaskDefinition` must not change.

**`internal/deploy/follow.go:290-293` already uses `Tasks` in the AWS sense** — `DescribeTasks`, and
`desc.Tasks[0]`, one running copy of a revision. That is correct today and stays. Renaming meimei's
`Task` out of the way is what makes it unambiguous.

**The compiler catches the identifier renames; nothing catches the strings.** Error-message text is
covered by assertions in `config_test.go`, `target_test.go`, `plan_test.go` and `cmd/deploy_test.go`,
so `go test ./...` catches most of step 6. Help text and doc comments are caught by nothing — read
them.

**Test fixtures are inline TOML, not files.** `[[services]]` appears 32 times across
`config_test.go`, `target_test.go`, `plan_test.go` and `catalog_test.go`, several times inside
single-line `"…\n[[services]]\nname = …"` strings. These are safe to `sed` — they contain no AWS
identifiers.

## Steps

Each step compiles and passes `go test ./...` on its own. **Any step that changes the config shape
updates [config-reference.md](config-reference.md) in the same commit** — steps 1 and 5 do.

| Step | Change |
|---|---|
| 0 ✓ | Capture baseline output — see Verification. Do this before touching anything. |
| 1 ✓ | `config.Service` → `config.Image`, `[[services]]` → `[[images]]`, `Config.Services` → `Config.Images`, the three methods taking one, and the `services[%d] has no name` / `duplicate service %q` / `service %q: …` validation messages. Test fixtures with it. **Reference doc: the `[[images]]` table.** |
| 2 ✓ | `Target`'s doc comments only: say that `cluster` is the ECS cluster, that nothing here names the ECS service, and that the next change adds it. No identifier moves. |
| 3 | `deploy.Task` → `deploy.Service`, `Tasks` → `Services`, `TaskFor` → `ServiceFor`, `TaskNamed` → `ServiceNamed`, and split `Service` → `Name` + `Family` with both assigned from `svc.ServiceName`. |
| 4 | The leaf renames: `catalog.Entry.Service`, `build.Options.Services`, `build.Plan.Service`, `registry.Ref.Service`. |
| 5 | The version gate: `ConfigVersion = 2` const, `Config.Version int` with `toml:"version"`, and the check in `LoadFrom` **before** `Validate`. Three tests — v2 loads, a file with no `version` is refused naming the two edits, `version = 3` is refused saying to upgrade. Every test fixture in the repo gains `version = 2`. **Reference doc: the `version` section and the two refusal messages.** |
| 6 | The user-facing sweep — help text, error messages, `ls --json`'s `"services"` key, and the `(s)` plurals — against the canonical list in [terms.md](terms.md). Every string is listed in "Step 6 in full" below. |
| 7 | Docs and release: `CLAUDE.md` (lines 44-48, 76-86, `Service.Group` → `Image.Group`, a pointer to `config-reference.md` from the "Where a fact lives" section, and its own prose swept against `terms.md` — it says "service" for an image throughout), `gap-vocabulary.md` (what was decided, and that `targets` is staying, with the reason), `gap-many-environments-one-cluster.md` (its "rename `[[services]]` … and the hole names itself" line is now done), `index.md`, `terms.md` if step 6 turned up a concept it does not list, and `config-reference.md`'s status table flipped to say version 2 is what the tool reads. Bump `cmd/version.go` to `1.1.0`, tag, push. `README.md` needs nothing — already flagged stale on this branch. |
| 8 | Migrate `jjc2_main` and `jjc_manualchatbot`, after installing the tagged binary. |

`known-issues.md` is stale — both entries describe code that is already fixed — but that is a separate
commit and not part of this plan.

### Step 6 in full: the user-facing sweep

Signed off 2026-08-24: the CLI's phrasing changes with everything else, so there are no synonyms left.
The canonical list is [terms.md](terms.md); this is every string that has to change to make it true.

**Help text** — `image` replaces `service`, and `container` stops being used for a buildable thing:

| File:line | Now | After |
|---|---|---|
| `cmd/root.go:12` | `Build and deploy this project's containers` | `…this project's images` |
| `cmd/build.go:22` | `build [service...]` | `build [image...]` |
| `cmd/build.go:23` | `Build service container images` | `Build container images` |
| `cmd/build.go:24` | `Build one or more services, or every buildable service with --all.` | `…one or more images, or every buildable image…` |
| `cmd/build.go:42` | `Build every buildable service` | `Build every buildable image` |
| `cmd/ls.go:17` | `List the containers this project can build` | `List the images this project can build` |
| `cmd/ls.go:18` | `List every service declared in .meimei.toml` | `List every image declared in .meimei.toml` |
| `cmd/deploy.go:21` | `deploy [service...]` | `deploy [image...]` |
| `cmd/deploy.go:39` | `Deploy every service this repo declares` | `Deploy every image this repo declares` |

`cmd/deploy.go:23-33` is the one that needs rewriting rather than substituting. It currently reads
"Which services are packed into which task is read from the cluster … a service's name is its task
family and its container names are the services it carries" — which uses *service* in both senses and
*task* for the task definition, inside three lines. Rewrite it against [terms.md](terms.md): images are
packed into task definitions, an ECS service's name is its family, and its container names are the
images in it.

**Error messages** — the `service`-means-image family. `internal/config/config.go`'s nine messages are
not listed: they name the config table itself (`no services defined`, `services[%d] has no name`), so
they would be actively wrong between step 1 and step 6 and land with the key in step 1 instead.

| File:line | Now |
|---|---|
| `internal/build/plan.go:241` | `--all cannot be combined with a service name` |
| `internal/build/plan.go:244` | `no service named (name one or more services, or pass --all)` |
| `internal/build/plan.go:261` | `nothing to build: no service is buildable` |
| `cmd/deploy.go:226` | `--all cannot be combined with a service name` |
| `cmd/deploy.go:229` | `no service named (name one or more services, or pass --all)` |
| `cmd/deploy.go:240` | `nothing to deploy: every service is disabled` |
| `cmd/ls.go:50` | `%d service(s) cannot be built` — also loses the `(s)` |

**Leave alone.** These already use *service* in the ECS sense and are correct:
`internal/deploy/deploy.go:143`, `:148`, `:161`, `internal/deploy/follow.go:215`, `:218`, and
`cmd/deploy.go:183` (`service is on %s, deploying from %s`). After the sweep they are the only uses of
the word, which is the point.

**`ls --json`.** `jsonOutput.Services` → `Images`, and the key `"services"` → `"images"`
(`cmd/ls.go:184`, `:214`, `:216`). This is an output contract with no known consumer; changing it here
rather than later is cheaper while the config key is changing in the same release.

**Pluralisation.** Replace `(s)` with the existing `plural` helper from `cmd/deploy.go:428` at
`cmd/ls.go:50`, `cmd/build.go:149`, `cmd/build.go:166` and `internal/deploy/follow.go:192`. The helper
is in `cmd`, so the `follow.go` site either moves the helper somewhere shared or phrases around it —
`plural` is four lines and duplicating it into `internal/deploy` is worse than moving it.

**Tests that assert these strings:** `internal/config/config_test.go`, `internal/config/target_test.go`,
`internal/build/plan_test.go`, `cmd/deploy_test.go`. `go test ./...` catches the errors; help text is
caught by nothing, so read `meimei --help`, `meimei build --help`, `meimei ls --help` and
`meimei deploy --help` in full.

## Verification

The real config files are not migrated until step 8, so from step 1 onward they no longer load. Verify
against a scratch copy instead, kept **inside** the repo it belongs to — `LoadFrom` sets the build root
from the config file's own directory, so a copy in `/tmp` would make every `dockerfile` path look
broken.

**Step 0, with the installed 1.0.0 binary**, from `jjc2_main`:

```bash
meimei ls --json > /tmp/before-ls.json
meimei deploy --all --to dev1 --dry-run 2> /tmp/before-deploy.txt

# the v2 copy the later steps read, deleted at step 8
sed 's/^\[\[services\]\]$/[[images]]/' .meimei.toml > .meimei.v2.toml
printf 'version = 2\n\n' | cat - .meimei.v2.toml > .tmp && mv .tmp .meimei.v2.toml

# and keep it out of git status, or it dirties the tree
echo .meimei.v2.toml >> .git/info/exclude
```

**The exclude is not optional.** An untracked scratch file makes the working tree dirty, and `dirty`
is in `ls --json` and decides the tag — so without it every later diff shows `"dirty": true` and reads
like a rename bug. `.git/info/exclude` is local and never committed, unlike `.gitignore`.

Adding `version = 2` now is harmless before step 5 — an unknown key is ignored — so one copy serves
every step.

**After each of steps 1-4**, with the newly built binary:

```bash
meimei --config .meimei.v2.toml ls --json | diff /tmp/before-ls.json -
meimei --config .meimei.v2.toml deploy --all --to dev1 --dry-run 2>&1 | diff /tmp/before-deploy.txt -
```

Both must be empty. The rename is behaviour-preserving, so any diff is a bug.

**After step 5**, the two diffs must still be empty, plus the gate itself against the real files:

| Command | Expected |
|---|---|
| `meimei ls` in `jjc2_main` (real file, still v1) | refused, naming the two v1 → v2 edits |
| `meimei --config .meimei.v3.toml ls` (a copy with `version = 3`) | refused, saying to upgrade meimei |
| `meimei --config .meimei.v2.toml ls` | works |

**After step 6**, `before-ls.json` differs by the `"services"` key only and `before-deploy.txt` by the
deliberate wording changes only. Diff them and read every line rather than checking they are non-empty.

**After step 8**, from `jjc_manualchatbot` with the real migrated file:
`meimei deploy chatbot --to public1 --dry-run` — the single-image, single-target shape, which exercises
`ResolveTarget`'s one-target default that the multi-target repo does not. Then delete both
`.meimei.v2.toml` scratch copies.

## What must not change

- **No prefix or substring matching, anywhere.** There is no `HasPrefix` or `Contains` on any AWS
  identifier in the tool, and repository names are exact concatenation. Both gap docs single this out
  as the thing a rename is most likely to disturb.
- **`byContainer` stays a flat cluster-wide map, silent overwrite included.** Fixing it needs the
  target scope that does not exist yet; half-fixing it here — a collision error without the scope —
  refuses the topology the next change is being built to allow.
- **`internal/build/plan.go` stays pure.** No I/O, no clock, no daemon inside `Resolve`.
- **`Deployable`/`Containers` keep coming from the family's newest revision, and `Running`/
  `RunningContainers` stay reporting-only.** That split is the fix for the first entry in
  `known-issues.md` and is easy to undo by accident while moving fields around.
