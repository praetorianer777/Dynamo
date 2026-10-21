# Deploying the suite

One Helm release installs Stator, Armature and Exciter at the versions in
`versions.yaml`, with a Keycloak holding the realm `dynamo` and one S3 store.
After every install and upgrade, the connect Job wires the three to each
other. `helm test` then checks from inside the cluster that they are
connected.

## What the cluster needs

- An ingress controller, and DNS names for Keycloak and the three tools.
- TLS certificates for those names: cert-manager with an issuer, or Secrets
  you create under the names the values give.
- A default StorageClass: each tool's PostgreSQL, Keycloak and the object
  store keep volumes.
- Pulls from `ghcr.io`: the images and charts are public.

## Install

1. Copy `deploy/charts/dynamo/values-cluster.yaml`, replace every
   `example.com` address, and replace every `CHANGE-ME` with a secret of your
   own. The object store's keys must be the same in `objectStore` and in each
   tool's `secrets`, and the chart refuses keys that differ.
2. Install the released chart:

   ```sh
   helm install dynamo oci://ghcr.io/praetorianer777/charts/dynamo \
     --version <version> -n dynamo --create-namespace -f values.yaml --timeout 30m
   ```

   Don't add `--wait`: the tools' own hooks run first, and the connect Job
   runs last of all. A finished install is a connected suite.
3. Check it:

   ```sh
   helm test dynamo -n dynamo --logs
   ```

   The test Pod has each tool send a test delivery on every webhook the
   connect Job made. When `check.signIn` names an account, it also signs that
   account in once and checks that it is then signed in to all three.

Each tool's administrator is kept in a Secret: `stator-bootstrap-admin`,
`exciter-bootstrap-admin` and `dynamo-armature-admin`. Keycloak's admin is
kept in `dynamo-keycloak-admin`.

## Upgrade

`helm upgrade` with a newer chart version runs the tools' migrations and then
the connect Job again, which changes only what is missing. Run `helm test`
again afterwards.

## Before you install: the same install in kind

`DYNAMO_FROM_RELEASE=1 make test-chart` does the same in a fresh local kind
cluster. It pulls the published chart and every image from its registry, with
nothing from this machine, then installs, runs `helm test` and upgrades.
