# glossa-platform

Helm chart for Glossa's platform (RFC 0002 §3, §11): **glossa-server**
(control plane and the `/v1` API), **glossa-edge** (the stateless
delivery plane) and **Studio** (the web app). Postgres is either
external (DSNs from Secrets) or, with `postgres.enabled`, one plain
Postgres StatefulSet in the release's namespace, the Klarlabs pattern
(see [In-namespace Postgres](#in-namespace-postgres)). Object storage is
either external S3 or, with `minio.enabled`, one MinIO in the release's
namespace (see [In-namespace MinIO](#in-namespace-minio)). With
`backup.enabled`, CronJobs dump Postgres and mirror MinIO to an rclone
remote outside the cluster every night and restore the newest dump every
week (see [Backups](#backups)).

The v0.3 chart was deleted with v0.3 (retired 2026-10-09).

```text
                  ┌─────────────── traefik (websecure) ────────────────┐
<studio>/v1/*     │→ glossa-server :8080 ──→ Postgres (glossa_app, RLS) │
<studio>/ + /_astro/* │→ site      :8080     object storage (read-write)│
<studio>/*        │→ studio        :8080                                │
<api>/v1/* (opt.) │→ glossa-server :8080     SMTP (optional)            │
<cdn>/v1/*        │→ glossa-edge   :8081 ──→ object storage (read-only) │
                  └─────────────────────────────────────────────────────┘
database hooks (pre-install, or post-install with postgres.enabled; pre-upgrade):
  postgres-init (owner, database) → migrate (schema owner) → postgres-app-login (glossa_app LOGIN)
post-install/post-upgrade hook: MinIO bootstrap (bucket, users; minio.enabled)
CronJobs (backup.enabled): postgres-backup, minio-backup → rclone remote; postgres-restore-test
```

What it renders:

| Resource | server | edge | studio | notes |
|---|---|---|---|---|
| Deployment + Service (`http`) | ✓ | ✓ | ✓ | `<fullname>-server`, `-edge`, `-studio` |
| PodDisruptionBudget | ✓ | ✓ | ✓ | `maxUnavailable: 1` |
| NetworkPolicy (ingress + egress) | ✓ | ✓ | ✓ | see [Network policies](#network-policies) |
| IngressRoute (traefik) | api host | cdn host | studio host (+ `/v1` → server) | plus an http→https redirect route |
| Certificate (cert-manager) | ✓ | ✓ | ✓ | one per host |
| Job (hook) + its NetworkPolicy | migrations | | | `<fullname>-migrate` |
| HorizontalPodAutoscaler | | optional | | `edge.autoscaling.enabled` |
| ServiceMonitor | optional | optional | | `metrics.serviceMonitor.enabled` |

With `minio.enabled` it adds `<fullname>-minio`: a StatefulSet with a
Longhorn volume, a ClusterIP and a headless Service (S3, 9000), a
PodDisruptionBudget and a NetworkPolicy; the bootstrap Job
`<fullname>-minio-bootstrap` (hook) with its NetworkPolicy; and the
`helm test` Pod `<fullname>-minio-test` with its NetworkPolicy.

With `postgres.enabled` it adds `<fullname>-postgres`: a StatefulSet with
a Longhorn volume, a ClusterIP and a headless Service (5432), a
PodDisruptionBudget and a NetworkPolicy; the hook Jobs
`<fullname>-postgres-init` and `<fullname>-postgres-app-login` with their
NetworkPolicy; and the `helm test` Pod `<fullname>-postgres-test`. With
`backup.enabled`: the CronJobs `<fullname>-postgres-backup`,
`<fullname>-minio-backup` and `<fullname>-postgres-restore-test`, each
with a NetworkPolicy.

Every pod meets PodSecurity **restricted**: non-root (65532 for the Go
images, the MinIO mirror and MinIO itself, 101 for Studio's nginx, 70
for Postgres and its Jobs), `seccompProfile: RuntimeDefault`, all
capabilities dropped, no privilege escalation, **read-only root
filesystem** (an `emptyDir` at `/tmp`), no service account token, no
service-link env vars. Probes: `GET /livez` (startup, liveness) and
`GET /readyz` (readiness) on the server and edge, `GET /healthz` on
Studio, `pg_isready` on Postgres. The server drains on SIGTERM within
`server.shutdownTimeout`, below `terminationGracePeriodSeconds`. Nothing
installs packages at runtime: the backup Jobs pair the Postgres image
with a pinned rclone image instead of `apk add rclone` (which needs root
and a writable root filesystem).

## Images

Built and pushed to GHCR by
[`.github/workflows/release-platform.yml`](../../../.github/workflows/release-platform.yml)
on a `v*.*.*` tag (see [Cutting a release](#cutting-a-release)); pull
requests only build them (`.github/workflows/platform.yml`).

| Image | Dockerfile | Base (pinned by digest) | User | Port |
|---|---|---|---|---|
| `ghcr.io/klarlabs-studio/glossa-server` | `platform/Dockerfile.server` | `gcr.io/distroless/static-debian12:nonroot` | 65532 | 8080 |
| `ghcr.io/klarlabs-studio/glossa-edge` | `platform/Dockerfile.edge` | `gcr.io/distroless/static-debian12:nonroot` | 65532 | 8081 |
| `ghcr.io/klarlabs-studio/glossa-studio` | `studio/Dockerfile` | `nginxinc/nginx-unprivileged:1.29-alpine` | 101 | 8080 |
| `ghcr.io/klarlabs-studio/glossa-site` | `site/Dockerfile` | `nginxinc/nginx-unprivileged:1.29-alpine` | 101 | 8080 |

All three build from the repository root, e.g.
`docker build -f platform/Dockerfile.server .`. A release tags each one
`v<semver>`, `sha-…` and `latest` — but a deployment pins the
**digest**, which is the only immutable reference and the one thing the
three tags cannot give you. `image.requireDigest` makes the chart refuse
to render an unpinned Glossa image at all.

**Studio at runtime.** The image renders `GLOSSA_STUDIO_*` variables at
start, so one image serves every environment: `/config.json`
(`apiBaseUrl`, `edgeUrl`, `environment`) and the CSP's `connect-src`
(`GLOSSA_STUDIO_CSP_CONNECT_SRC`). The CSP is strict: `default-src
'none'`, scripts, styles and fonts from `'self'` only, no inline code, no
eval, `frame-ancestors 'none'`. Studio's `/v1` is expected on its own
origin; the chart routes it to glossa-server. nginx answers `/v1` itself
with a 502 problem if that routing is missing. With `docker run
--read-only`, mount a tmpfs at `/tmp` (startup fails loudly otherwise).

Studio also serves the in-product editor at `/overlay/v1/overlay.js`, with
`/overlay/v1/overlay.json` (`{ version, integrity }`) beside it (RFC 0004
§5.1). Preview deployments of a product load that script cross-origin and
check the hash they pinned at build time, so these two paths — and only
these — answer with `Access-Control-Allow-Origin: *` and a cross-origin
`Cross-Origin-Resource-Policy`; the script is cached for five minutes, the
JSON not at all. Nothing about a tenant is in either.

## Cutting a release

A release is a tag. Everything else follows from it.

1. **Agree the version.** `Chart.yaml`'s `appVersion` is the version the
   chart deploys: every `<component>.image.tag` defaults to
   `v<appVersion>`, the tag Kiln pushes (`v0.4.0` for `appVersion:
   "0.4.0"`). Bump `appVersion` and land that on `main`; it is the only
   version bumped by hand. The images take theirs from the tag:
   [`.kiln.yaml`](../../../.kiln.yaml) passes `VERSION: "${kiln.version}"`
   and `REVISION: "${kiln.sha}"` (kiln ≥ 0.7.0), so every binary reports
   the release and commit it was built from.
   [`ci/check-release-version.sh`](ci/check-release-version.sh) checks those
   args, and the tag against `appVersion`; CI runs it on every chart change.
2. **Check, tag and push it:**

   ```sh
   deploy/charts/glossa-platform/ci/check-release-version.sh v0.5.0
   git tag v0.5.0 && git push origin v0.5.0
   ```

3. **Kiln publishes** (owner decision, 2026-10-05: Kiln, not GitHub
   Actions). On the tag, Kiln re-proves the commit with warden, builds the
   three images for `linux/amd64`, pushes them to
   `ghcr.io/klarlabs-studio/glossa-{server,edge,studio}` tagged `sha-…`,
   `v0.5.0` and `latest`, and signs each digest with the fleet's cosign key
   (`KILN_COSIGN_KEY=k8s://signing/cosign-key`), the key RollOps verifies
   at apply time. A box that watches the repository builds the tag by
   itself; by hand:

   ```sh
   KILN_COSIGN_KEY=k8s://signing/cosign-key \
     kiln run --sha v0.5.0 --event tag --ref refs/tags/v0.5.0
   kiln verify ghcr.io/klarlabs-studio/glossa-server:v0.5.0 --key cosign.pub
   ```

   `.github/workflows/release-platform.yml` no longer runs on tags; it stays
   as a manual (`workflow_dispatch`) fallback.

4. **Make the packages public (first release to a new registry owner
   only).** The first push to a GHCR owner creates `glossa-server`,
   `glossa-edge` and `glossa-studio` as *internal* packages, and the
   cluster pulls anonymously. For each package, in GitHub (the package's
   settings; there is no API for this): set **Change package visibility**
   to **Public**, and under **Manage Actions access** add
   `klarlabs-studio/glossa` with the **Write** role (the
   `workflow_dispatch` fallback needs it). Once per package, not per
   release.
5. **Take the digests** with [`ci/image-digests.sh`](ci/image-digests.sh).
   It writes the tag that exists in the registry (Kiln's `v0.5.0`, falling
   back to the bare `0.5.0` the GitHub workflow wrote) beside each digest,
   so pod specs name a real tag, and it checks that each image pulls
   **anonymously**, failing with the step above when one does not:

   ```sh
   ci/image-digests.sh v0.5.0 > values-images.yaml
   ```

   The file is **not** committed: a checked-in copy goes stale the moment an
   image is republished. `examples/values-klarlabs.yaml` keeps its
   `digest: ""` placeholders; the digests come on top of it, as a second
   `-f`.

6. **Deploy.** The digests file goes last, so it wins:

   ```sh
   helm upgrade --install glossa-platform deploy/charts/glossa-platform \
     --namespace glossa-platform \
     -f deploy/charts/glossa-platform/examples/values-klarlabs.yaml \
     -f values-images.yaml \
     --timeout 15m
   ```

   **The first install runs without `--wait`** — the database hooks
   bootstrap the app login and the server pods cannot become ready before
   they finish. See [First install](#first-install) step 7 and
   [Order of the database hooks](#order-of-the-database-hooks) for why,
   and for the `kubectl rollout status` that follows it. Later upgrades
   take `--wait`. Klarlabs rolls out through RollOps instead; see
   [Deploying with RollOps](#deploying-with-rollops), which needs the
   same digests.

`workflow_dispatch` with the tag name republishes a version — after a
failed run, say. It produces exactly what the tag push would, so the
digests may change even though the version does not; re-take them.

The chart itself is not published to an OCI registry yet: install it from
a checkout of the tag.

## First install

Order matters: **create the Secrets → install → the database hooks run
(owner and database, migration, glossa_app's LOGIN) → the server pods
become ready.** With an external database the hooks run pre-install,
before any other resource of the release exists, so the database and its
Secrets must already be in place. With `postgres.enabled` the release
creates the database, so on the first install they run post-install (see
[Order of the database hooks](#order-of-the-database-hooks)).

1. **Cluster prerequisites.** traefik (k3s ships it), cert-manager with a
   `ClusterIssuer` named `letsencrypt-prod` (or set
   `ingress.certManager.*`). Optional: Prometheus Operator for
   `ServiceMonitor`s.
2. **Namespace**, enforcing the restricted profile:

   ```sh
   kubectl create namespace glossa-platform
   kubectl label namespace glossa-platform \
     pod-security.kubernetes.io/enforce=restricted \
     pod-security.kubernetes.io/audit=restricted \
     pod-security.kubernetes.io/warn=restricted
   ```

3. **DNS.** Point `hosts.studio`, `hosts.cdn` and, if you set it,
   `hosts.api` at the ingress's public address. cert-manager's HTTP-01 challenge needs them
   to resolve before the Certificates can be issued.
4. **Database and roles** (Postgres 16).
   - **In-namespace (`postgres.enabled`, the Klarlabs pattern):** nothing
     to do in Postgres. The hooks create the schema owner, the database
     and the backup role, and give `glossa_app` its login after the
     migration has created it. Only the Secrets of step 6 are needed.
   - **External** (`postgres.enabled: false`): the schema owner runs
     migrations and must have `CREATEROLE` but must **not** be a
     superuser; the app role must be `NOSUPERUSER NOBYPASSRLS` (the server
     refuses to start otherwise). The first migration creates `glossa_app`
     and `glossa_system` as `NOLOGIN` only if they are missing, so create
     `glossa_app` with its login up front and the first install comes up
     in one go:

     ```sql
     CREATE ROLE glossa_owner LOGIN CREATEROLE PASSWORD '…';
     CREATE DATABASE glossa OWNER glossa_owner;
     CREATE ROLE glossa_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD '…';
     ```

     Backups of an external database are its provider's.
5. **Bucket and credentials.** External storage: create the bucket (and
   prefix, if shared) and two credentials, read-write for glossa-server
   and **read-only** for glossa-edge. With `minio.enabled`, create only
   the three MinIO Secrets below; the bootstrap Job creates the bucket and
   a MinIO user for each of the two credential Secrets after the install.
6. **Secrets** (names are the defaults of the example values; any name
   works, see [Values](#values)):

   ```sh
   NS=glossa-platform
   # In-namespace Postgres: one key, `password`, per role. Hex, because the
   # chart puts them into keyword/value DSNs (no ' or \ allowed).
   for s in glossa-pg-superuser glossa-pg-owner glossa-pg-app glossa-pg-backup; do
     kubectl -n $NS create secret generic $s --from-literal=password="$(openssl rand -hex 24)"
   done
   # Backups: rclone.conf and the files it references (Klarlabs: the Storage
   # Box's SSH key and known_hosts), mounted at /etc/rclone. Klarlabs copies
   # v0.3's (see Backups).
   kubectl -n glossa get secret rclone-config -o json \
     | jq '{apiVersion, kind, type, data, metadata: {name: .metadata.name}}' \
     | kubectl -n $NS apply -f -
   # External Postgres instead: the two DSNs.
   kubectl -n $NS create secret generic glossa-db-app \
     --from-literal=DATABASE_URL='postgres://glossa_app:…@<host>:5432/glossa?sslmode=require&pool_max_conns=20'
   kubectl -n $NS create secret generic glossa-db-owner \
     --from-literal=MIGRATION_DATABASE_URL='postgres://glossa_owner:…@<host>:5432/glossa?sslmode=require'
   # Keep this value: it seals enrolled TOTP secrets.
   kubectl -n $NS create secret generic glossa-auth \
     --from-literal=GLOSSA_AUTH_SECRET="$(openssl rand -base64 32)"
   # keyId=base64(32-byte Ed25519 seed); runtimes pin the public key.
   kubectl -n $NS create secret generic glossa-release-signing \
     --from-literal=GLOSSA_RELEASE_SIGNING_KEYS="k1=$(openssl rand -base64 32)"
   # Only with an SMTP server (see Mail):
   kubectl -n $NS create secret generic glossa-smtp \
     --from-literal=GLOSSA_SMTP_USERNAME='…' --from-literal=GLOSSA_SMTP_PASSWORD='…'
   kubectl -n $NS create secret generic glossa-s3-rw \
     --from-literal=GLOSSA_S3_ACCESS_KEY_ID='…' --from-literal=GLOSSA_S3_SECRET_ACCESS_KEY='…'
   kubectl -n $NS create secret generic glossa-s3-ro \
     --from-literal=GLOSSA_S3_ACCESS_KEY_ID='…' --from-literal=GLOSSA_S3_SECRET_ACCESS_KEY='…'
   ```

   With `minio.enabled` the chart makes glossa-s3-rw and glossa-s3-ro MinIO
   users, so generate them, and add MinIO's root credentials (never used
   by glossa itself). MinIO wants access keys of at least 3 and secret keys
   of 8–40 characters; the three access keys must differ:

   ```sh
   kubectl -n $NS create secret generic glossa-minio-root \
     --from-literal=MINIO_ROOT_USER=glossa-root \
     --from-literal=MINIO_ROOT_PASSWORD="$(openssl rand -hex 20)"
   kubectl -n $NS create secret generic glossa-s3-rw \
     --from-literal=GLOSSA_S3_ACCESS_KEY_ID=glossa-server \
     --from-literal=GLOSSA_S3_SECRET_ACCESS_KEY="$(openssl rand -hex 20)"
   kubectl -n $NS create secret generic glossa-s3-ro \
     --from-literal=GLOSSA_S3_ACCESS_KEY_ID=glossa-edge \
     --from-literal=GLOSSA_S3_SECRET_ACCESS_KEY="$(openssl rand -hex 20)"
   ```

   Back up `GLOSSA_AUTH_SECRET` and the signing seeds outside the cluster.
   Percent-encode special characters in DSN passwords.
7. **Install.** With `postgres.enabled`, the first install goes
   **without `--wait`**, then waits for the rollout:

   ```sh
   helm upgrade --install glossa-platform deploy/charts/glossa-platform \
     --namespace glossa-platform -f my-values.yaml -f values-images.yaml \
     --timeout 15m
   kubectl -n glossa-platform rollout status deployment/<fullname>-server --timeout 10m
   ```

   `values-images.yaml` pins the three images to the digests the release
   published ([Cutting a release](#cutting-a-release)); without it the
   chart deploys the `v<appVersion>` tags.

   Helm always waits for hook Jobs, so the command returns once
   postgres-init, the migration and postgres-app-login have succeeded.
   `--wait` would wait for the server pods *before* running the
   post-install hooks, and they cannot become ready before `glossa_app`
   has its login: the install would hang until `--timeout`. Until the
   hooks are done the server pods restart; that is expected. Every later
   `helm upgrade` takes `--wait` (the hooks run pre-upgrade).

   With an external database, `--wait --timeout 10m` works from the
   start: the migration runs pre-install and server pods start after it.

   A failed hook Job is kept for `kubectl logs job/<fullname>-postgres-init`
   (or `-migrate`, `-postgres-app-login`). With `minio.enabled`, the
   MinIO bootstrap Job creates the bucket and users after the install
   (neither server nor edge needs them to become ready);
   `kubectl logs job/<fullname>-minio-bootstrap` if it fails. The whole
   install, Longhorn volume attach included, has to fit in `--timeout`.
8. **Verify:** `helm test glossa-platform -n glossa-platform` checks, with
   `postgres.enabled`, that `glossa_app` connects and can `SET ROLE` to
   no superuser, BYPASSRLS or CREATEROLE role, and with `minio.enabled`
   that the edge's credentials read the bucket but cannot write or delete.
   With backups, run the first backup and the restore drill now rather
   than waiting for the weekend ([Backups](#backups)). `https://<studio>`
   loads and signs in, `https://<api>/v1/…` answers (`<api>` is `<studio>`
   when `hosts.api` is empty), `https://<api>/.well-known/glossa-audit-keys.json`
   returns the audit key set,
   `https://<cdn>/v1/<key>/production/manifest.json` answers 404 for an
   unknown key. `/metrics`, `/livez` and `/readyz` are not routed
   publicly.

Upgrades run the same hooks before the new server version starts; there
is no down-migration step in the chart. Rotating signing keys: add the
new key to the Secret (both sign), move runtimes to it, then move the old
one to `release.retiredKeys` (platform/README.md, "Release").

Uninstall leaves the hook NetworkPolicies (migration, Postgres bootstrap,
MinIO bootstrap and the tests) and never touches the database, the bucket,
the backups or the Secrets. The PersistentVolumeClaims
(`data-<fullname>-postgres-0`, `data-<fullname>-minio-0`) are kept too;
deleting one deletes its data (Longhorn's reclaim policy is `Delete`).

## In-namespace Postgres

`postgres.enabled` runs one Postgres per product namespace, the Klarlabs
pattern every product uses (v0.3's `glossa` namespace included): a
single-replica StatefulSet, no operator. glossa-server, the migration and
bootstrap Jobs, the backup Job and the test Pod reach it on 5432;
nothing else does.

**Image and version.** `postgres.image` is `postgres:16.15-alpine`,
pinned by digest: Postgres 16, v0.3's major (v0.3 runs 16.14; a minor
bump is a plain image change). Every Job that talks to Postgres uses the
same image, so `pg_dump`, `psql` and the restore drill's throwaway server
always have the server's major version (see [Backups](#backups) for why
that matters). A **major** upgrade is not an image change: the data
directory is incompatible, so dump, install the new major on a new
volume, restore.

**Pod.** uid/gid 70 (the alpine image's `postgres` user), read-only root
filesystem with `emptyDir`s for `/var/run/postgresql` and `/tmp`, data in
`/var/lib/postgresql/data/pgdata` (a subdirectory: the volume's root is
not owned by uid 70). The image's entrypoint runs `initdb` on an empty
volume and creates only the superuser. `pg_isready` probes; the startup
probe allows ten minutes for crash recovery. Its STOPSIGNAL is SIGINT (a
fast shutdown) within `terminationGracePeriodSeconds: 60`.

**Storage.** `longhorn` (3 replicas) by default: v0.3's database once
lived on a one-node `local-path` volume, so losing that node lost the
database. `postgres.persistence` is a volumeClaimTemplate and immutable
after install: grow the PVC (`data-<fullname>-postgres-0`) instead. The
PVC is retained when the StatefulSet is deleted or scaled to 0. The
PodDisruptionBudget (`maxUnavailable: 1`) lets a node drain move the pod;
the server is unavailable meanwhile.

**Roles.** Nothing is generated by the chart; every password comes from
an existing Secret (key `password`).

| Role | Created by | Attributes | Used by |
|---|---|---|---|
| `postgres.superuser.username` (`postgres`) | initdb | superuser | Postgres itself and the two bootstrap Jobs, nothing else |
| `postgres.owner.username` (`glossa_owner`) | postgres-init | `LOGIN CREATEROLE`, never superuser or BYPASSRLS; owns the database | the migration Job |
| `glossa_app` | the first migration (`NOLOGIN`, as the owner, who so holds ADMIN OPTION on it) | `LOGIN` + password from postgres-app-login; never superuser, BYPASSRLS, CREATEROLE, CREATEDB | glossa-server (FORCE RLS binds it) |
| `glossa_system` | the first migration | `NOLOGIN`; `glossa_app` may `SET ROLE` to it | the server's system scope |
| `backup.postgres.role.username` (`glossa_backup`) | postgres-init (backups on) | `LOGIN BYPASSRLS`, member of `pg_read_all_data`, nothing else | the backup Job's `pg_dump` |

The backup role exists because nobody else can dump without being a
superuser: the tables `FORCE ROW LEVEL SECURITY`, which binds their owner
too, and `pg_dump` refuses a table whose policies would filter it
(`query would be affected by row-level security policy`). It can read
everything and change nothing, which is what the pod holding the
off-site credentials should have.

### Order of the database hooks

| Weight | Hook | Connects as | Does |
|---|---|---|---|
| -10 | NetworkPolicies of the hook pods | | DNS and Postgres egress |
| -5 | `<fullname>-postgres-init` | superuser | waits for Postgres; owner role and password; database owned by it; backup role; `glossa_app`'s password if the role exists already |
| 0 | `<fullname>-migrate` | owner | `glossa-server -migrate=only`; the first migration creates `glossa_app` and `glossa_system` `NOLOGIN` |
| 5 | `<fullname>-postgres-app-login` | superuser | `ALTER ROLE glossa_app LOGIN PASSWORD …` |

They run **post-install** on the first install (the StatefulSet is a
release resource and does not exist before it) and **pre-upgrade** on
every upgrade (before new server pods roll). With an external database
only the migration runs, pre-install and pre-upgrade. Both bootstrap Jobs
are idempotent (`files/postgres-bootstrap.sh`): they converge on the
state above, so a rotated password in a Secret is applied by the next
`helm upgrade`.

Why hook Jobs rather than an initdb script in a ConfigMap: an initdb
script runs once, on an empty volume, and never again, so it cannot
apply a rotated password, cannot run *after* the migration that creates
`glossa_app`, and a failure midway leaves an initialized data directory
the entrypoint never retries (a half-bootstrapped database). It would
also put every role's password into the Postgres pod. The Jobs re-run on
every upgrade, fail loudly and are kept for `kubectl logs`.

Enabling `postgres.enabled` on an existing release is not an upgrade
path: the pre-upgrade hooks would wait for a database that does not
exist yet. Move the data with a dump and restore instead.

**DSNs.** With `database.app.secretName` and
`database.migration.secretName` unset, the server and the migration Job
get keyword/value DSNs for `<fullname>-postgres:5432` built from the
owner's and `glossa_app`'s passwords (Kubernetes expands `$(VAR)`, so the
password never lands in a manifest): `host=… dbname=glossa
user=glossa_app password='…' sslmode=disable pool_max_conns=20`.
Keyword/value needs no percent-encoding, only no `'` or `\`, which
postgres-init refuses. `sslmode=disable` is the in-namespace trade-off
MinIO makes too: the traffic stays on the pod network and NetworkPolicy
admits only the listed pods. Setting a `database.*` Secret overrides the
built DSN.

**Rotating passwords.** Owner, `glossa_app`, backup role: change the
Secret, `helm upgrade` (postgres-init applies it before new pods roll),
then `kubectl rollout restart deployment/<fullname>-server` for the app
password. The superuser's `POSTGRES_PASSWORD` only matters at initdb;
change it inside the pod (the local socket trusts `postgres`), then in
the Secret:

```sh
kubectl -n glossa-platform exec -it <fullname>-postgres-0 -- \
  psql -U postgres -c "ALTER ROLE postgres PASSWORD 'new-hex-password'"
```

**psql.** `kubectl -n glossa-platform exec -it <fullname>-postgres-0 --
psql -U postgres -d glossa`.

## Backups

`backup.enabled` adds three CronJobs (UTC) writing to an rclone remote
outside the cluster. Klarlabs: the shared Hetzner Storage Box over SFTP
(port 23), with v0.3's `rclone-config` Secret (`rclone.conf`,
`id_storagebox`, `known_hosts`) copied into the namespace; it is mounted
at `/etc/rclone`, where `rclone.conf`'s `key_file` points.

**Everything leaves the cluster encrypted.** The dumps are
[age](https://age-encryption.org) files (`<database>-<timestamp>.sql.gz.age`),
each encrypted to two recipients: the offline recovery key
(`backup.age.recoveryRecipient`, the public half, the same for every
Klarlabs tenant; its private half is kept offline) and the drill key.
The drill key is the Secret `backup.age.drillSecretName`, with the age
identity under the key `identity`; the backup derives its recipient from
it, and the restore drill decrypts with it. The MinIO mirror goes through
an rclone `crypt` remote (standard names and content) over
`backup.minio.remote`, with the passwords from the Secret
`backup.minio.cryptSecretName` (keys `password` and `password2`). Keep the
mirror's remote path separate from any plaintext mirror of the bucket.

| CronJob | Default schedule | What |
|---|---|---|
| `<fullname>-postgres-backup` | `40 3 * * *` | `pg_dump` → verify → encrypt (age) → upload to `backup.postgres.remote` → retention |
| `<fullname>-minio-backup` | `50 3 * * *` | the release bucket → `backup.minio.remote/current` (encrypted, rclone crypt), changes kept in `…/deleted/<timestamp>` |
| `<fullname>-postgres-restore-test` | `40 5 * * 0` | the newest dump restored into a throwaway Postgres and checked |

No image installs anything at runtime (v0.3 ran `apk add rclone` as root):
each pod pairs `postgres.image` with the pinned `docker.io/rclone/rclone`
image, sharing an `emptyDir`, under the restricted profile. The dump and
the drill need age, which an init container fetches (`fetch-age`: the
release pinned in `backup.age.version`, checked against
`backup.age.sha256`), so those pods reach GitHub on 443
(`networkPolicy.egress.backupAgeDownload`; with NetworkPolicies on, an
empty `to` allows 443 to any destination, for the backup pods only).

**postgres-backup** (`files/postgres-dump.sh`, then
`files/postgres-upload.sh`). The dump container runs `postgres.image`, so
`pg_dump` is the server's major version, and refuses to run otherwise.
That is v0.3's lesson (commit `ace78d5`, 2026-09-13): `pg_dump` 17
against the 16 server wrote `SET transaction_timeout`, which 16 cannot
parse, so every dump restored **zero tables** under `ON_ERROR_STOP` while
looking perfect from the backup side. Before anything is uploaded the dump
must pass `gzip -t`, end with `pg_dump`'s completion trailer, and hold
one `CREATE TABLE` per table of the live database. Then the dump is
encrypted to both recipients, and the drill key must decrypt the encrypted
file to the same bytes before the plaintext is removed. The upload is
checked by size; then dumps older than `retentionDays` are deleted, only
this release's own files (`<database>-<timestamp>.sql.gz.age`, and the
plaintext ones from before encryption) in that one directory, and only
after a verified upload, so the newest dump always survives. The scratch
volume holds the dump twice for a moment (plaintext and encrypted):
`scratchSize` must cover twice the compressed dump.

**minio-backup** (`files/minio-backup.sh`). Brotwerk's pattern (`mc
mirror` to scratch, then `rclone sync`) in one step: rclone reads MinIO
directly with glossa-edge's **read-only** user, so there is no scratch
copy of the bucket and no root. The sync writes through the crypt remote,
and the mirror must then hold the bucket's object count and bytes, or the
Job fails. A plain sync would carry an accidental
delete, or an empty bucket on a freshly lost volume, into the backup the
next night; here what a run removes or overwrites moves to
`deleted/<timestamp>/` (kept `deletedRetentionDays`), and an empty bucket
never replaces a non-empty mirror (the Job fails instead). Keep the path
out of any directory another product `rclone sync`s to (Brotwerk syncs
`storagebox:minio/`).

**postgres-restore-test** (`files/restore-fetch.sh`,
`files/postgres-restore-test.sh`). A backup that has never been restored
is a hypothesis; v0.3's drill is what found its unrestorable dumps. This
one fetches the newest encrypted dump (by this release's file names),
decrypts it with the drill key, fails if it is older than `maxDumpAgeHours`
(the nightly backup has stopped), starts
a throwaway Postgres of `postgres.image` inside its own pod (it never
connects to the live database), restores in one transaction under
`ON_ERROR_STOP`, and asserts the post-state: as many tables as the dump
creates, a clean `schema_migrations` version, and rows in every table of
`nonEmptyTables`.

### Backup runbook

```sh
NS=glossa-platform
# Run now (e.g. after install, or before a risky upgrade), then read the log:
kubectl -n $NS create job --from=cronjob/<fullname>-postgres-backup backup-$(date +%s)
kubectl -n $NS create job --from=cronjob/<fullname>-minio-backup mirror-$(date +%s)
kubectl -n $NS create job --from=cronjob/<fullname>-postgres-restore-test drill-$(date +%s)
kubectl -n $NS logs job/<name> --all-containers
# What is on the remote (from a machine with the same rclone.conf):
rclone lsl storagebox:db/glossa-platform/
```

A failed run stays as a failed Job (`failedJobsHistoryLimit`); alert on
failed Jobs of these CronJobs.

**Restoring the database** (after data loss; the drill rehearses steps
3–4 every week):

1. Stop the writers: `kubectl -n $NS scale deployment/<fullname>-server --replicas=0`.
2. Fetch a dump: `rclone copy storagebox:db/glossa-platform/glossa-<timestamp>.sql.gz.age .`,
   then decrypt it with the drill key (or the offline recovery key):
   `age -d -i drill.identity glossa-<timestamp>.sql.gz.age > glossa-<timestamp>.sql.gz`
3. Recreate the database, and on a **new, empty volume** first run
   `helm upgrade` so postgres-init recreates the owner and the database,
   then the migration roles (as the owner, like the first migration does):

   ```sh
   P="kubectl -n $NS exec -i <fullname>-postgres-0 -- psql -U postgres -v ON_ERROR_STOP=1"
   $P -c 'DROP DATABASE glossa WITH (FORCE)' -c 'CREATE DATABASE glossa OWNER glossa_owner'
   # New volume only:
   $P -c 'SET ROLE glossa_owner' \
      -c 'CREATE ROLE glossa_app NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS' \
      -c 'CREATE ROLE glossa_system NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS' \
      -c 'GRANT glossa_system TO glossa_app WITH INHERIT FALSE, SET TRUE'
   ```

4. Restore, all or nothing:
   `gunzip -c glossa-<timestamp>.sql.gz | $P -d glossa --single-transaction -q`
5. `helm upgrade` (migrations newer than the dump apply; postgres-app-login
   gives `glossa_app` its login), then scale the server back up.

**Restoring MinIO**: the mirror is an rclone `crypt` remote, so restore
through a crypt remote with the same two passwords (`rclone config`,
type `crypt`, remote `storagebox:minio-crypt/glossa-platform`, the
passwords from the `minio-backup-crypt` Secret), copying `current/` back
into the bucket with the server's read-write user, through a
`kubectl port-forward` to `<fullname>-minio`. Objects deleted by mistake
are under `deleted/<timestamp>/` of the same crypt remote.

## Deploying with RollOps

Klarlabs rolls Glossa out with RollOps, as it did v0.3 (whose `.rollops/*.yaml`
were deleted with it); the chart carries no Keel or other rollout-tool
annotations. RollOps RolloutConfigs are **generated from `helm template`
output**, one per rendered resource, each embedding the manifest as
`spec.target.spec.manifest` (and `spec.target.spec.image` for workloads),
with every image **pinned by digest** once it exists in GHCR:

```sh
helm template glossa-platform deploy/charts/glossa-platform \
  --namespace glossa-platform -f my-values.yaml --skip-tests \
  -f values-images.yaml
```

`values-images.yaml` is the release run's artifact, or `ci/image-digests.sh
<tag>` ([Cutting a release](#cutting-a-release)). It sets
`image.requireDigest`, so a render that would produce an unpinned
workload fails instead of generating one.

The chart's Helm hooks are plain resources in that output and do not run
by themselves: the migration Job (`<fullname>-migrate`, with its
NetworkPolicy) must complete before the server Deployment rolls, and a
Job is immutable, so it is deleted and re-created per rollout. With
`postgres.enabled` the order is the hooks' weights (see [Order of the
database hooks](#order-of-the-database-hooks)): the Postgres StatefulSet
ready → `<fullname>-postgres-init` → `<fullname>-migrate` →
`<fullname>-postgres-app-login` → the server Deployment. With
`minio.enabled` the same holds for `<fullname>-minio-bootstrap`, which
runs once MinIO is up (and again whenever a user Secret changes). The
backup CronJobs are ordinary resources. `--skip-tests` leaves out the
`helm test` Pods. The StatefulSets' volumeClaimTemplates are immutable:
keep `postgres.persistence` and `minio.persistence` unchanged in the
rendered manifests.

## In-namespace MinIO

`minio.enabled` runs one MinIO per product namespace, the Klarlabs
pattern (Brotwerk runs the same shape). It is a single-replica,
single-drive StatefulSet; S3 on 9000 is reachable only from glossa-server,
glossa-edge, the bootstrap Job and the test Pod.

**Storage and availability.** glossa-edge serves every published release
from this bucket (it keeps serving what it has cached while MinIO is
down, for `edge.cache.*`), so the delivery plane depends on it. The
default `minio.persistence.storageClassName` is therefore `longhorn`, 3
replicas across the nodes: a node loss moves the pod, and the volume
with it. `longhorn-r1` (1 replica, as Brotwerk uses) is fine for
development. Artifacts can be rebuilt by republishing, but the served
manifests and the release history should survive a node loss, which one
replica does not guarantee. `minio.persistence` is a volumeClaimTemplate
and immutable after install: to grow the volume, edit the PVC
(`data-<fullname>-minio-0`; the Longhorn classes allow expansion) rather than
`minio.persistence.size`. The PVC is retained when the StatefulSet is
deleted or scaled to 0. A PodDisruptionBudget with `maxUnavailable: 1`
lets a node drain evict the pod; `0` blocks drains until it is moved by
hand.

**Credentials.** Nothing is generated by the chart. MinIO's root
credentials come from `minio.rootCredentials.secretName` and reach only
MinIO and the bootstrap Job. The bootstrap Job
(`files/minio-bootstrap.sh`, `cgr.dev/chainguard/minio-client` pinned by
digest) runs after every install and upgrade and is idempotent: it
creates the bucket,
the policy `<fullname>-readwrite` (`GetObject`, `PutObject`,
`DeleteObject` and multipart cleanup on `<bucket>/<prefix>/*`) for the
user in `objectStorage.server.secretName`, and `<fullname>-readonly`
(`GetObject` only) for the user in `objectStorage.edge.secretName`. Both
also get `ListBucket` and `GetBucketLocation` on the bucket itself, not
the prefix: without `ListBucket` a missing key answers 403 instead of
404, which glossa counts as a storage failure (the edge's circuit breaker
would open on unknown keys). Rotating a secret key: change
the Secret, `helm upgrade` (the Job updates the user), then restart the
pods reading it. A new *access key* creates a new user; remove the old
one with `mc admin user rm`.

**Transport.** Server and edge talk to MinIO over plain HTTP on the pod
network (`GLOSSA_S3_INSECURE=true`, path-style requests): the traffic
never leaves the namespace and NetworkPolicy admits only them. TLS for
MinIO is not wired into the chart; it would need a certificate for
`<fullname>-minio` and its CA in the server's and edge's trust store.

**Console.** Port 9001 listens inside the pod only:

```sh
kubectl -n glossa-platform port-forward statefulset/<fullname>-minio 9001
# http://localhost:9001, root credentials from minio.rootCredentials
```

**Backups.** With `backup.enabled`, `<fullname>-minio-backup` mirrors the
bucket off the cluster every night (see [Backups](#backups)). Longhorn
snapshots can add in-cluster restore points: label the PVC for a
recurring job group (`minio.persistence.labels`, e.g.
`recurring-job-group.longhorn.io/<group>: enabled`). After a restore,
`helm upgrade` re-runs the bootstrap Job, which is harmless on existing
state.

### MinIO images

MinIO **withdrew its OSS distribution in September 2026**: Docker Hub
stopped serving `minio/minio` and `quay.io/minio/*` now refuses anonymous
pulls, so the release tags this chart pinned are unpullable. The chart
therefore runs **Chainguard's builds**, `cgr.dev/chainguard/minio` and
`cgr.dev/chainguard/minio-client`, which are public, drop-in (same
`minio`/`mc` binaries, same `MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD`, same
`/minio/health/live`) and run as **uid 65532** — the pods' `runAsUser`,
`runAsGroup` and `fsGroup` say so, and `fsGroupChangePolicy:
OnRootMismatch` hands the Longhorn volume over on the first start after
an upgrade from the old `quay.io` images (uid 1000). If Chainguard ever
gates them too, the alternatives with an S3 API are SeaweedFS, Garage,
RustFS and Ceph RGW; each would need its own image, probes and
bootstrap.

**Refreshing the digest.** Chainguard's free tier publishes `latest`
only and garbage-collects the digests behind it, so a pin here *will*
eventually name an image that no longer exists — a `helm upgrade` then
fails to pull. Re-pin both from the registry:

```sh
docker buildx imagetools inspect cgr.dev/chainguard/minio:latest --format '{{.Manifest.Digest}}'
docker buildx imagetools inspect cgr.dev/chainguard/minio-client:latest --format '{{.Manifest.Digest}}'
# or: crane digest cgr.dev/chainguard/minio:latest
```

and set `minio.image.digest` / `minio.bootstrap.image.digest`. The test
harnesses (`platform/internal/kernel/objectstore/s3store/s3test` and
`ci/e2e-database.sh`) deliberately track `:latest` unpinned instead: a
stale pin there breaks the build for no reproducibility a test needs.

## Mail

SMTP is optional. With `mail.smtp.addr` set (and optionally
`mail.smtp.secretName` for AUTH), the chart sets `GLOSSA_MAIL_DRIVER=smtp`,
the `GLOSSA_SMTP_*` variables and the server's SMTP egress rule. Without
it, the chart sets **no** `GLOSSA_MAIL_*`/`GLOSSA_SMTP_*` variable, needs
no SMTP Secret and opens no SMTP egress: glossa-server's own behaviour
without a mailer applies. Email sign-in links and invitations are not
delivered then. glossa-server 0.4.0 still defaults to the `log` driver,
which writes sign-in links to its log; NOTES.txt says so after install.
`mail.driver: log` can be set explicitly for development.

## GitHub App

Optional (RFC 0004 §6.1). With `github.appId` empty the chart sets **no**
`GLOSSA_GITHUB_APP_*`/`_CLIENT_*`/`_WEBHOOK_*` variable, needs no Secret
and opens no egress to GitHub; the GitHub endpoints answer
`github_not_configured` (503) and nothing else in the deployment
changes. Setting `github.appId` makes `github.appSlug`,
`github.clientId` and `github.credentials.secretName` required, so a
half-configured chart fails to render with the missing value named
rather than crash-looping the pods on the server's own startup check.

**Where the App is registered.** Glossa's own App is **private, under the
`klarlabs-studio` organization** (RFC 0004 §14.1): installable on our own
repositories only, until the product's status is decided. A self-hosted
instance registers its own App under its own organization and points this
chart at that one — nothing about the App is shared between instances.

Repository permissions, and no more:

| Permission | Why |
|---|---|
| `checks: write` | the Glossa check run on a pull request's head commit |
| `pull_requests: write` | the one sticky PR comment |
| `metadata: read` | mandatory for every App |

There is deliberately **no `contents` access**: Glossa never reads code.
Keys, usages and screenshots arrive from the product's own CI instead
(RFC 0004 §6.3). Subscribe to four events — `installation`,
`installation_repositories`, `pull_request` and `check_run` — and no
others; anything else is stored, processed and dropped for nothing.

**How that CI authenticates.** With an App configured, a GitHub Actions
run needs no stored Glossa secret: it asks GitHub for an OIDC ID token
with audience `glossa` and exchanges it at
`POST /v1/auth/github-oidc-exchanges` (RFC 0004 §6.3). The server
verifies the token against the issuer's published keys — one verifier
per process, with a cached JWKS — and matches its `repository_id`
against a Git connection; what it hands back lives 30 minutes, is bound
to that one project and allows only `catalog.read` and `catalog.write`.
`github.oidc.*` below points that at a GHES issuer; on github.com the
defaults are right. Without an App the exchange answers
`github_not_configured` and CI uses a stored API token, as before.

The App's URLs:

| Field on the App | Value |
|---|---|
| Webhook URL (API host: `hosts.api`, else `hosts.studio`) | `https://<API host>/v1/integrations/github/webhooks` |
| Webhook secret | the same string as `GLOSSA_GITHUB_WEBHOOK_SECRET` in the Secret below |
| Setup URL, and the OAuth callback URL | Studio, on `hosts.studio`: GitHub returns the person there with `state`, `installation_id`, `code` and `setup_action`, which Studio posts to `POST /v1/tenants/{tenant}/github/installations` to finish the install |

**"Request user authorization (OAuth) during installation" must be
ticked.** Without it GitHub sends no `code`, and the callback cannot
redeem the person's own token to check that they can really see the
`installation_id` they are claiming — which is the whole reason nobody
can claim another organization's installation.

The webhook endpoint is not authenticated by a session (GitHub signs each
delivery, and the signature is checked before the body is parsed), but it
lives under `/v1` like everything else, so the API host's IngressRoute
already routes it. There is nothing to add to the ingress.

**The Secret.** The private key, the webhook secret and the OAuth client
secret are platform configuration, never tenant data: one Secret in the
release's namespace, read through env, exactly like the release signing
key. The chart never creates it, so there is nowhere in `values.yaml` to
paste a private key:

```sh
kubectl -n glossa-platform create secret generic glossa-github \
  --from-file=GLOSSA_GITHUB_APP_PRIVATE_KEY=glossa.<date>.private-key.pem \
  --from-literal=GLOSSA_GITHUB_WEBHOOK_SECRET='…' \
  --from-literal=GLOSSA_GITHUB_CLIENT_SECRET='…'
```

then `github.credentials.secretName: glossa-github`. Those key names are
the defaults of `github.credentials.privateKeyKey`, `.webhookSecretKey`
and `.clientSecretKey`; an existing Secret with different names works
just as well, point the three values at them. All three are read once at
startup, so rotating one means re-applying the Secret and
`kubectl rollout restart deployment/<fullname>-server`. Installation
tokens are minted from the key on demand, cached in memory and never
stored.

**Processing.** A delivery is stored and acknowledged with `202` inside
GitHub's 10 s limit; `server.github.inbox.*` tunes the worker that
processes the inbox afterwards. Leaving `server.github.inbox.enabled` off
everywhere queues deliveries rather than losing them, but nothing acts on
them.

**GitHub Enterprise Server.** `github.apiUrl: https://HOST/api/v3` and
`github.webUrl: https://HOST`; both default to github.com.

## MCP

`glossa-server` can serve the [Model Context Protocol](https://modelcontextprotocol.io)
at `/mcp` over streamable HTTP, in the same process, behind the same
middleware, tenancy and row-level security as the REST API
(RFC 0005 §7). It is **off by default**:

```yaml
server:
  mcp:
    enabled: true
```

Nothing else is needed, and nothing else is minted. An MCP session
presents an existing **tenant API token** (`glossa_api_…`) as its bearer
token, and inherits that token's scopes, grant and tenant unchanged —
the same `Scope` → permissions the REST API derives. So:

- **the token's tenant is the session's tenant.** No tool takes a tenant
  argument. A token cannot reach another tenant's catalog, and revoking
  it ends its agent's session at the next call.
- **CI tokens (`glossa_ci_…`) and in-context grants (`glossa_ctx_…`) are
  refused at connect**, with a message naming what was presented. Each
  is minted for one workflow run or one browser origin; lending either
  to a long-lived agent would widen it.
- **writes need two locks**: the token must carry the `write` scope
  *and* the client must open the session asking for the write toolset
  (`https://<API host>/mcp?toolset=write`). A plain `/mcp` is a read
  session, whatever the token could do.
- **`admin` is not exposed, and there is no delete tool of any kind.**
  No member, token, connection or tenant management, and no AI provider
  key in either direction.
- **every tool call is written to an append-only audit ledger**
  (`mcp_tool_calls`) with the token, the tool, the outcome and the
  *shape* of the arguments — a locale verbatim, a translation as its
  length. Message text never reaches the ledger or the logs.

Turn it on deliberately. Enabling it lets every existing token that
carries `read` drive an agent against that tenant's catalog, and those
tokens were minted before the endpoint existed.

Metrics: `glossa_mcp_tool_calls_total{tool,scope,outcome}`,
`glossa_mcp_sessions_total{transport}` and
`glossa_mcp_sessions_open{transport}`.

## Network policies

With `networkPolicy.enabled`, each component gets one policy covering both
directions; anything not listed is denied.

| Pod | Ingress | Egress |
|---|---|---|
| server | ingress controller → 8080; `metrics.allowFrom` → 8080 | DNS; Postgres²; object storage¹; `egress.smtp` (SMTP configured); `egress.github` (`github.appId` set); `egress.otlp` (endpoint set) |
| edge | ingress controller → 8081; `metrics.allowFrom` → 8081 | DNS; object storage¹; `egress.otlp` (endpoint set) |
| studio | ingress controller → 8080 | none |
| migrate Job | none | DNS; Postgres² |
| postgres | server, migrate Job, bootstrap Jobs, backup Job, test Pod → 5432; `extraIngress.postgres` | DNS |
| postgres-init/-app-login Jobs, postgres-test Pod | none | DNS; Postgres → 5432 |
| minio | server, edge, bootstrap Job, backup Job, test Pod → 9000; `extraIngress.minio` | DNS |
| minio-bootstrap Job, minio-test Pod | none | DNS; MinIO → 9000 |
| postgres-backup | none | DNS; Postgres → 5432; `egress.backupRemote` |
| minio-backup | none | DNS; MinIO → 9000; `egress.backupRemote` |
| postgres-restore-test | none | DNS; `egress.backupRemote` (it restores inside its own pod) |

¹ `egress.objectStorage`, or with `minio.enabled` the MinIO pod on 9000
(a podSelector rule; `egress.objectStorage` is then unused).
² `egress.postgres`, or with `postgres.enabled` the Postgres pod on 5432
(a podSelector rule; `egress.postgres` is then unused).

NetworkPolicy matches IPs, not hostnames. Each `egress.*.to` takes
NetworkPolicyPeers (`ipBlock` for external services, namespace/pod
selectors for in-cluster ones). **An empty `to` allows those ports to any
destination**; NOTES.txt warns while an external Postgres, object storage
or the backup remote is unpinned. Most policy engines let the kubelet's
probes through regardless; if yours doesn't, allow the node addresses
with `networkPolicy.extraIngress`.

## Values

REQUIRED values have no default; rendering fails with a message naming
the value until it is set.

### Hosts and images

| Value | Default | Meaning |
|---|---|---|
| `hosts.studio` | REQUIRED | Studio host (e.g. `glossa.<domain>`); with `hosts.api` empty it also serves the API under `/v1`. Also `GLOSSA_STUDIO_URL`, default passkey RP ID and origin. |
| `hosts.api` | `""` | Optional separate API host (`api.<domain>`), `/v1` and `/.well-known/glossa-audit-keys.json` only. Empty: the API is `hosts.studio`, and no second route, Certificate or redirect entry is rendered. |
| `hosts.cdn` | REQUIRED | glossa-edge host (`cdn.<domain>`), `/v1` only. |
| `nameOverride` | `""` | `app.kubernetes.io/name` (default: chart name). |
| `fullnameOverride` | `""` | Prefix of every resource name (default: release name). |
| `image.registry` | `ghcr.io` | Registry of all three images. |
| `image.pullPolicy` | `IfNotPresent` | |
| `image.requireDigest` | `false` | Refuse to render unless server, edge and studio are each pinned by digest. The release's `values-images.yaml` sets it with the digests ([Cutting a release](#cutting-a-release)). |
| `image.pullSecrets` | `[]` | `imagePullSecrets` for every pod. |
| `<component>.image.repository` | `klarlabs-studio/glossa-{server,edge,studio}` | `<component>` is `server`, `edge` or `studio`. |
| `<component>.image.tag` | `""` → `v<appVersion>` | Kiln's release tag. |
| `<component>.image.digest` | `""` | `sha256:<64 hex>`; appended as `@digest`, and what is actually pulled. Anything else fails the render. |
| `commonLabels` | `{}` | Labels on every resource. |
| `deploymentAnnotations` | `{}` | Annotations on every Deployment. |
| `podAnnotations` | `{}` | Annotations on every pod. |

### Per component (`server`, `edge`, `studio`)

| Value | Default | Meaning |
|---|---|---|
| `<component>.replicas` | `2` | Ignored for the edge when autoscaling is on. |
| `<component>.resources` | see values.yaml | Requests and memory limits; no CPU limits. |
| `<component>.extraEnv` | `[]` | Extra `EnvVar`s (any variable from platform/README.md). |
| `<component>.pdb.enabled` / `.maxUnavailable` | `true` / `1` | PodDisruptionBudget. |
| `<component>.nodeSelector` / `.tolerations` / `.affinity` | empty | Scheduling. |
| `<component>.topologySpreadConstraints` | `[]` | Replaces the default soft spread across nodes. |

### glossa-server

| Value | Default | Env / meaning |
|---|---|---|
| `server.logLevel` | `info` | `GLOSSA_LOG_LEVEL` |
| `server.shutdownTimeout` | `25s` | `GLOSSA_SHUTDOWN_TIMEOUT` |
| `server.terminationGracePeriodSeconds` | `35` | Must exceed the shutdown timeout. |
| `server.sessionTTL` | `336h` | `GLOSSA_SESSION_TTL` |
| `server.outbox.enabled` | `true` | `GLOSSA_OUTBOX_ENABLED` (dispatchers claim with leases, so replicas share work). |
| `server.ai.workersEnabled` | `true` | `GLOSSA_AI_WORKERS_ENABLED`: AI translation job workers in the server pods. No provider is called until a tenant configures one and gives consent. |
| `server.ai.workers` | `2` | `GLOSSA_AI_WORKERS` per pod. |
| `server.ai.providerConcurrency` | `4` | `GLOSSA_AI_PROVIDER_CONCURRENCY`: in-flight calls per provider, per pod (the per-tenant cap is exact across replicas). |
| `server.integration.workersEnabled` | `true` | `GLOSSA_INTEGRATION_WORKERS_ENABLED`: import/export workers and the file-retention sweep. |
| `server.integration.workers` | `1` | `GLOSSA_INTEGRATION_WORKERS` per pod. |
| `server.integration.maxUploadBytes` | `67108864` | `GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES` (64 MiB). The upload route enforces it itself; keep any ingress body limit above it. |
| `server.integration.retention` | `168h` | `GLOSSA_INTEGRATION_RETENTION`: import/export files are deleted after this; jobs and results stay. |
| `server.purge.enabled` | `true` | `GLOSSA_PURGE_ENABLED`: the daily jobs — builds beyond retention (the latest 3 per application, branch and source, and a closed branch's after 7 days) with their captures, the images no capture references, and Catalog's separate sweep of the proposals of branches closed 14 days ago. One replica leads each run (a lease), so leave it on everywhere. |
| `server.purge.interval` | `24h` | `GLOSSA_PURGE_INTERVAL`: how often the jobs run across the deployment, not per pod. |
| `server.purge.timeout` | `30m` | `GLOSSA_PURGE_TIMEOUT`: bounds one run of one job; it must fit inside the interval. |
| `server.purge.lease` | `35m` | `GLOSSA_PURGE_LEASE`: how long a run reserves its job against the other replicas. Must exceed the timeout. |
| `server.purge.pollInterval` | `5m` | `GLOSSA_PURGE_POLL_INTERVAL`: how often a pod asks whether a job is due. |
| `server.purge.jitter` | `0.2` | `GLOSSA_PURGE_JITTER`: fraction of the poll interval each poll is spread by (0–1), so replicas don't ask in lockstep. |
| `server.purge.batchSize` | `100` | `GLOSSA_PURGE_BATCH_SIZE`: object-store deletes issued at a time while freeing unreferenced images. |
| `server.workflow.instanceRetention` | `4320h` | `GLOSSA_WORKFLOW_INSTANCE_RETENTION`: a finished workflow instance and its transition log are kept this long (180 days, at least `24h`), then the daily `workflow.retention` job — one of the purge jobs, so it runs only where `server.purge.enabled` — deletes them. Running instances are never deleted; who did what stays in the audit trail. |
| `server.context.storageQuotaBytes` | `2147483648` | `GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES`: capture images one tenant may keep in object storage (2 GiB). A capture upload whose new pixels would pass it is refused with `storage_quota_exceeded` (413); the daily purge frees space again. Watch `glossa_context_capture_bytes_used` against `glossa_context_capture_quota_bytes`. |
| `server.mcp.enabled` | `false` | `GLOSSA_MCP_ENABLED`: serve MCP at `/mcp` (RFC 0005 §7). Off, the path is not registered at all. See [MCP](#mcp) before turning it on. |
| `server.mcp.sessionTimeout` | `30m` | `GLOSSA_MCP_SESSION_TIMEOUT`: close a session idle this long, so an agent that walks away does not hold one open. |
| `server.mcp.rate` | `120` | `GLOSSA_MCP_RATE`: tool calls a minute per tenant. A runaway agent hits the same wall a runaway script does. |
| `server.mcp.burst` | `240` | `GLOSSA_MCP_BURST`: tool calls one tenant may make at once. |
| `server.branches.publisherEnabled` | `true` | `GLOSSA_BRANCH_PUBLISHER_ENABLED`: publish a branch's preview environment when its debounced request is due. A publish is keyed by its request, so every replica may run it. |
| `server.branches.publishInterval` | `5s` | `GLOSSA_BRANCH_PUBLISH_INTERVAL`: how often due branch publishes are looked for (the debounce itself is 30 s). |
| `server.github.inbox.enabled` | `true` | `GLOSSA_GITHUB_INBOX_ENABLED`: process stored webhook deliveries in these pods. The endpoint stores and acknowledges a delivery whatever this says (RFC 0004 §6.2), so turning it off everywhere queues them rather than losing them. Nothing reaches the inbox without an App — see [GitHub App](#github-app). |
| `server.github.inbox.workers` | `2` | `GLOSSA_GITHUB_INBOX_WORKERS`: deliveries handled at once, per pod (1–64). |
| `server.github.inbox.pollInterval` | `1s` | `GLOSSA_GITHUB_INBOX_POLL_INTERVAL`: how long a worker waits on an empty inbox. |
| `server.github.inbox.timeout` | `30s` | `GLOSSA_GITHUB_INBOX_TIMEOUT`: bounds one attempt at one delivery. |
| `server.github.inbox.lease` | `2m` | `GLOSSA_GITHUB_INBOX_LEASE`: how long a claimed delivery is reserved against the other replicas. Must exceed the timeout, or the server refuses to start. |
| `server.github.inbox.depthInterval` | `30s` | `GLOSSA_GITHUB_INBOX_DEPTH_INTERVAL`: how often the inbox-depth metric is sampled. |
| `server.github.checks.enabled` | `true` | `GLOSSA_GITHUB_CHECKS_ENABLED`: render pull requests' Glossa checks in these pods (RFC 0004 §6.4). The webhook queues a check whatever this says, so turning it off everywhere makes checks wait rather than lose them. |
| `server.github.checks.workers` | `2` | `GLOSSA_GITHUB_CHECK_WORKERS`: checks rendered at once, per pod (1–64). |
| `server.github.checks.pollInterval` | `1s` | `GLOSSA_GITHUB_CHECK_POLL_INTERVAL`: how long a worker waits on an empty queue. |
| `server.github.checks.timeout` | `1m` | `GLOSSA_GITHUB_CHECK_TIMEOUT`: bounds one attempt at one check. |
| `server.github.checks.lease` | `5m` | `GLOSSA_GITHUB_CHECK_LEASE`: how long a claimed check is reserved against the other replicas. Must exceed the timeout, or the server refuses to start. It is also what keeps one job per pull request, and so one writer of its sticky comment. |
| `server.github.checks.depthInterval` | `30s` | `GLOSSA_GITHUB_CHECK_DEPTH_INTERVAL`: how often the check queue's depth is sampled. |
| `server.webauthn.rpId` | `hosts.studio` | `GLOSSA_WEBAUTHN_RP_ID`; changing it later invalidates enrolled passkeys. |
| `server.webauthn.rpName` | `Glossa` | `GLOSSA_WEBAUTHN_RP_NAME` |
| `server.webauthn.origins` | `[https://<hosts.studio>]` | `GLOSSA_WEBAUTHN_ORIGINS` |
| `server.otel.endpoint` | `""` | `OTEL_EXPORTER_OTLP_ENDPOINT`; also opens `egress.otlp`. |
| `server.migrations.enabled` | `true` | Hook Job `glossa-server -migrate=only`. |
| `server.migrations.backoffLimit` | `3` | Retries (a fresh pod's NetworkPolicy may lag). |
| `server.migrations.activeDeadlineSeconds` | `600` | |
| `server.migrations.resources` | see values.yaml | |

### glossa-edge

| Value | Default | Env / meaning |
|---|---|---|
| `edge.logLevel` / `edge.shutdownTimeout` / `edge.terminationGracePeriodSeconds` | `info` / `25s` / `35` | As for the server. |
| `edge.cache.bytes` | `67108864` | `GLOSSA_EDGE_CACHE_BYTES`; keep the memory limit above it. |
| `edge.cache.keyTTL` | `30s` | `GLOSSA_EDGE_KEY_TTL`: how long a revoked key keeps working. |
| `edge.cache.manifestTTL` | `5s` | `GLOSSA_EDGE_MANIFEST_TTL`: publish delay per edge. |
| `edge.otel.endpoint` | `""` | `OTEL_EXPORTER_OTLP_ENDPOINT` |
| `edge.autoscaling.enabled` | `false` | HPA on CPU. |
| `edge.autoscaling.minReplicas` / `.maxReplicas` | `2` / `6` | |
| `edge.autoscaling.targetCPUUtilizationPercentage` | `70` | |

### Site

The landing page (`site/`, static Astro behind nginx) shares the Studio
host: the ingress sends the exact `/` and `/_astro/*` (plus `/favicon.svg`
and `/og.png`) to it at priority 50, above Studio's catch-all, and
Studio's home is `/app`. Everything else (`/auth/*`, `/t/*`, `/device`,
`/v1`, `/mcp`, `/.well-known/*`) is unchanged. The site has no runtime
configuration and no egress.

| Key | Default | Notes |
|---|---|---|
| `site.enabled` | `true` | Off: no Deployment, Service, NetworkPolicy or route; `/` goes to Studio, which redirects to `/app`. |
| `site.image.repository` / `.tag` / `.digest` | `klarlabs-studio/glossa-site` / `v<appVersion>` / `""` | With `image.requireDigest` the digest is required while `site.enabled`. |
| `site.replicas`, `site.resources`, `site.pdb.*`, `site.nodeSelector`, `site.tolerations`, `site.affinity`, `site.topologySpreadConstraints`, `site.extraEnv` | as `studio.*` | Small defaults: 2 replicas, 64Mi limit. |
| `networkPolicy.extraIngress.site` | `[]` | Extra `NetworkPolicyIngressRule`s. |

### Studio

| Value | Default | Env / meaning |
|---|---|---|
| `studio.runtimeConfig.apiBaseUrl` | `""` | `GLOSSA_STUDIO_API_BASE_URL` → `/config.json`; empty means `/v1` on Studio's origin. |
| `studio.runtimeConfig.edgeUrl` | `https://<hosts.cdn>` | `GLOSSA_STUDIO_EDGE_URL` → `/config.json`. |
| `studio.runtimeConfig.environment` | `production` | `GLOSSA_STUDIO_ENVIRONMENT` → `/config.json`. |
| `studio.runtimeConfig.cspConnectSrc` | `""` | `GLOSSA_STUDIO_CSP_CONNECT_SRC`: extra `connect-src` origins, space separated. |

### Secrets and external services

| Value | Default | Meaning |
|---|---|---|
| `database.app.secretName` / `.secretKey` | REQUIRED unless `postgres.enabled` / `DATABASE_URL` | glossa_app DSN (server). Pool size goes in the DSN (`pool_max_conns`). With `postgres.enabled`, unset means a DSN for the in-namespace Postgres. |
| `database.migration.secretName` / `.secretKey` | REQUIRED with migrations unless `postgres.enabled` / `MIGRATION_DATABASE_URL` | Owner DSN; mounted into the migration Job only. Defaults like `database.app`. |
| `auth.secretName` / `.secretKey` | REQUIRED / `GLOSSA_AUTH_SECRET` | Base64 of ≥ 32 random bytes. |
| `release.signingKeys.secretName` / `.secretKey` | REQUIRED / `GLOSSA_RELEASE_SIGNING_KEYS` | `keyId=base64(seed)`, comma-separated. |
| `release.retiredKeys` | `""` | `GLOSSA_RELEASE_RETIRED_KEYS` (public keys, not secret). |
| `audit.exports.enabled` | `false` | `GLOSSA_AUDIT_EXPORTS_ENABLED`: audit export jobs (RFC 0006 §6.2). Requires `audit.signingKey.secretName`; the chart fails to render without it. |
| `audit.signingKey.secretName` / `.secretKey` | `""` / `GLOSSA_AUDIT_SIGNING_KEY` | Exactly one `keyId=base64(seed)`, e.g. `kubectl create secret generic glossa-audit-signing --from-literal=GLOSSA_AUDIT_SIGNING_KEY="audit-2026=$(openssl rand -base64 32)"`. Its own key: the server refuses a seed that is also a release signing key. |
| `audit.retiredKeys` | `""` | `GLOSSA_AUDIT_RETIRED_KEYS`: public keys of earlier audit keys, so exports they signed keep verifying (public, not secret). |
| `mail.driver` | `""` → `smtp` if `mail.smtp.addr` is set, else unset | `GLOSSA_MAIL_DRIVER`; `log` is for development only. See [Mail](#mail). |
| `mail.from` | `""` | `GLOSSA_MAIL_FROM` (set only with a driver). |
| `mail.smtp.addr` | `""` | `GLOSSA_SMTP_ADDR` (`host:587`, STARTTLS). Setting it turns SMTP on; required with `mail.driver: smtp`. |
| `mail.smtp.secretName` / `.usernameKey` / `.passwordKey` | `""` / `GLOSSA_SMTP_USERNAME` / `GLOSSA_SMTP_PASSWORD` | AUTH credentials; empty name: no AUTH. |
| `github.appId` | `""` | `GLOSSA_GITHUB_APP_ID`. Empty: the integration is off and its endpoints answer `github_not_configured` (503). Set, it makes the four values below required. See [GitHub App](#github-app). |
| `github.appSlug` | `""` | `GLOSSA_GITHUB_APP_SLUG`, the App's name in its own URL; the install link is `<web url>/apps/<slug>/installations/new`. REQUIRED with `github.appId`. |
| `github.clientId` | `""` | `GLOSSA_GITHUB_CLIENT_ID`, the App's OAuth client id (public, unlike its secret). REQUIRED with `github.appId`. |
| `github.apiUrl` / `.webUrl` | `https://api.github.com` / `https://github.com` | `GLOSSA_GITHUB_API_URL` / `GLOSSA_GITHUB_WEB_URL`; GitHub Enterprise Server: `https://HOST/api/v3` and `https://HOST`. |
| `github.oidc.issuer` | `https://token.actions.githubusercontent.com` | `GLOSSA_GITHUB_OIDC_ISSUER`: the exact `iss` of the Actions ID tokens CI presents (RFC 0004 §6.3). GHES: `https://HOST/_services/token`. |
| `github.oidc.audience` | `glossa` | `GLOSSA_GITHUB_OIDC_AUDIENCE`: the `aud` a run must ask GitHub for. Change it only if another service already uses that name on your runners — GitHub's own default audience is the repository owner's URL, which any service could be handed. |
| `github.oidc.maxStale` | `24h` | `GLOSSA_GITHUB_OIDC_MAX_STALE`: how long a cached key set keeps verifying while the issuer is unreachable. Past it exchanges fail closed until a refetch succeeds, so a key GitHub revoked cannot stay trusted. |
| `github.credentials.secretName` | REQUIRED with `github.appId` | One existing Secret with all three secret values (RFC 0004 §14.1); the chart never creates it. |
| `github.credentials.privateKeyKey` / `.webhookSecretKey` / `.clientSecretKey` | `GLOSSA_GITHUB_APP_PRIVATE_KEY` / `GLOSSA_GITHUB_WEBHOOK_SECRET` / `GLOSSA_GITHUB_CLIENT_SECRET` | The App's RSA private key (PEM, PKCS#1 or PKCS#8), the webhook HMAC secret and the OAuth client secret. |
| `objectStorage.endpoint` | REQUIRED; `<fullname>-minio:9000` with `minio.enabled` | `GLOSSA_S3_ENDPOINT`, `host[:port]` without scheme. |
| `objectStorage.bucket` | REQUIRED | `GLOSSA_S3_BUCKET`; the bootstrap Job creates it with `minio.enabled`. |
| `objectStorage.region` | `us-east-1` | `GLOSSA_S3_REGION` (MinIO's default region). |
| `objectStorage.prefix` | `""` | `GLOSSA_S3_PREFIX`; with `minio.enabled` the users' object permissions are scoped to it. |
| `objectStorage.pathStyle` | `false` | `GLOSSA_S3_PATH_STYLE`; always `true` with `minio.enabled`. |
| `objectStorage.insecure` | `false` | `GLOSSA_S3_INSECURE`; always `true` with `minio.enabled` (in-namespace HTTP). |
| `objectStorage.timeout` | `10s` | `GLOSSA_S3_TIMEOUT` |
| `objectStorage.server.secretName` / `.accessKeyIdKey` / `.secretAccessKeyKey` | REQUIRED / `GLOSSA_S3_ACCESS_KEY_ID` / `GLOSSA_S3_SECRET_ACCESS_KEY` | Read-write credentials; with `minio.enabled`, the MinIO user the bootstrap Job creates. |
| `objectStorage.edge.secretName` / keys | `""` → server's / same | Read-only credentials for the edge (recommended; REQUIRED and distinct with `minio.enabled`). |

### In-namespace MinIO

| Value | Default | Meaning |
|---|---|---|
| `minio.enabled` | `false` | MinIO in the release's namespace; objectStorage points at it. |
| `minio.image.repository` / `.tag` / `.digest` | `cgr.dev/chainguard/minio` / `latest` / `sha256:6a1d0b…` | Full image reference (not under `image.registry`). Runs as uid 65532; refresh the digest, see [MinIO images](#minio-images). |
| `minio.rootCredentials.secretName` | REQUIRED with `minio.enabled` | Existing Secret; the chart never creates it. |
| `minio.rootCredentials.userKey` / `.passwordKey` | `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` | |
| `minio.persistence.storageClassName` | `longhorn` | 3 Longhorn replicas; `longhorn-r1` for development. Immutable after install. |
| `minio.persistence.size` | `5Gi` | Immutable in the template; grow the PVC instead. |
| `minio.persistence.labels` / `.annotations` | `{}` | On the PVC, e.g. a Longhorn recurring-job group. |
| `minio.resources` | 50m / 256Mi, limit 512Mi | |
| `minio.terminationGracePeriodSeconds` | `30` | |
| `minio.pdb.enabled` / `.maxUnavailable` | `true` / `1` | `0` blocks node drains. |
| `minio.nodeSelector` / `.tolerations` / `.affinity` | empty | Also used by the bootstrap Job and the test Pod. |
| `minio.bootstrap.enabled` | `true` | The post-install/post-upgrade Job (and the `helm test` Pod). |
| `minio.bootstrap.image.repository` / `.tag` / `.digest` | `cgr.dev/chainguard/minio-client` / `latest` / `sha256:b2bd78…` | `mc`, uid 65532; same refresh caveat. |
| `minio.bootstrap.waitSeconds` | `600` | How long the Job waits for MinIO. |
| `minio.bootstrap.backoffLimit` / `.activeDeadlineSeconds` | `3` / `900` | |
| `minio.bootstrap.resources` | 10m / 32Mi, limit 128Mi | Also the test Pod's. |

### In-namespace Postgres

| Value | Default | Meaning |
|---|---|---|
| `postgres.enabled` | `false` | Postgres in the release's namespace; the DSNs default to it. |
| `postgres.image.repository` / `.tag` / `.digest` | `docker.io/library/postgres` / `16.15-alpine` / `sha256:3c5c88…` | Also every Job's `psql`/`pg_dump` and the drill's throwaway server: one major version everywhere. |
| `postgres.database` | `glossa` | Created by postgres-init, owned by the owner. Also the dump file prefix. |
| `postgres.superuser.username` | `postgres` | `POSTGRES_USER` at initdb. |
| `postgres.superuser.secretName` / `.passwordKey` | REQUIRED with `postgres.enabled` / `password` | Read by Postgres and the bootstrap Jobs only. |
| `postgres.owner.username` | `glossa_owner` | Schema owner: `LOGIN CREATEROLE`, never a superuser. |
| `postgres.owner.secretName` / `.passwordKey` | REQUIRED with `postgres.enabled` / `password` | |
| `postgres.app.secretName` / `.passwordKey` | REQUIRED with `postgres.enabled` / `password` | `glossa_app`'s password (server, `helm test`). |
| `postgres.app.poolMaxConns` | `20` | `pool_max_conns` of the built DSN, per server pod. |
| `postgres.persistence.storageClassName` | `longhorn` | 3 Longhorn replicas. Immutable after install. |
| `postgres.persistence.size` | `5Gi` | Immutable in the template; grow the PVC instead. |
| `postgres.persistence.labels` / `.annotations` | `{}` | On the PVC. |
| `postgres.resources` | 100m / 256Mi, limit 1Gi | |
| `postgres.terminationGracePeriodSeconds` | `60` | |
| `postgres.pdb.enabled` / `.maxUnavailable` | `true` / `1` | `0` blocks node drains. |
| `postgres.nodeSelector` / `.tolerations` / `.affinity` | empty | Also used by the Postgres Jobs, the backup and restore Jobs and the test Pod. |
| `postgres.bootstrap.enabled` | `true` | The postgres-init and postgres-app-login hook Jobs (and the `helm test` Pod). |
| `postgres.bootstrap.waitSeconds` | `600` | How long postgres-init waits for Postgres. |
| `postgres.bootstrap.backoffLimit` / `.activeDeadlineSeconds` | `3` / `900` | |
| `postgres.bootstrap.resources` | 10m / 32Mi, limit 128Mi | Also the test Pod's. |

### Backups

| Value | Default | Meaning |
|---|---|---|
| `backup.enabled` | `false` | The backup CronJobs; needs `postgres.enabled` or `minio.enabled`. |
| `backup.rclone.image.repository` / `.tag` / `.digest` | `docker.io/rclone/rclone` / `1.75.1` / `sha256:45401a…` | |
| `backup.rclone.configSecretName` | REQUIRED with `backup.enabled` | Existing Secret mounted at `/etc/rclone`. |
| `backup.rclone.configKey` | `rclone.conf` | |
| `backup.age.recoveryRecipient` | the Klarlabs offline recovery key (public) | The second recipient of every dump; the private half is kept offline. |
| `backup.age.drillSecretName` | REQUIRED with `backup.postgres.enabled` | Existing Secret, key `identity`: the drill key (an age identity). |
| `backup.age.version` / `.sha256` / `.image` | `v1.3.2` / `cbe240…` / `alpine:3.22@sha256:…` | The age release fetched by `fetch-age`, its checked SHA-256, and the image that runs the fetch. |
| `backup.successfulJobsHistoryLimit` / `.failedJobsHistoryLimit` | `3` / `3` | Per CronJob. |
| `backup.postgres.enabled` | `true` | Nightly dump (with `postgres.enabled`). |
| `backup.postgres.schedule` | `40 3 * * *` | UTC. |
| `backup.postgres.remote` | REQUIRED | `<remote>:<directory>`, e.g. `storagebox:db/glossa-platform`; never a remote's root. |
| `backup.postgres.retentionDays` | `30` | Older dumps of this release are deleted after a verified upload. |
| `backup.postgres.role.username` | `glossa_backup` | `LOGIN BYPASSRLS`, `pg_read_all_data`; created by postgres-init. |
| `backup.postgres.role.secretName` / `.passwordKey` | REQUIRED with backups / `password` | |
| `backup.postgres.backoffLimit` / `.activeDeadlineSeconds` | `2` / `3600` | |
| `backup.postgres.scratchSize` | `2Gi` | `emptyDir` for the compressed dump. |
| `backup.postgres.resources` | 50m / 64Mi, limit 512Mi | Also the drill's fetch container. |
| `backup.minio.enabled` | `true` | Nightly bucket mirror (with `minio.enabled`). |
| `backup.minio.schedule` | `50 3 * * *` | UTC. |
| `backup.minio.remote` | REQUIRED | e.g. `storagebox:minio-crypt/glossa-platform`; the crypt remote's `current/` and `deleted/<timestamp>/`. |
| `backup.minio.cryptSecretName` | REQUIRED with `backup.minio.enabled` | Existing Secret, keys `password` and `password2`: the rclone crypt passwords. |
| `backup.minio.deletedRetentionDays` | `30` | How long `deleted/` keeps a run's removed or overwritten objects. |
| `backup.minio.backoffLimit` / `.activeDeadlineSeconds` / `.resources` | `2` / `3600` / 50m, 64Mi, limit 512Mi | |
| `backup.restoreTest.enabled` | `true` | The weekly drill (with the Postgres backup). |
| `backup.restoreTest.schedule` | `40 5 * * 0` | UTC, Sundays. |
| `backup.restoreTest.nonEmptyTables` | `[]` | Tables that must come back with rows, e.g. `[tenants]`. |
| `backup.restoreTest.maxDumpAgeHours` | `48` | Fails when the newest dump is older. |
| `backup.restoreTest.backoffLimit` / `.activeDeadlineSeconds` | `1` / `3600` | |
| `backup.restoreTest.scratchSize` | `4Gi` | The dump plus the throwaway data directory. |
| `backup.restoreTest.resources` | 100m / 256Mi, limit 1Gi | The throwaway Postgres. |

### Ingress

| Value | Default | Meaning |
|---|---|---|
| `ingress.enabled` | `true` | IngressRoutes, Middlewares, Certificates. |
| `ingress.entryPoints.web` / `.websecure` | `web` / `websecure` | traefik entry points. |
| `ingress.httpsRedirect` | `true` | Permanent http→https (ACME HTTP-01 paths excepted). |
| `ingress.compress.studio` / `.api` / `.cdn` | `true` / `true` / `false` | traefik compression per host (the edge's strong ETags describe uncompressed bytes). |
| `ingress.hsts.enabled` / `.maxAge` / `.includeSubdomains` / `.preload` | `true` / `31536000` / `false` / `false` | HSTS on all three hosts. |
| `ingress.certManager.enabled` | `true` | One Certificate per host. |
| `ingress.certManager.issuerName` / `.issuerKind` | `letsencrypt-prod` / `ClusterIssuer` | |
| `ingress.tlsSecretNames.studio` / `.api` / `.cdn` | `<fullname>-<host>-tls` | TLS Secret per host. |
| `ingress.extraMiddlewares.studio` / `.api` / `.cdn` | `[]` | Extra middleware refs (`{name, namespace}`), e.g. rate limiting on the API. |

### Network policies and metrics

| Value | Default | Meaning |
|---|---|---|
| `networkPolicy.enabled` | `true` | Policies for all components and the migration Job. |
| `networkPolicy.ingressController.namespaceSelector` / `.podSelector` | kube-system / `app.kubernetes.io/name: traefik` | Where traffic may come from. |
| `networkPolicy.dns.namespaceSelector` / `.podSelector` | kube-system / `k8s-app: kube-dns` | Cluster DNS (53/UDP+TCP). |
| `networkPolicy.egress.postgres` | `to: []`, 5432 | Server and migration Job; unused with `postgres.enabled`. |
| `networkPolicy.egress.objectStorage` | `to: []`, 443 | Server and edge; unused with `minio.enabled`. |
| `networkPolicy.egress.smtp` | `to: []`, 587 | Server, only when SMTP is configured. |
| `networkPolicy.egress.github` | `to: []`, 443 | Server → the GitHub API, only when `github.appId` is set. GitHub's ranges (`api` and `hooks` in `https://api.github.com/meta`) move, so pin `to` only if you will follow them. |
| `networkPolicy.egress.otlp` | `to: []`, 4318 | Server/edge, when their OTel endpoint is set. |
| `networkPolicy.egress.backupRemote` | `to: []`, 23 | The backup Jobs → the rclone remote (Storage Box SFTP). |
| `networkPolicy.extraIngress.{server,edge,studio,minio,postgres}` | `[]` | Extra `NetworkPolicyIngressRule`s. |
| `networkPolicy.extraEgress.{server,edge}` | `[]` | Extra `NetworkPolicyEgressRule`s. |
| `metrics.allowFrom` | `[]` | Peers that may scrape `/metrics` on server and edge. |
| `metrics.serviceMonitor.enabled` | `false` | ServiceMonitors for server and edge. |
| `metrics.serviceMonitor.interval` / `.scrapeTimeout` / `.labels` | `30s` / `10s` / `{}` | |

## Testing the chart

```sh
helm lint deploy/charts/glossa-platform --strict -f deploy/charts/glossa-platform/ci/minimal-values.yaml
helm template t deploy/charts/glossa-platform -f deploy/charts/glossa-platform/ci/full-values.yaml \
  | kubeconform -strict -summary -schema-location default \
      -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
```

`ci/*-values.yaml` follow chart-testing's layout (`ct lint` picks them
up): `minimal` (external S3, no SMTP), `full` (every optional feature,
external S3 and SMTP), `minio` (the in-namespace MinIO) and `postgres`
(the in-namespace Postgres and MinIO with every backup on).
`examples/values-klarlabs.yaml` holds the Klarlabs decisions, with
`TODO(infra)` markers for the questions still open. CI runs lint,
template and kubeconform for every one of them on each pull request that
touches the chart. In a cluster, `helm test <release>` runs the Postgres
and MinIO checks (`files/postgres-test.sh`, `files/minio-test.sh`).

`ci/e2e-database.sh` (Docker, helm, go, python3 with PyYAML) runs the
database scripts exactly as rendered from `ci/postgres-values.yaml`
against throwaway containers: Postgres as the StatefulSet runs it,
postgres-init, the real migrations, postgres-app-login, the `helm test`
script, glossa-server starting as `glossa_app`, a password rotation, the
backup (dump, checks, upload, scoped retention), the restore drill, the
restore runbook onto an empty volume and the MinIO mirror, and the
failures the checks exist for: `pg_dump` 17 against a 16 server, an
unrestorable dump, an empty required table, a stale dump, an emptied
bucket. CI runs it too.
