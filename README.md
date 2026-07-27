# hr

A file-first RSS / blog reader. Articles live as plain markdown files
in a git-syncable vault. There is no database — `find`, `grep`, `git`,
and your editor are the UI.

```sh
hr init <name> [dir]   # scaffold a vault
hr use [dir]           # set/show the global default vault (~/.hrrc)
hr doctor              # check the vault for inconsistencies
hr sync                # fetch feeds, write articles + sidecars
hr mv <feed>... <group> # move feed folders into a group under feeds/
hr groups              # folders under feeds/ + feed/article/unread counts
hr list                # list articles (pretty, --tsv, --json)
hr read <path>...      # mark as read (sidecar mutation)
hr unread <path>...
hr fav <path>...       # toggle favorite
hr alias <path> [name] # local display label (no [name] = clear)
```

Nvim panel: `~/.dotfiles/nvim/lua/hr/init.lua`. `<leader>r` toggles a
sidebar with its own buffer keys (`<CR>`/`o`/`r`/`u`/`f`/`a`/`R`/`s`/`q`/`?`).

## Vault layout

```
<vault>/
  hr.toml                                # config (committed)
  feeds/<feed>/<date>-<slug>-<id>.md     # article content (committed)
  feeds/<feed>/<date>-<slug>-<id>.meta.toml   # mutable state (committed)
  .hr/                                   # gitignored
    cache.json                           # ETag / Last-Modified per feed
    raw/<feed>/<date>-<slug>-<id>.html   # original HTML, keyed by feed name
    err.txt                              # one line per error during sync
~/.hrrc                                  # global default vault: vault = "..."
                                          # (set/read via `hr use`)
```

The `<id>` segment is the first 8 hex chars of `sha1(GUID || URL ||
title)`, so the same item lands on the same path across machines.

### Grouping feeds into folders

Feed directories may be nested under `feeds/` to any depth, so a large
vault can be shelved by kind:

```
feeds/
  humans/matklad/…        # personal blogs
  sites/lobsters/…        # aggregators, project blogs
  books/classics/knuth/…  # hand-curated, never synced
```

**The directory is the source of truth.** `hr` finds a feed by its
directory *name* wherever it sits, so you regroup with plain `mv` /
`git mv` and nothing else has to change — no config edit, no re-sync, no
rewritten article paths. Feed directories `hr` never fetched (a shelf of
essays you assembled by hand) group exactly like synced ones.

`hr mv` does the move for you, in batches:

```sh
hr mv matklad danluu regehr humans
hr mv lobsters lwn sites
hr mv knuth turing books/classics
hr mv lobsters /                    # back to the top level
hr mv --dry-run matklad humans      # show the plan, touch nothing
```

The group is created if missing, and group folders a move leaves empty
are removed. The whole batch is validated before anything moves, so an
unknown feed or an occupied destination fails with the vault untouched.
Only directories move — articles, sidecars, ids and read state are
untouched, and it's a plain rename, so `git` records it as one.

For feeds declared in `hr.toml`, `hr mv` also updates their `group` key
in place, preserving every comment and the rest of the formatting
(`--no-config` skips it).

#### Reading a shelf or an author

`hr groups` summarizes the tree; `hr list` and `hr feed` take `--group`
and `--feed` filters, both repeatable:

```sh
hr groups                                      # counts per folder
hr list --group humans                         # includes humans/archive
hr list --group books --group humans/archive   # stacks (union)
hr list --group books,sites                    # comma form works too
hr list --group / --unread                     # top-level feeds only
hr list --feed matklad                         # one author
hr list --feed matklad,danluu                  # several authors (union)
hr list --feed matklad --group humans          # both must hold
hr feed --group sites --feed lobsters          # unread TSV, scoped
```

`--group` and `--feed` are each repeatable and union their own values,
while the two flags **intersect** with each other — so several shelves
or several authors widen the result, but combining a shelf with an
author narrows it. Both compose with `--unread`, `--tag`, `--since` and
the rest rather than replacing them.

`--group` matches by **subtree**, so `--group humans` covers
`humans/archive`; a bare `/` means the feeds root only. `--feed` matches
the feed name **exactly** — it's a name, not a path or a prefix.

`hr groups` counts each group's own feeds, not its subgroups, so the
numbers sum to the vault total instead of double-counting nested
shelves. `--tsv` output gained the group as a **trailing** seventh
column, so anything indexing fields 1-6 keeps working.

A directory under `feeds/` holding at least one file is a **feed**; one
holding only subdirectories is a **group**. Two directories sharing a
name are ambiguous — sync and `hr doctor` both report it rather than
guessing.

`group` in `hr.toml` is optional and does one thing: it decides where a
feed with no directory yet gets created on its first sync.

```toml
[[feeds]]
url   = "https://lobste.rs/newest.rss"
name  = "lobsters"
group = "sites"
```

Once the directory exists, its location on disk wins over `group`.

`.hr/raw/<feed>/` stays flat, keyed by feed name — it's a gitignored
cache, so moving a feed never has to touch it.

### Which vault does `hr` use?

Resolved in priority order: `-C <dir>` flag > `$HR_VAULT` > walking up
from the current directory for an `hr.toml` (like `git` finds `.git`,
so a cloned vault works as soon as you `cd` into it) > the global
default in `~/.hrrc` (`hr use <dir>` to set it, `hr use` to show it,
`hr use --clear` to unset it).

`hr doctor` checks a vault's `hr.toml` and feed directories for
inconsistencies (malformed sidecars, orphaned files, tombstone
contradictions, id collisions, undeclared feed dirs) without modifying
anything.

## Storage design

### Properties

- **Plain text everywhere.** Markdown body, YAML frontmatter, TOML
  config & sidecar, JSON cache. If `hr` disappears, the vault stays
  usable in any editor.
- **Immutable content, mutable state.** Article `.md` files are
  written once at fetch time and never touched again. Per-article
  state (`read`, `read_at`, `favorite`, `tags`, `alias`) lives in a
  sibling `.meta.toml` that only `hr read`/`unread`/`fav`/`alias`
  modifies. Small files, mergeable in git.
- **No DB lock-in.** Migrate the vault by `mv`. Bulk operations are
  `grep`/`find`/`fd`.
- **Git is the sync layer.** Commit the vault, push, pull on another
  machine; `hr` reads it without any cross-machine state.
- **Raw HTML archive.** `<vault>/.hr/raw/<id>.html` keeps the original
  feed body. If a future conversion bug or readability improvement
  warrants reprocessing, the source is local.

### Known limitations

- **Lossy HTML → markdown conversion.** Body goes through
  `JohannesKaufmann/html-to-markdown` plus `Kunde21/markdownfmt/v3`
  canonicalization plus our `cleanBody` (strips leading nav `<h1>`s).
  The raw HTML archive (above) is the recovery path.
- **No schema version field.** Adding sidecar/frontmatter fields is
  forward-compatible (zero values are fine); removing them silently
  orphans data. Bump `schema = N` if/when this matters.
- **Partial test coverage.** Feed-directory resolution, moves, config
  editing, group filtering, sync and tombstones are covered; sidecar
  I/O and alias toggling still aren't.

## Formatting

There's no Go library for markdown-aware hard column wrap (checked:
`Kunde21/markdownfmt` doesn't expose one; `yuin/goldmark` is a parser
toolkit; `muesli/reflow` is markdown-blind). So:

- **Canonicalize structure** during sync via `markdownfmt.Process`.
  This normalizes heading style, list markers, link refs, and code
  fences across feeds.
- **No hard wrap on disk.** Paragraphs from `html-to-markdown` stay as
  produced (typically one line per paragraph). This keeps `.md` files
  source-of-truth and avoids reformatting churn on resyncs.
- **Display wrap in nvim** via a markdown filetype autocmd
  (`wrap` + `linebreak` + `breakindent`). User config; not shipped by `hr`.

Same trade-off `nom` makes — it doesn't pre-wrap either, just renders
nicely at view time with `glamour`. `hr`'s "view time" is nvim.

## Refresh resolution

`hr sync` is idempotent and **never overwrites user-mutable state.**

| What                         | Created by           | Modified by                  | Sync behavior on re-run      |
| ---------------------------- | -------------------- | ---------------------------- | ---------------------------- |
| `hr.toml`                    | `hr init`            | user                         | untouched                    |
| `feeds/.../*.md`             | first `hr sync`      | user (if they choose)        | **skipped if exists**        |
| `feeds/.../*.meta.toml`      | first `hr sync`      | `hr read`/`fav`/`alias`/user | **untouched** after creation |
| `.hr/cache.json`             | `hr sync`            | `hr sync`                    | overwritten each run         |
| `.hr/raw/<feed>/<name>.html` | first `hr sync`      | —                            | **skipped if exists**        |
| `.hr/err.txt`                | `hr sync` (on error) | —                            | appended                     |

Implications:

- **You can edit article files freely.** Sync won't clobber them.
  Frontmatter additions (`summary: "..."`, custom tags) survive.
- **Sidecar state persists across resyncs.** Mark something read,
  resync 100 times, still read.
- **Aliases are committed.** They live in `.meta.toml`, which is in
  the vault, which is in git → aliases sync to other machines. If you
  want truly machine-local aliases, the design needs to move them to
  `.hr/aliases.json` (gitignored). Not done yet.
- **Stale content stays stale.** If a feed corrects a published item
  (typo fix, retitling), `hr` keeps the original on disk — same GUID
  → same hash → same path → skipped. To force a fresh copy, delete
  the `.md` and `.meta.toml` and run `hr sync`. A future `hr refresh
<path>` command would do this targeted.
- **Cross-machine merge conflicts** can only happen on `.meta.toml`
  (everything else is either immutable or gitignored). The files are
  small and line-oriented; most conflicts auto-merge. Real
  collisions need human attention via `git`.

The bias is **stability over freshness**: existing files are the
source of truth, sync only adds new ones.

-- impl cache
