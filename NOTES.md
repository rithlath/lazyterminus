# Notes: where Terminus differs from the spec

Findings from reading `terminus help` and real (read-only) `--format=json`
output on 2026-10-27, and how lazyterminus handles each one. After that capture,
development switched to fixtures only: no Terminus command, read-only or not,
is run against the real account during development.

## Versions

- **Terminus is 4.2.0, not 3.x.** The command set matches what the spec
  describes; field lists below are from 4.2.0.
- **Bubble Tea, Bubbles, Lip Gloss, Huh are v2** and now live at
  `charm.land/...` instead of `github.com/charmbracelet/...`. Used the v2
  modules (latest stable ≥ 2 weeks old). teatest is
  `github.com/charmbracelet/x/exp/teatest/v2`.
- Go toolchain pinned to 1.27.2 (1.27.3 was released less than two weeks
  earlier).

## Output shapes

- List commands emit a JSON **object keyed by ID**, not an array, and emit
  `[]` when empty (PHP's empty array). The parser handles both and keeps key
  order.
- Timestamps (`created`, `started_at`, `finished_at`, backup `date`) come as
  integers *or* floats with fractional seconds, sometimes in the same list.
- `workflow:list` rows have `env: null` for site-level workflows such as
  "Create a Multidev environment".
- `env:list` omits `php_version` unless asked: lazyterminus always passes
  `--fields=*`.
- `upstream:updates:status` does **not** support `--format`; it prints a
  plain word. On composer-managed sites it failed with "The operation failed
  to complete." in the capture, so the app treats its failure as "unknown",
  not an error state.
- Notices (`[notice] No changes on server.`) go to stderr, but the parser
  also tolerates stray non-JSON text before or after the document.

## Missing data

- **No "asleep" state anywhere.** Neither `env:info` nor `env:list` reports
  whether an environment is sleeping. The `asleep` badge therefore cannot be
  shown from Terminus data. Auto-wake instead *always* runs `env:wake`
  (shown in the status line) before drush or clone on dev and multidev
  environments; test and live never sleep on paid plans and are not woken
  automatically. If it is ever needed, the only way to detect sleep would be
  an HTTP request to the site, which itself wakes it (and is not Terminus).
- **No Drupal version.** `env:info` has `drush_version` but not the Drupal
  version; the only Terminus route is `remote:drush … status`, which runs on
  the server. The Detail panel reads `drupal/core` from the mapped local
  repo's `composer.lock` and labels it "(local repo)".
- **No failure reason in `workflow:list`/`workflow:info:status`.** Failed
  workflows only say `status: failed`. The reason comes from
  `workflow:info:operations` (the description of the first operation with
  `result: failed`; some failed operations have an empty description, shown
  as "platform operation failed"). `workflow:info:logs` was empty for the
  failed workflows captured.
- **No progress percentage for workflows.** Progress is computed from
  `workflow:info:operations`: operations with a result ÷ total operations.
  It is polled only for running workflows.

## Deployable commits

- `env:diffstat` shows *uncommitted SFTP changes* on a dev/multidev, not
  commits ready to deploy.
- Commits ready to deploy come from `env:code-log` labels: a commit in the
  source env's log that carries the source label but not the target label
  (this is exactly Terminus's `Environment::countDeployableCommits()`).
  Labels are a comma-separated string: `"test, live, dev"`.
- **Multidev code logs only carry the multidev's own label** (e.g. `d11`), so
  "commits ahead of dev" for a multidev is computed by comparing hashes
  against dev's code log. Terminus caps logs at 50 entries, so for
  long-lived branches this is approximate.

## Command restrictions

- `connection:set` only works on dev and multidevs (Terminus: "excludes Test
  and Live"). The `m` key is unavailable on test and live.
- `env:diffstat` only works on development environments.
- `upstream:updates:list` only works on development environments.
- Terminus mutating commands that ask "Are you sure?" (`env:wipe`,
  `multidev:delete`, `env:clone-content`, …) default to *no* under
  `--no-interaction`. lazyterminus does its own confirmation first, then
  passes `--yes`.
- Terminus has **aliases** (`deploy`, `env:cc`, `env:delete`, `import:db`,
  `drush`, …). They appear as single-word trailing entries in each command's
  `usage` list in `terminus list --format=json`. Safety rules resolve aliases
  to canonical names (from the catalog, with a built-in map for dangerous
  aliases as a fallback).

## Fixtures

- `testdata/real/` holds scrubbed captures (`scripts/scrub_fixtures.py`):
  people, emails, account/org/site UUIDs, and the vendor name are replaced
  with stable placeholders; long lists are trimmed. `connection:info` output
  was **not** kept because it contains database passwords.
- `testdata/mock/scenario.json` adds states the capture did not contain
  (commits pending deploy, an SFTP multidev with uncommitted changes, a failed
  test deploy, an asleep environment, upstream updates, two plugin commands).
  `--mock` loads both.

## Safety-layer decisions

None of these weaken the spec's rules; where a choice was needed, the
stricter option was taken unless noted.

- **Enforcement is in the runner, not the UI.** `safety.Guard` is the
  outermost runner. It refuses any command not on the read-only list unless
  the context carries an `Approval` for that exact argv, created by
  `Policy.Approve` after the user confirmed. Approvals are single-use and are
  re-checked against the policy at run time. Interactive commands
  (`remote:drush` through `tea.ExecProcess`) go through the same check.
- **Which environment a command "acts on"** decides protection. It's the env
  in the first `site.env` argument, except `env:clone-content`, where it's the
  target, and `multidev:create`, where it's the new multidev. So creating a
  multidev *from* live is a yes/no, not a typed confirmation: nothing on live
  changes. Every other command on live needs `site.live` typed.
- **Waking is confirmed with the action.** Auto-wake adds `env:wake` as an
  explicit first step in the same confirmation, so the modal shows it.
- **Yes/no confirmations only accept `y`**, not enter. An enter that submits
  a form must not also confirm the modal that form opens.
- **`--yes` is appended** to commands that Terminus would otherwise prompt
  for (deploy, clone, multidev delete, connection:set, commit), after
  lazyterminus's own confirmation. Under `--no-interaction` Terminus would
  answer "no".
- **Deploying to live with `--sync-content` and cloning into live are
  blocked outright**, from keys and from the palette.
- **If the audit log can't be written, mutating commands are refused.**
  Read-only commands still run.
- **`--mock` writes its audit log to `history.mock.log`** so mock sessions
  don't pollute the real history.

## Command palette

- The catalog comes from `terminus list --format=json` and is cached in
  `$XDG_CACHE_HOME/lazyterminus/catalog.json` keyed by `terminus --version`.
  Installing or removing a plugin doesn't change the version, so the cache
  won't notice. Delete the cache file (or `terminus self:clear-cache` won't
  help; that's Terminus's own cache) to pick up plugin changes. `--mock` uses
  a separate `mock/` cache directory.
- `_complete` is the only hidden command; it's left out of the palette.
- **Plugin commands are never on the read-only list**, so even list-style
  plugin commands get the yes/no confirmation. This is the spec's rule for
  unknown commands; add a plugin's read-only commands to `readonly.go` if
  that's too noisy.
- Mutating palette commands get `--yes` appended (after lazyterminus's
  confirmation) for the same reason as keybound actions; interactive ones
  (`remote:drush`, `remote:wp`, `self:console`) don't, since they own the
  terminal.
- Array arguments (drush's command) go after `--` so their flags aren't
  parsed by Terminus.
- Choice options are detected from a leading `[a|b|c]` in the option
  description (e.g. `backup:create --element`) and shown as a select.

## lazygit integration

- Local branch status uses local refs only (`git rev-list --left-right
  --count branch...remote/branch`); lazyterminus never runs `git fetch`, so
  it is "as of last fetch". The Pantheon remote is the one whose URL contains
  `.drush.in` (often named `pantheon`, with `origin`
  pointing at GitHub).
- `--env` takes a bare env/branch name inside a mapped repo and maps
  `master`/`main` to `dev`, since Pantheon's dev environment is the master
  branch.

## Not verified in this environment

- Real-account behavior (milestone 2 acceptance): by request, no Terminus
  command was run against real sites after the initial read-only capture.
- macOS: darwin binaries cross-compile, but weren't run on a Mac.
- `make demo` needs VHS, which isn't installed here; `docs/demo.tape` is
  written but not recorded.

## Pipeline view

- **Updates follow polling.** Terminus has no event stream, so the view
  changes when `workflow:list` is polled (every 5 s while anything is running).
  The dots are animated locally between polls.
- **One extra read per site:** `env:code-log` for live, to show live's newest
  commit. Cached like the other code logs (30 s).
- **Landings** are recorded when a watched deploy or "Sync code on" workflow
  succeeds, either seen finishing between two polls, or appearing already
  finished after lazyterminus started (short deploys can fall between polls),
  or when lazyterminus's own deploy job succeeds. The commit count is what was
  waiting just before. History from before startup is never shown as landed.
- **Merges into dev** are matched by a running workflow whose name contains
  "merge" and the multidev's name. The exact workflow name for
  `multidev:merge-to-dev` wasn't in the captured data, so this is a best
  guess; if it doesn't light up, the merge still shows as a running workflow.
- **"Behind dev"** compares the 50 newest commits of each log, like "ahead";
  for long-lived multidevs both counts are approximate.
- **Clones started elsewhere** are drawn as an arc when a running workflow's
  description starts with "Clone" and names its source as `from "<env>"`.
  That wording is a guess (no clone was in the captured data); clones started
  from lazyterminus always draw the arc, since the job knows its source.
- **Action results on boxes** ("✓ cache cleared") come from lazyterminus's own
  jobs and last 3 minutes; deploy landings last 15.
