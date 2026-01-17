I have hundreds of Git repositories hosted on GitHub. Different technologies (Go libraries/apps, web apps/sites etc.) and domains. Having them in separate repositories makes perfect sense. Until I want to operate on or configure all or a subset of them as a whole.

Configuration examples could be common `AGENTS.md` for all Go repositories, or common [API keys](https://github.com/bep/firstupdotenv) for all AWS applications.

Operation examples could be update dependencies in all Go repos (or a selection using a `--paths `go/**/foo` flag), prompt an AI agent to make certain changes/fixes and then create PRs in the respective repos.

This would obviously be a great tool for getting dev teams up and running with the same project structure(s). One example Git view could look like:

```
.
├── go
│   ├── AGENTS.md
│   ├── apps
│   │   └── gitjoin.txt
│   ├── firstup.env
│   └── libs
│       └── gitjoin.txt
└── sites
    ├── AGENTS.md
    ├── firstup.env
    └── gitjoin.txt
```

* `gitjoin.txt` contains one Git repository path per line (e.g. `github.com/bep/s3deploy`). Lines starting with `#` are comments.
* `firstup.env` would contain environment variables needed for that branch (see [firstupdotenv](https://github.com/bep/firstupdotenv), typically using `op://Dev/myapp/keys` for API keys, so we can commit this structure to Git.
* `AGENTS.md` would be the AI agent guide for that branch.
* The cloned content will be in `.gitignore`.

I think it would make sense to have some built in commands in the tool itself. Installable via `go install github.com/bep/gitjoin@latest`.

The default command (`get`?) would _update_ the tree according to `gitjoin.txt`: Remove repos no longer in use, clone repos not existing locally, update repos.

## Default command behavior

### Without flags

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
