---
name: gh
description: GitHub workflow for this repo, read before any change. Creating issues, issue branches, running tests, opening PRs. Use this whenever you want to change a file, commit, push or open a pull request.
---

# GitHub workflow

Repo: `praetorianer777/Dynamo` · default branch: `main`

## Rules

- Every change needs an issue and a branch named `<type>/<issue>-<slug>`.
  Types: `feat fix chore docs refactor test perf ci build revert`.
- Never push to `main`. Never merge yourself; that is the user's decision.
- Agree on issues and PRs with the user before creating them.
- Always call `gh` non-interactively: `--title` / `--body-file`, never open the editor.
- `.claude/hooks/branch-guard.sh` enforces this; a violation is blocked, not commented on.

## Language

Everything in the repository is written in English: issues, pull requests, commit messages, code,
comments and documentation.

## The tools

Dynamo changes none of the tools. What it needs from Stator (`praetorianer777/Stator`), Armature
(`Cloudster1/Armature`) or Exciter (`Cloudster1/Exciter`) goes through an issue in that repository,
linked from the Dynamo issue. This account has read access to Armature and Exciter, so pull
requests there come from the forks `praetorianer777/Armature` and `praetorianer777/Exciter`.

## Recipe

```bash
gh auth status                       # preflight

gh issue list --limit 20             # does the issue already exist?
gh issue create --title "..." --body-file /tmp/body.md

gh issue develop 42 --name feat/42-umbrella-chart --base main --checkout

# ... work ...

./run-tests.sh                       # everything; the hook runs the quick subset before every push (DYNAMO_GATE=full for the stack)
git push -u origin HEAD

gh pr create --title "..." --body-file /tmp/pr.md   # body contains "Closes #42"
gh pr checks --watch
gh run view --log-failed             # when CI is red
```

## Pitfalls

- `gh issue develop` creates the branch on the remote and checks it out; no manual
  `git switch -c` needed.
- Branch slugs are lowercase ASCII: `feat/42-umbrella-chart`, not `feat/42-Umbrella-Chart`.
- Commit messages are Conventional Commits, lowercase, imperative, subject at most 72 characters,
  and describe the **effect** rather than the files touched.
- Every guard decision and gate duration is in `$(git rev-parse --git-common-dir)/branch-guard.log`.
- Parallel work happens in worktrees under `.claude/worktrees/`; each gets its own compose
  project and ports, so gates never collide.
