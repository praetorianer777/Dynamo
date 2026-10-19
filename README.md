# Dynamo

Runs [Stator](https://github.com/praetorianer777/Stator),
[Armature](https://github.com/Cloudster1/Armature) and
[Exciter](https://github.com/Cloudster1/Exciter) together, tests them as one
and releases the combinations that pass.

- `versions.yaml` pins one released version of each tool.
- `make up` starts all three from those images, with their databases, one
  object store and one Keycloak whose realm `dynamo`
  (`deploy/keycloak/realm.json`, the canonical copy) all three sign in
  through, and connects them to each other with `dynamo-connect`: Stator and
  Exciter reach Armature and each other, and each tool's webhooks reach its
  partners.
- `make down` stops the stack and keeps its data; `make reset` deletes it.
- `docs/compatibility.md` holds the rules that let the tools release at their
  own pace.

Nothing is installed on the host: every tool and toolchain runs in a
container. `make help` lists the targets.

## Signing in

Open any of the three and sign in with single sign-on as `alice` /
`alice password` (an administrator everywhere) or `bob` / `bob password` (a
member). `make up` prints the addresses; they and the versions are also in
`.cache/stack.env`.

## What dynamo-connect does

It runs once after the three answer, through their admin APIs only, and
changes only what differs, so it can run again at any time (`make connect`):

1. Checks that each tool answers `GET /api/v1/meta` as itself, at the pinned
   version.
2. Sets Armature up with its owner if it has none yet, points its sign-in at
   the realm and lets the members of `DYNAMO_MEMBERS` in. Stator and Exciter
   get theirs from their own bootstrap settings.
3. Connects Stator and Exciter to Armature and to each other, makes each
   sender's webhook for the topics its receiver asks for, pointed at the
   receiver's api inside the network, and stores the secret in the receiver.

People still connect their own accounts across tools until the tools connect
them at sign-in (Cloudster1/Armature#301, #302, praetorianer777/Stator#430,
#461, #462, Cloudster1/Exciter#273, #274).
