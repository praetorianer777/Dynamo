# Working on Dynamo

Dynamo runs [Stator](https://github.com/praetorianer777/Stator),
[Armature](https://github.com/Cloudster1/Armature) and
[Exciter](https://github.com/Cloudster1/Exciter) together from their released
images, tests them as one and releases the combinations that pass. It runs
nothing of its own in production: what it ships is the pinned versions, one
deployment of all three, and `dynamo-connect`, the job that connects the tools
to each other once through their admin APIs. People sign in to whichever tool
they open; the tools share one Keycloak realm, `dynamo`, whose canonical copy
is `deploy/keycloak/realm.json`. The rules the tools keep with each other are
in `docs/compatibility.md`.

## Every change hangs off an issue

Read `.claude/skills/gh/SKILL.md` before changing anything. Branches are
`<type>/<issue>-<slug>`, created with `gh issue develop`; changes land via pull
request with `Closes #N`, and the user merges. `.claude/hooks/branch-guard.sh`
enforces this and, before every push, runs the layers of `./run-tests.sh` that
the pushed files can affect: the shell tests, the format check and
dynamo-connect's Go tests by default, and the whole stack with
`DYNAMO_GATE=full`. A gate that does not finish blocks the push.

What a tool must change goes to that tool as an issue; Dynamo never patches a
tool's image or works around one in its own code.

## Nothing is installed on this machine

Every toolchain runs in a container, driven by the `Makefile`. Never install
Go, Node, Helm or a Postgres client on the host. If a tool is missing, add a
target that runs it in a container. Go is pinned to 1.26, as in the tools.

## The stack

`versions.yaml` pins one version of each tool; the compose stack pulls exactly
those images and never builds a tool. Each tool keeps its own databases,
Valkey and network, because each web image proxies to a service called `api`;
the apis and workers also join the network `suite`, where they are
`stator-api`, `armature-api` and `exciter-api`. Keycloak, object storage and
mail are shared.

## Style

- Comments say why, never what. Doc comments at most two lines.
- No en dashes and no typographic quotes, in code or in prose.
- Errors the user reads are sentences, and name what to do about it.
