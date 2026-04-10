A CLI tool for managing multiple Git repositories as a unified workspace. Define your repos in `gitjoin.txt` files, and Gitjoin will clone, pull, and prune them for you.

Install via:

```
go install github.com/bep/gitjoin@latest
```

## Why

If you have hundreds of Git repositories across different technologies and domains, keeping them in separate repositories makes sense — until you need to operate on or configure all (or a subset) of them as a whole.

Examples:

* Share a common `AGENTS.md` across all Go repositories.
* Share common [API keys](https://github.com/bep/firstupdotenv) across all AWS applications.
* Update dependencies in all Go repos (or a subset using `--paths "go/**/foo"`).
* Prompt an AI agent to make changes across repos and create PRs.

## Why not use Git Submodules instead?

Git Submodules track a specific commit in each sub-repo and embed that reference in the parent. This is the right tool when you need a **pinned, reproducible** dependency tree — but it's the wrong tool when your goal is a **workspace of independent repos** you want to keep up to date.

Gitjoin treats the listed repos as peers, not dependencies:

* No commit in the parent repo when a child repo changes.
* No detached-HEAD checkouts — every repo stays on its default branch.
* Adding or removing a repo is a one-line edit in `gitjoin.txt`, not a Git operation.
* Shared configuration files (`AGENTS.md`, `firstup.env`, …) sit alongside the repos without being wired into their history.

If you need version-pinning, use submodules. If you need a convenient umbrella for many repos you actively develop, Gitjoin is a better fit.

## Tree structure

```
.
├── go
│   ├── AGENTS.md
│   ├── apps
│   │   └── gitjoin.txt
│   ├── firstup.env
│   └── libs
│       └── gitjoin.txt
└── sites
    ├── AGENTS.md
    ├── firstup.env
    └── gitjoin.txt
```

* `gitjoin.txt` — one Git repository path per line (e.g. `github.com/bep/s3deploy`). Lines starting with `#` are comments.
* `firstup.env` — environment variables for that branch (see [firstupdotenv](https://github.com/bep/firstupdotenv)), typically referencing `op://` paths so you can commit this to Git.
* `AGENTS.md` — AI agent guide for that branch.
* Cloned repo content is automatically added to `.gitignore`.

## Usage

Running `gitjoin` from the workspace root syncs all repositories defined in `gitjoin.txt` files recursively: clones missing repos, pulls updates for existing ones, and removes repos no longer listed. Note that you can start at any subdirectory with a `gitjoin.txt` to sync just that subtree.

### Flags

| Flag | Description |
|------|-------------|
| `--force` | Force sync (see below) |
| `--quiet` | Suppress output |
| `--paths` | Glob filter for repo paths (e.g. `"go/**/foo"`) |

### Default behavior

| Condition | Action |
|-----------|--------|
| Repo on non-default branch | Skip, warn in summary |
| Repo with uncommitted changes | Skip, warn in summary |
| Clean repo on default branch | Pull |

### With `--force`

For repos that would normally be skipped:
1. Stash uncommitted changes (if any)
2. Switch to default branch
3. Pull
4. Unstash (if stashed)

If unstash fails due to conflicts, warn and leave stash intact.
