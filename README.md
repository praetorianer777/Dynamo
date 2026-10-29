# Dynamo

Runs [Stator](https://github.com/praetorianer777/Stator),
[Armature](https://github.com/Cloudster1/Armature) and
[Exciter](https://github.com/Cloudster1/Exciter) together, tests them as one
and releases the combinations that pass.

- `versions.yaml` pins one released version of each tool, and of the one
  Kroki that draws diagrams for all three.
- `make up` starts all three from those images, with their databases, one
  object store, one Kroki and one Keycloak whose realm `dynamo`
  (`deploy/keycloak/realm.json`, the canonical copy) all three sign in
  through, and connects them to each other with `dynamo-connect`: Stator and
  Exciter reach Armature and each other, each tool's webhooks reach its
  partners, and a person who signs in to Stator or Exciter is connected to
  its partners as themselves.
- `make down` stops the stack and keeps its data; `make reset` deletes it.
- `docs/compatibility.md` holds the rules that let the tools release at their
  own pace.

Nothing is installed on the host: every tool and toolchain runs in a
container. `make help` lists the targets.

## On a fresh machine

The machine needs git, make and Docker with its Compose and buildx plugins:
Docker Engine on Linux, Docker Desktop elsewhere. Your user must be able to
reach Docker without sudo. Then:

```sh
git clone https://github.com/praetorianer777/Dynamo.git
cd Dynamo
make up
```

`make up` first runs `make check-host`. It stops when something the stack
needs is missing:
- Docker or one of its plugins;
- Compose 2.24 or newer;
- a free port for each tool, Keycloak and Mailpit.

It warns, without stopping, when the machine is small or slow:
- less than 4 GB of memory for Docker;
- less than 20 GB of free disk, while the images take about 11 GB;
- an arm64 machine. The tools publish amd64 images only, so they run
  emulated there.

Each message says what to do. The first start pulls every image and takes a
few minutes. It ends by printing the addresses; sign in as below.

## Signing in

Open any of the three and sign in with single sign-on as `alice` /
`alice password` (an administrator everywhere) or `bob` / `bob password` (a
member). `make up` prints the addresses; they and the versions are also in
`.cache/stack.env`.

To sign in through a Keycloak or realm of your own, set `OIDC_ISSUER` and
the tools' clients; `docs/keycloak.md` lists the settings and what the realm
must hold.

## On a server with Docker Compose

The same compose file runs the suite on one machine, without `make`:
`deploy/compose-secrets.sh` makes `deploy/.env` from `deploy/.env.example`
and generates every password left empty, then `docker compose up -d` starts
all three, with Keycloak and its database and HTTPS by Caddy, and connects
them. `docs/deploying-compose.md` has the steps; `make test-server` tests
exactly that.

## In a cluster

`deploy/charts/dynamo` installs the three with one `helm install`, at the
pinned versions, with the same wiring: a bundled Keycloak (or a realm of your
own: `keycloak.enabled=false`, `oidc.issuer`, `oidc.clients`), one S3 store,
fixed service names (`stator-api`, `armature-web`, ...) and dynamo-connect as
a post-install and post-upgrade Job, so a finished install is a connected
suite. Each tool is configured under its own key as its own chart documents;
set `<tool>.ingress.host` and the realm, the Job and the partners follow.

```bash
helm install dynamo deploy/charts/dynamo -n dynamo --create-namespace \
  --set stator.ingress.host=wiki.example.com \
  --set armature.ingress.host=issues.example.com \
  --set exciter.ingress.host=requirements.example.com \
  --set keycloak.ingress.enabled=true --set keycloak.ingress.host=sso.example.com
```

`make test-chart` installs it into a kind cluster and checks that the three
come up connected: the Job ran, one sign-in through the realm opens all three
and connects the person across them, every webhook delivers, and an upgrade
finds nothing left to change. Until
the tools publish their charts, `DYNAMO_CHARTS_FROM_SOURCE=1` packages them
from their main branches (`DYNAMO_<TOOL>_SOURCE=owner/repo@branch` for
another).

## What dynamo-connect does

It runs once after the three answer, through their admin APIs only, and
changes only what differs, so it can run again at any time (`make connect`):

1. Checks that each tool answers `GET /api/v1/meta` as itself, at the pinned
   version.
2. Sets Armature up with its owner if it has none yet, points every tool's
   sign-in at the realm with that tool's client, and lets the members of
   `DYNAMO_MEMBERS` into Armature; Stator and Exciter take them from their
   own bootstrap settings.
3. Connects Stator and Exciter to Armature and to each other, makes each
   sender's webhook for the topics its receiver asks for, pointed at the
   receiver's api inside the network, and stores the secret in the receiver.
4. Registers Stator and Exciter as partner apps in each partner that offers
   `oauth.token-exchange@1` (Stator in Armature and Exciter, Exciter in
   Armature and Stator), each with its Keycloak client and its address, and
   stores the app's client id and secret in the caller. A person who signs
   in to Stator or Exciter is then connected to its partners as themselves,
   with no token to paste. An app whose secret the caller still holds is left
   alone; otherwise it gets a new secret, or, in Stator, which issues none,
   is made anew.

The stack, the server setup and the chart switch the exchange on in Stator
and Exciter: `TOKEN_EXCHANGE=false` in the environment or in `deploy/.env`,
or `tokenExchange: false` under each tool's partner in the chart's values,
switches it off, and people then paste a token on their profile.

`make test-signin` signs alice in to all three through Keycloak on the
running stack, as a browser would: with her password once, then without. It
then asks Stator and Exciter, as alice, whether they reach their partners on
her behalf.

## Sample data

To have something to look at, `dynamo-connect sample-data` fills the running
tools with projects from GitHub, or with sparkplug, a small project built in
that needs no network:

```sh
make sample-data                            # sparkplug
make sample-data SOURCES=evcc,owner/repo    # GITHUB_TOKEN raises GitHub's rate limit
```

A source is one of `sparkplug`, `evcc`, `armature`, `stator`,
`exciter` and `dynamo`, or any public `owner/repo`; `owner/repo=KEY` chooses
its project key. Each becomes:

- an Armature project with its newest issues (`DYNAMO_SAMPLE_MAX_ISSUES`,
  500 by default, 0 for all), their labels, closed ones closed, each linked
  to its GitHub issue and related to the issues it mentions;
- a Stator space with the README as its home, the CHANGELOG and every
  Markdown file under `docs/`, nested by folder, each page linked
  to its source on GitHub and to the issues it mentions, and a page that says
  where the sample came from, linked to the projects. evcc brings its
  docs site as well (`evcc-io/docs`), under a page named after the site;
- an Exciter project with a requirement for each issue labelled feature or
  enhancement, traced to its issue and to the pages that mention it, and
  related to the requirements whose issues it mentions. Exciter's
  administrator is connected to their Armature and Stator accounts for this,
  with a token named `dynamo-sample-data` in each.

Links in the docs lead to what was imported: to another doc's page, also from
its address on the docs site or on GitHub, and to the Armature issue for the
project's issues. Anything else of the repository, such as an image, leads to
GitHub, and other addresses stay as they are.

To bring another project's docs site, give the source as JSON:

```sh
make sample-data SOURCES='[{"repo": "owner/tool", "docs": {"url": "https://docs.tool.example", "repo": "owner/tool-docs", "dir": "content/en"}}]'
```

Issues and requirements carry their GitHub number as `(#N)` in their titles
and pages link to their source, by which a rerun finds them, so it changes
nothing that is there already.
