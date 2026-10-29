# lazyterminus

A terminal UI for [Terminus](https://docs.pantheon.io/terminus), Pantheon's
CLI, in the spirit of lazygit and lazydocker: see where every Pantheon site
and environment stands, watch workflows, and run Terminus actions without
typing long commands, behind a safety layer built for production sites.

```
╭─ [1] Sites ──────────────────────────────────╮╭─ [3] acme-web.test ──────────────────────────────────────────────────╮
│   acme-intranet  drupal   8 envs             ││ Domain        test-acme-web.pantheonsite.io                          │
│   acme-portal    drupal   3 envs             ││ Connection    git                                                    │
│ › acme-web       drupal   12 envs            ││ PHP           8.4                                                    │
│   sandbox-12     drupal   3 envs             ││ Drupal        unknown (no local repo mapped)                         │
╰───────────────────────────────────── 3 of 4 ─╯│ Protection    protected (typed confirmation)                         │
╭─ [2] Environments · acme-web ────────────────╮│ Commits       ↑2 ready to deploy from dev                            │
│   ● dev         git                          ││ Last deploy   ✗ 7h ago by user1@example.com                          │
│ › ✗ test        git ↑2 failed                ││                                                                      │
│   ● live        git ↑1                       ││ ✗ Failed 7h ago: Deploy code to "test"                               │
│ ── multidevs ─────────────────────────────── ││   Bring application container to desired configuration state         │
│   ● annual      git                          ││                                                                      │
│   ● contact-us  git                          ││ Commits ready to deploy from dev:                                    │
│   ● d11         git ↑11                      ││   a1c9e0f PROJ-77: Add hero banner block to homepage — Developer B   │
│   ● feedback    sftp ↑1 ✎2 uncommitted       ││   b2d0f1a PROJ-76: Update news listing view config — Developer A     │
│   ● newsletter  git                          │╰──────────────────────────────────────────────────────────────────────╯
│   ● proj-19     git                          │╭─ [4] Workflows ──────────────────────────────────────────────────────╮
│   ● proj-29     git                          ││   ✓ Automated backup for the "dev" environment          dev · 1h ago │
│   ● proj-40     git                          ││   ✓ Automated backup for the "live" environment        live · 1h ago │
│   ● schedules   git                          ││   ✗ Deploy code to "test"                              test · 7h ago │
│                                              ││   ✓ Automated backup for the "test" environment        test · 9h ago │
│                                              ││   ✓ Converge resources based on updated proper… newsletter · 19h ago │
│                                              ││   ✓ Sync Advanced Edge configuration (if any) … newsletter · 19h ago │
│                                              ││   ✓ Create a Multidev environment                     site · 19h ago │
│                                              ││   ✓ Automated backup for the "dev" environment          dev · 1d ago │
│                                              ││   ✓ Automated backup for the "live" environment        live · 1d ago │
│                                              ││   ✓ Automated backup for the "test" environment        test · 1d ago │
│                                              ││   ✓ Automated backup for the "dev" environment          dev · 2d ago │
│                                              ││   ✓ Automated backup for the "live" environment        live · 2d ago │
╰──────────────────────────────────── 12 envs ─╯╰──────────────────────────────────────────────────────────────────────╯
 lazyterminus   MOCK  4 sites                                                                             acme-web.test
d deploy  c cache  b backup  l clone  m mode  w wake  x drush  o commit  n new md  D del md  u upstream  : palette  g l…
```

That is `lazyterminus --mock --env acme-web.test` at 120×32. More captures: [deploy form](docs/screen-deploy.txt),
[command palette](docs/screen-palette.txt), [narrow layout](docs/screen-narrow.txt).
`make demo` records an animated GIF from [docs/demo.tape](docs/demo.tape)
with [VHS](https://github.com/charmbracelet/vhs).

## Drupal only

lazyterminus is built for Drupal sites and has only been checked against
Drupal (`drupal8` framework) Terminus output; it has never been run against a
WordPress site. Most actions are plain Terminus commands that don't depend on
the framework (pipeline, deploy, backup, clone, cache, mode, wake, multidevs,
upstream updates, history), and WP-CLI is reachable from the command palette
(`remote:wp`, alias `wp`). But on a WordPress site:

- `x` still runs drush, not WP-CLI.
- The deploy form's note that Quicksilver hooks run `drush cim` doesn't apply.
- The Detail panel's Drupal version (read from `drupal/core` in the mapped
  repo's `composer.lock`) shows "not in composer.lock".

## Install

Requirements: [Terminus](https://docs.pantheon.io/terminus/install) 3.x or
4.x, authenticated (`terminus auth:login`). Optional: lazygit, for `g`.

```sh
make install          # builds and installs to ~/.local/bin/lazyterminus
make dist             # static binaries for linux/darwin × amd64/arm64 in dist/
```

Building needs Go 1.27+ (an older `go` downloads the right toolchain
automatically). The binaries have no runtime dependencies: copy
`dist/lazyterminus-*-darwin-arm64` to a Mac, or `*-linux-amd64` into WSL, and
put it on your `PATH`.

## Quick start

lazyterminus uses your Terminus session: it shows the sites the logged-in
account can see, and the account in the status bar. If you're not logged in
(or the session expires), it says so; run
`terminus auth:login --machine-token=<token>` in another pane and press `r` on
the Sites panel.


```sh
lazyterminus --mock            # try it first: built-in fixtures, nothing touches Pantheon
lazyterminus --dry-run         # real data, but mutating commands are printed, not run
lazyterminus                   # the real thing
lazyterminus --env acme-web.test
```

On first run a commented config is written to
`~/.config/lazyterminus/config.yaml`. Map each site to its local checkout so
the Detail panel can show the Drupal version and `g` can open lazygit.

## Layout

| Panel | Shows |
| --- | --- |
| **1 Sites** | name, framework, environment count |
| **2 Environments** | dev, test, live, then multidevs. Each row: state symbol and badges |
| **3 Detail** | connection mode, PHP, Drupal (from the local `composer.lock`), commits ahead of the next environment, uncommitted SFTP changes, last deploy, branch and local git status (multidevs), most recent unresolved failure and its reason |
| **4 Workflows** | lazyterminus's own jobs, then running Pantheon workflows with a progress bar (operations done/total), then recent ones with ✓/✗ |

Below 100 columns the panels stack (the focused one gets the most room); at
100 or more, Sites and Environments sit on the left, Detail and Workflows on
the right.

Every status has a symbol or word as well as a color, so `NO_COLOR` and
monochrome terminals lose nothing:

| Symbol | Meaning |
| --- | --- |
| `●` green | healthy, in sync |
| `●` amber | needs attention: commits waiting, uncommitted SFTP changes |
| `◐` / spinner | running |
| `✗` red, `failed` | most recent run of a workflow failed and hasn't succeeded since |
| `○` | not loaded yet |
| `↑3` | test/live: 3 commits ready to deploy here. Multidevs: 3 commits ahead of dev |
| `sftp`, `✎2 uncommitted` | on-server development, with 2 changed files |
| `locked` | HTTP basic auth enabled |

## Keys

| Key | Action |
| --- | --- |
| `tab` / `shift+tab`, `1`–`4` | switch panels |
| `j` `k` / arrows | move |
| `enter` | Sites: go to its environments. Environments, Detail, pipeline: open the action menu |
| `r` | refresh the focused panel (bypasses the cache) |
| `:` | command palette |
| `g` | lazygit in the site's repo |
| `p` | pipeline view (see below) |
| `h` | command history (see below) |
| `O` | output log (command output, tables, errors) |
| `?` | help |
| `q`, `ctrl+c` | quit (asks first if jobs are running) |

On the selected environment:

| Key | Action | Terminus |
| --- | --- | --- |
| `d` | deploy to this environment (test, live) | `env:deploy` |
| `c` | clear cache | `env:clear-cache` |
| `b` | create backup | `backup:create` |
| `l` | clone content from another environment | `env:clone-content` |
| `m` | toggle git/sftp (dev and multidevs) | `connection:set` |
| `w` | wake | `env:wake` |
| `x` | drush, with your terminal attached (press Enter afterwards to return) | `remote:drush` |
| `o` | commit SFTP changes | `env:commit` |
| `n` | create multidev | `multidev:create` |
| `D` | delete multidev | `multidev:delete` |
| `u` | upstream updates (shown in Output) | `upstream:updates:list` |
| `v` | open in the browser (primary custom domain, else `*.pantheonsite.io`) | `domain:list` |

Rebind anything in the config under `keymap:` using the action names shown in
`?` help.

### Mouse

Anything with a key can also be clicked. A click selects a site, environment,
workflow or pipeline box; a double or right click acts like `enter` (on an
environment, the action menu). The key hints along the bottom, and in a
dialog's footer, are the keys themselves: clicking `c cache` is pressing `c`,
so every form and confirmation still applies. A URL (in the pipeline
footer) opens in your browser on a single click. In a dialog, click a menu entry
to run it, a palette entry to choose it, or a form field to focus it (toggles
flip, choices advance). A click outside a dialog cancels it, and the wheel
moves the selection as before.

While lazyterminus has the mouse, hold Shift to select and copy text.

### Action menu

`enter` on an environment (in the Environments or Detail panel, or on a box
in the pipeline view) opens a menu of what you can do with it, with live
counts ("Deploy 2 commits from dev", "Commit 2 changed files"). Actions that
don't apply are greyed out with the reason ("test and live only", "SFTP mode
only"). Choosing an entry, or pressing its key, runs the same flow as the key
itself, with the same confirmations; the last entry opens the command
palette for that environment.

### Deploys

Pantheon deploys promote **every** commit waiting in the environment below;
there is no per-commit selection. `d` shows exactly which commits will move,
asks for a deploy note, and offers clear cache, update DB, and (test only, off
by default) sync content from live. Deploys trigger Quicksilver hooks
(`drush cim`), so the confirmation names the target. The deploy runs in the
background; keep navigating while the Workflows panel shows progress.

### Pipeline view

`p` swaps the panels for the site's code pipeline: multidevs feeding dev,
then dev → test → live. Each box shows the environment's state, connection
mode, newest commit, and when it last changed; each hop shows how many
commits are waiting to move (`↑2`) or `✓` when nothing is.

```
╭─ dev ───────────────────╮        ╔═ test ══════════════════╗        ╭─ live ──────────────────╮
│ ● in sync               │   ↑2   ║ ◒ deploying 3/6         ║   ↑1   │ ↑ 1 commit waiting      │
│ git                     │━━━●━━━▶║ git                     ║───────▶│ git                     │
│ a1c9e0f PROJ-77: Add h… │        ║ c3e1a2b PROJ-75: Bump … ║        │ 39634da Test/live buil… │
│ committed 22h ago       │        ║ ███████░░░░░░░░░░░░ 21s ║        │ deployed 4d ago         │
╰─────────────────────────╯        ╚═════════════════════════╝        ╰─────────────────────────╯
```

While a deploy runs, dots flow along the hop, the target box shows its
progress (Pantheon workflow operations done/total), and the commits moving
are listed underneath. When it finishes, the box turns green with
"✓ landed 2 commits" for 15 minutes; a failure turns it red with the reason.
Confirming a deploy anywhere switches to this view so you can watch it land.

Every other action shows on its box too: "◐ clearing cache", "creating
backup", "waking", "switching to git", then "✓ cache cleared · just now" for
a few minutes, or "✗ creating backup failed" with the error underneath.
Under the graph, the selected environment's URLs come first (platform
domain and any custom domains; click one to open it, or press `v` for the
primary), then what's waiting to deploy, then its recent commits with author
and age.

Cloning content draws an arc over the boxes from source to target
(e.g. live → dev) with dots flowing while it copies. Actions started
elsewhere (the dashboard, another terminal) appear from Pantheon's workflow
list on the next poll.

The selection is shared with the Environments panel: `←`/`→` (or `j`/`k`) move it, every
action key (`d`, `c`, `x`, …) acts on the selected box, `enter` jumps to its
Detail panel, and `p` or `esc` goes back. Under 100 columns the chain stacks
vertically. The full capture is [docs/screen-pipeline.txt](docs/screen-pipeline.txt).

### History view

`h` lists every Terminus command lazyterminus has run this session, newest
first: background loads and polling as well as the commands behind actions,
deploys and the palette. Each row has the start time, ✓/✗ (◐ while running,
↗ for drush and other commands handed to your terminal), how long it took,
whether it was a read or a change, and the full command; a failure shows
Terminus's error under it. `t` cycles the time range (last 15 min, last hour,
this session, last 5 min), `f` toggles changes only, and `h` or `esc` goes
back.

It's kept in memory for the session only (the most recent 2000 commands) and
lists what actually ran: under `--dry-run`, intercepted commands are printed
at exit instead. Changes from earlier sessions are in the
[history log](#safety).

The Detail panel and the pipeline footer also list your last five changes
on the selected environment, read back from that log across sessions, in
plain words: "clear cache", "drush cim -y", "deploy …" (✓ ok, ✗ failed,
↗ ran in your terminal, where the exit isn't logged). It's local history:
only what this machine ran through lazyterminus, not your team's or the
dashboard's activity; the Workflows panel has Pantheon's side.

### Auto-wake

Terminus can't report whether an environment is asleep, so before drush and
clone on dev or a multidev, lazyterminus adds an `env:wake` step. You see it
in the confirmation and in the status line while it runs.

## Safety

Every Terminus command goes through one runner chain:

```
Guard → (DryRun) → History → concurrency limit → terminus
```

- **Guard** refuses any command that isn't on a known read-only list unless
  you confirmed exactly that command line. The confirmation creates a
  single-use approval for that argv. It's enforced in the runner, so no code
  path can skip it.
- **Protected environments** (live always, test by default): every mutating
  command requires typing the full `site.env`, e.g. `acme-web.live`.
- **Destructive commands** require typed confirmation on any environment:
  `env:wipe`, `multidev:delete`, `site:delete`, `import:*`,
  `backup:restore`, `env:clone-content` into test or live, `connection:set`
  to git with uncommitted changes, and any deploy to live. Add more with
  `extra_destructive_commands` (globs like `secret:*`). Aliases (`env:delete`,
  `import:db`, `deploy`, …) are resolved, so they get the same rules.
- **Never offered:** cloning content into live, or `--sync-content` on a live
  deploy.
- **Everything else that changes state**, including all plugin commands, gets
  a yes/no confirmation showing the exact command. Only `y` confirms; enter
  doesn't.
- `read_only: true` disables every mutating action.
- **History:** every executed mutating command is appended to
  `$XDG_STATE_HOME/lazyterminus/history.log` (default
  `~/.local/state/lazyterminus/history.log`) with timestamp, user, the full
  command line, and its result. If the log can't be written, the command
  doesn't run.
- `--dry-run` runs reads for real and prints mutating commands instead,
  including after you quit.

## Command palette

`:` fuzzy-searches every Terminus command, including installed plugins, built
from `terminus list --format=json` and cached until the Terminus version
changes. With an environment selected, commands that take `site_env` come
first (marked `◆`) with `site.env` filled in; from the Sites panel, site-level
commands come first with the site filled in.

Choosing a command opens a form for its remaining arguments and options,
built from Terminus's own definitions, and shows the full command line before
it runs. Commands that support `--format=json` render as a table in the Output
panel; others stream their output there. `remote:drush` and other interactive
commands get your terminal. Palette commands go through the same safety layer
as the keys.

## Configuration

`$XDG_CONFIG_HOME/lazyterminus/config.yaml` (default
`~/.config/lazyterminus/config.yaml`):

```yaml
terminus_path: terminus
lazygit_path: lazygit
sites:                      # optional allowlist; empty means all sites from site:list
  - name: acme-web
    repo: ~/sites/acme-web
  - name: acme-intranet
    repo: ~/sites/acme-intranet
repos:                      # local checkouts without limiting the site list
  acme-portal: ~/sites/acme-portal
protected_envs: [live, test] # live is always protected
read_only: false
deploy_defaults:
  clear_cache: true
  update_db: true
  sync_content: false
poll_interval: 10s           # workflow polling; 5s while something is running
max_concurrent: 4            # concurrent terminus processes
extra_destructive_commands: []
keymap: {}                   # e.g. { deploy: ["D"], delete_multidev: ["X"] }
```

Read results are cached: 30 s for environment data, 60 s for the rest, and 5 s
for workflows while a job is running. `r` bypasses the cache for the focused
panel.

## lazygit integration

- `g` opens lazygit in the selected site's repo (from `sites[].repo`) and
  refreshes everything when you come back, since a push changes what Pantheon
  has.
- For a multidev, the Detail panel shows whether a local branch of the same
  name exists and whether it's ahead of or behind the Pantheon remote (the
  remote whose URL is a `*.drush.in` codeserver), as of your last fetch.
  lazyterminus never fetches.
- `--env` accepts a bare environment name when run inside a mapped repo, and
  maps `master`/`main` to `dev`, so lazygit can launch it on the checked-out
  branch.

Add to `~/.config/lazygit/config.yml` (lazygit 0.45+; pick keys that don't
clash with yours):

```yaml
customCommands:
  # Open lazyterminus focused on the environment for the checked-out branch.
  # Uses the sites:/repos: mapping in lazyterminus's config to find the site.
  - key: "<c-p>"
    context: "global"
    description: "lazyterminus: open this branch's environment"
    command: "lazyterminus --env {{.CheckedOutBranch.Name | quote}}"
    output: terminal

  # Wake the matching multidev, then push the branch to Pantheon.
  # Set the site once per repo: git config lazyterminus.site acme-web
  - key: "<c-w>"
    context: "localBranches"
    description: "Wake matching multidev, then push to Pantheon"
    loadingText: "Waking multidev and pushing…"
    command: >-
      site=$(git config --get lazyterminus.site) &&
      branch={{.SelectedLocalBranch.Name | quote}} &&
      terminus env:wake "$site.$branch" &&
      git push "$(git remote -v | awk '/drush\.in/ {print $1; exit}')" "$branch"
    output: log
```

## Flags

| Flag | |
| --- | --- |
| `--mock` | run against built-in fixtures (scrubbed real Terminus output); simulated workflows, nothing sent anywhere |
| `--dry-run` | real reads; mutating commands printed instead of run |
| `--site NAME` | select a site on startup (default: the site whose repo contains the current directory) |
| `--env site.env` | focus an environment on startup; `env` alone inside a mapped repo |
| `--config PATH` | use another config file |
| `--version` | print the version |

## Development

```sh
make check            # go vet, golangci-lint, go test
make race
make mock             # run against fixtures
```

Tests never run the real Terminus. Parsers are tested against scrubbed real
output in `testdata/real/` (regenerate with `scripts/scrub_fixtures.py` from
raw captures of read-only commands). The mock runner adds the states in
`testdata/mock/scenario.json`, simulates latency and workflows that progress
over time, and records every mutating call so tests can assert exactly what
would have run. UI tests drive the whole app with
[teatest](https://github.com/charmbracelet/x/tree/main/exp/teatest) through
the same safety chain as the real binary.

Where Terminus behaves differently from what you might expect (no "asleep"
state, no Drupal version, labels in code logs, aliases, `--no-interaction`
answering "no"), see [NOTES.md](NOTES.md).

```
cmd/lazyterminus/   main, flags
internal/terminus/  Runner interface, exec/mock/limit/history/dry-run runners, models, parsers
internal/safety/    policy, destructive list, approvals, Guard
internal/catalog/   terminus list → palette catalog, cache, search
internal/jobs/      background job queue
internal/cache/     TTL cache
internal/config/    config loading, XDG paths
internal/ui/        root model, actions, palette, forms, modals, keymap, mouse
internal/ui/panels/ panel renderers
testdata/           scrubbed fixtures and the mock scenario
```
