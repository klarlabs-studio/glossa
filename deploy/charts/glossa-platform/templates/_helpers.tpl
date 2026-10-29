{{/* ── Names and labels ─────────────────────────────────────────── */}}

{{- define "gp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Prefix of every resource name. */}}
{{- define "gp.fullname" -}}
{{- default .Release.Name .Values.fullnameOverride | trunc 50 | trimSuffix "-" -}}
{{- end -}}

{{/* Name of a component's resources: (dict "root" $ "component" "server"). */}}
{{- define "gp.componentName" -}}
{{- printf "%s-%s" (include "gp.fullname" .root) .component | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "gp.labels" -}}
app.kubernetes.io/name: {{ include "gp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: glossa
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/* (dict "root" $ "component" "server") */}}
{{- define "gp.componentLabels" -}}
{{ include "gp.labels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* The pod template labels of a stateful component (Postgres, MinIO):
     gp.componentLabels without the version and chart labels. Those change
     with every release, and a changed pod template rolls the pod, so on
     a StatefulSet they would restart the database for nothing (#106).
     (dict "root" $ "component" "postgres") */}}
{{- define "gp.statefulPodLabels" -}}
app.kubernetes.io/name: {{ include "gp.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
app.kubernetes.io/part-of: glossa
{{- with .root.Values.commonLabels }}
{{ toYaml . }}
{{- end }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* (dict "root" $ "component" "server") */}}
{{- define "gp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gp.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* ── Images ───────────────────────────────────────────────────── */}}

{{/* One of Glossa's own images.
     (dict "root" $ "component" "server" "image" .Values.server.image)

     The tag defaults to v<appVersion>, the tag Kiln pushes for a
     release (.kiln.yaml). A digest, when
     set, is appended as @sha256:… and is what actually gets pulled; the
     tag stays for readability. With image.requireDigest the chart
     refuses to render an unpinned Glossa image at all, so a deployment
     that must be reproducible cannot silently fall back to a tag. */}}
{{- define "gp.image" -}}
{{- $c := .component -}}
{{- $repo := required (printf "%s.image.repository is required (e.g. klarlabs-studio/glossa-%s)" $c $c) .image.repository -}}
{{- $tag := .image.tag | default (printf "v%s" .root.Chart.AppVersion) -}}
{{- if not $tag -}}
{{- fail (printf "%s.image.tag is empty and the chart has no appVersion to fall back to" $c) -}}
{{- end -}}
{{- $digest := .image.digest | default "" -}}
{{- if $digest -}}
{{- if not (regexMatch "^sha256:[0-9a-f]{64}$" $digest) -}}
{{- fail (printf "%s.image.digest must look like sha256:<64 hex chars> (got %q)" $c $digest) -}}
{{- end -}}
{{- else if .root.Values.image.requireDigest -}}
{{- fail (printf "image.requireDigest is set but %s.image.digest is empty: pin %s/%s:%s by the digest the release published (deploy/charts/glossa-platform/README.md, \"Cutting a release\")" $c .root.Values.image.registry $repo $tag) -}}
{{- end -}}
{{- $ref := printf "%s/%s:%s" .root.Values.image.registry $repo $tag -}}
{{- if $digest -}}
{{- $ref = printf "%s@%s" $ref $digest -}}
{{- end -}}
{{- $ref -}}
{{- end -}}

{{/* An image outside image.registry, by full repository:
     (dict "image" .Values.minio.image). */}}
{{- define "gp.externalImage" -}}
{{- $ref := printf "%s:%s" .image.repository .image.tag -}}
{{- if .image.digest -}}
{{- $ref = printf "%s@%s" $ref .image.digest -}}
{{- end -}}
{{- $ref -}}
{{- end -}}

{{/* ── Hosts and URLs ───────────────────────────────────────────── */}}

{{- define "gp.host.studio" -}}
{{- required "hosts.studio is required (e.g. glossa.<domain>)" .Values.hosts.studio -}}
{{- end -}}

{{/* The public API host: hosts.api, or the Studio host when hosts.api is
     empty (the API is then served under /v1 of Studio). */}}
{{- define "gp.host.api" -}}
{{- .Values.hosts.api | default (include "gp.host.studio" .) -}}
{{- end -}}

{{/* "true" when the API has its own host, distinct from Studio's. */}}
{{- define "gp.host.apiSeparate" -}}
{{- if ne (include "gp.host.api" .) (include "gp.host.studio" .) }}true{{ end -}}
{{- end -}}

{{- define "gp.host.cdn" -}}
{{- required "hosts.cdn is required (e.g. cdn.<domain>)" .Values.hosts.cdn -}}
{{- end -}}

{{- define "gp.studioUrl" -}}
{{- printf "https://%s" (include "gp.host.studio" .) -}}
{{- end -}}

{{/* ── Pod hardening (PodSecurity "restricted") ─────────────────── */}}

{{/* Pod securityContext; argument: the numeric uid/gid of the image. */}}
{{- define "gp.podSecurityContext" -}}
runAsNonRoot: true
runAsUser: {{ . }}
runAsGroup: {{ . }}
fsGroup: {{ . }}
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "gp.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
runAsNonRoot: true
capabilities:
  drop: [ALL]
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{/* Default soft spread of a component's pods across nodes. */}}
{{- define "gp.topologySpread" -}}
{{- if .values.topologySpreadConstraints -}}
{{ toYaml .values.topologySpreadConstraints }}
{{- else -}}
- maxSkew: 1
  topologyKey: kubernetes.io/hostname
  whenUnsatisfiable: ScheduleAnyway
  labelSelector:
    matchLabels:
      {{- include "gp.selectorLabels" (dict "root" .root "component" .component) | nindent 6 }}
{{- end -}}
{{- end -}}

{{/* nodeSelector, tolerations, affinity, pull secrets of a component. */}}
{{- define "gp.scheduling" -}}
{{- with .root.Values.image.pullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .values.nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .values.tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .values.affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}

{{/* ── Mail ─────────────────────────────────────────────────────── */}}

{{/* The effective GLOSSA_MAIL_DRIVER: mail.driver, else smtp when an SMTP
     server is set, else "" (the variable is left unset). */}}
{{- define "gp.mailDriver" -}}
{{- .Values.mail.driver | default (ternary "smtp" "" (not (empty .Values.mail.smtp.addr))) -}}
{{- end -}}

{{/* ── Secret references ────────────────────────────────────────── */}}

{{/* (dict "name" "ENV_NAME" "secret" "secret-name" "key" "key") */}}
{{- define "gp.secretEnv" -}}
- name: {{ .name }}
  valueFrom:
    secretKeyRef:
      name: {{ .secret }}
      key: {{ .key }}
{{- end -}}

{{/* DATABASE_URL (glossa_app): database.app's Secret, else with
     postgres.enabled a DSN for the in-namespace Postgres built from
     glossa_app's password. */}}
{{- define "gp.databaseAppEnv" -}}
{{- if .Values.database.app.secretName -}}
{{- include "gp.secretEnv" (dict "name" "DATABASE_URL" "secret" .Values.database.app.secretName "key" .Values.database.app.secretKey) }}
{{- else if .Values.postgres.enabled -}}
{{- include "gp.secretEnv" (dict "name" "GLOSSA_DB_APP_PASSWORD" "secret" (include "gp.postgres.appSecret" .) "key" .Values.postgres.app.passwordKey) }}
- name: DATABASE_URL
  value: {{ include "gp.postgres.dsn" (dict "root" . "user" "glossa_app" "passwordVar" "GLOSSA_DB_APP_PASSWORD" "extra" (printf "pool_max_conns=%d" (int .Values.postgres.app.poolMaxConns))) | quote }}
{{- else -}}
{{- fail "database.app.secretName is required (Secret with the glossa_app DSN), or set postgres.enabled" -}}
{{- end -}}
{{- end -}}

{{/* MIGRATION_DATABASE_URL (the schema owner), like gp.databaseAppEnv. */}}
{{- define "gp.databaseMigrationEnv" -}}
{{- if .Values.database.migration.secretName -}}
{{- include "gp.secretEnv" (dict "name" "MIGRATION_DATABASE_URL" "secret" .Values.database.migration.secretName "key" .Values.database.migration.secretKey) }}
{{- else if .Values.postgres.enabled -}}
{{- include "gp.secretEnv" (dict "name" "GLOSSA_DB_OWNER_PASSWORD" "secret" (include "gp.postgres.ownerSecret" .) "key" .Values.postgres.owner.passwordKey) }}
- name: MIGRATION_DATABASE_URL
  value: {{ include "gp.postgres.dsn" (dict "root" . "user" .Values.postgres.owner.username "passwordVar" "GLOSSA_DB_OWNER_PASSWORD" "extra" "") | quote }}
{{- else -}}
{{- fail "database.migration.secretName is required when server.migrations.enabled (Secret with the owner DSN), or set postgres.enabled" -}}
{{- end -}}
{{- end -}}

{{- define "gp.authEnv" -}}
{{- include "gp.secretEnv" (dict "name" "GLOSSA_AUTH_SECRET" "secret" (required "auth.secretName is required (Secret with GLOSSA_AUTH_SECRET)" .Values.auth.secretName) "key" .Values.auth.secretKey) }}
{{- end -}}

{{/* ── Object storage ───────────────────────────────────────────── */}}

{{/* host:port of the in-namespace MinIO Service. */}}
{{- define "gp.minio.service" -}}
{{- printf "%s:9000" (include "gp.componentName" (dict "root" . "component" "minio")) -}}
{{- end -}}

{{/* GLOSSA_S3_ENDPOINT: objectStorage.endpoint, which defaults to the
     in-namespace MinIO when minio.enabled. */}}
{{- define "gp.storage.endpoint" -}}
{{- if .Values.minio.enabled -}}
{{- .Values.objectStorage.endpoint | default (include "gp.minio.service" .) -}}
{{- else -}}
{{- required "objectStorage.endpoint is required (host[:port], no scheme), or set minio.enabled" .Values.objectStorage.endpoint -}}
{{- end -}}
{{- end -}}

{{/* MinIO's S3 URL as mc sees it: in-cluster plain HTTP. */}}
{{- define "gp.minio.url" -}}
{{- printf "http://%s" (include "gp.storage.endpoint" .) -}}
{{- end -}}

{{- define "gp.storage.bucket" -}}
{{- required "objectStorage.bucket is required" .Values.objectStorage.bucket -}}
{{- end -}}

{{- define "gp.storage.serverSecret" -}}
{{- required "objectStorage.server.secretName is required (Secret with read-write S3 credentials)" .Values.objectStorage.server.secretName -}}
{{- end -}}

{{/* glossa-edge's credentials as YAML {secretName, accessKeyIdKey,
     secretAccessKeyKey}: objectStorage.edge, else the server's. With
     MinIO the chart provisions a separate read-only user, so the edge
     must have its own Secret then. */}}
{{- define "gp.storage.edgeCreds" -}}
{{- $s := .Values.objectStorage -}}
{{- if $s.edge.secretName -}}
{{- if and .Values.minio.enabled (eq $s.edge.secretName $s.server.secretName) (eq $s.edge.accessKeyIdKey $s.server.accessKeyIdKey) -}}
{{- fail "objectStorage.edge reuses objectStorage.server's access key (same Secret and key); with minio.enabled the edge gets its own read-only user" -}}
{{- end -}}
{{- toYaml $s.edge -}}
{{- else if .Values.minio.enabled -}}
{{- fail "objectStorage.edge.secretName is required with minio.enabled (glossa-edge's read-only MinIO user)" -}}
{{- else -}}
{{- $_ := required "objectStorage.edge.secretName or objectStorage.server.secretName is required" $s.server.secretName -}}
{{- toYaml $s.server -}}
{{- end -}}
{{- end -}}

{{/* Env of the mc containers (bootstrap Job, helm test): MinIO, the
     bucket and both application users' credentials. */}}
{{- define "gp.minio.mcEnv" -}}
{{- $s := .Values.objectStorage -}}
{{- $edge := include "gp.storage.edgeCreds" . | fromYaml -}}
{{- $serverSecret := include "gp.storage.serverSecret" . -}}
- name: MINIO_URL
  value: {{ include "gp.minio.url" . | quote }}
- name: BUCKET
  value: {{ include "gp.storage.bucket" . | quote }}
- name: OBJECT_PREFIX
  value: {{ trimAll "/" $s.prefix | quote }}
- name: MC_CONFIG_DIR
  value: /tmp/.mc
- name: HOME
  value: /tmp
{{ include "gp.secretEnv" (dict "name" "RW_ACCESS_KEY" "secret" $serverSecret "key" $s.server.accessKeyIdKey) }}
{{ include "gp.secretEnv" (dict "name" "RW_SECRET_KEY" "secret" $serverSecret "key" $s.server.secretAccessKeyKey) }}
{{ include "gp.secretEnv" (dict "name" "RO_ACCESS_KEY" "secret" $edge.secretName "key" $edge.accessKeyIdKey) }}
{{ include "gp.secretEnv" (dict "name" "RO_SECRET_KEY" "secret" $edge.secretName "key" $edge.secretAccessKeyKey) }}
{{- end -}}

{{/* Object storage env; (dict "root" $ "creds" .Values.objectStorage.server "secretName" "…"). */}}
{{- define "gp.storageEnv" -}}
{{- $s := .root.Values.objectStorage -}}
{{- $minio := .root.Values.minio.enabled -}}
- name: GLOSSA_STORAGE_DRIVER
  value: s3
- name: GLOSSA_S3_ENDPOINT
  value: {{ include "gp.storage.endpoint" .root | quote }}
- name: GLOSSA_S3_BUCKET
  value: {{ include "gp.storage.bucket" .root | quote }}
- name: GLOSSA_S3_REGION
  value: {{ $s.region | quote }}
{{- with $s.prefix }}
- name: GLOSSA_S3_PREFIX
  value: {{ . | quote }}
{{- end }}
{{- /* The in-namespace MinIO: path-style requests and plain HTTP on the
       pod network, where NetworkPolicy admits only server and edge. */}}
- name: GLOSSA_S3_PATH_STYLE
  value: {{ or $minio $s.pathStyle | quote }}
- name: GLOSSA_S3_INSECURE
  value: {{ or $minio $s.insecure | quote }}
- name: GLOSSA_S3_TIMEOUT
  value: {{ $s.timeout | quote }}
{{ include "gp.secretEnv" (dict "name" "GLOSSA_S3_ACCESS_KEY_ID" "secret" .secretName "key" .creds.accessKeyIdKey) }}
{{ include "gp.secretEnv" (dict "name" "GLOSSA_S3_SECRET_ACCESS_KEY" "secret" .secretName "key" .creds.secretAccessKeyKey) }}
{{- end -}}

{{/* ── In-namespace Postgres ─────────────────────────────────────── */}}

{{/* Host of the in-namespace Postgres Service. */}}
{{- define "gp.postgres.host" -}}
{{- include "gp.componentName" (dict "root" . "component" "postgres") -}}
{{- end -}}

{{- define "gp.postgres.superuserSecret" -}}
{{- required "postgres.superuser.secretName is required with postgres.enabled (existing Secret with the superuser's password)" .Values.postgres.superuser.secretName -}}
{{- end -}}

{{- define "gp.postgres.ownerSecret" -}}
{{- required "postgres.owner.secretName is required with postgres.enabled (existing Secret with the schema owner's password)" .Values.postgres.owner.secretName -}}
{{- end -}}

{{- define "gp.postgres.appSecret" -}}
{{- required "postgres.app.secretName is required with postgres.enabled (existing Secret with glossa_app's password)" .Values.postgres.app.secretName -}}
{{- end -}}

{{- define "gp.backup.roleSecret" -}}
{{- required "backup.postgres.role.secretName is required with backups on (existing Secret with the backup role's password)" .Values.backup.postgres.role.secretName -}}
{{- end -}}

{{/* A keyword/value DSN for the in-namespace Postgres; the password is a
     $(VAR) reference to an env var defined before it (Kubernetes expands
     it). Keyword/value rather than a URL: a password needs no percent-
     encoding, only no ' or \ (the bootstrap Job refuses those).
     (dict "root" $ "user" "…" "passwordVar" "…" "extra" "k=v …") */}}
{{- define "gp.postgres.dsn" -}}
{{- $r := .root -}}
{{- $dsn := printf "host=%s port=5432 dbname=%s user=%s password='$(%s)' sslmode=disable" (include "gp.postgres.host" $r) $r.Values.postgres.database .user .passwordVar -}}
{{- if .extra }}{{ $dsn = printf "%s %s" $dsn .extra }}{{ end -}}
{{- $dsn -}}
{{- end -}}

{{/* libpq env (PGHOST, PGPORT, PGDATABASE, PGUSER, PGPASSWORD) for psql
     and pg_dump against the in-namespace Postgres:
     (dict "root" $ "user" "…" "secret" "…" "key" "…" "database" "…"). */}}
{{- define "gp.postgres.libpqEnv" -}}
- name: PGHOST
  value: {{ include "gp.postgres.host" .root | quote }}
- name: PGPORT
  value: "5432"
- name: PGDATABASE
  value: {{ .database | default .root.Values.postgres.database | quote }}
- name: PGUSER
  value: {{ .user | quote }}
{{ include "gp.secretEnv" (dict "name" "PGPASSWORD" "secret" .secret "key" .key) }}
- name: PGCONNECT_TIMEOUT
  value: "10"
{{- end -}}

{{/* When the database hooks (Postgres bootstrap, migrations, glossa_app's
     login) run. An external database exists before the release, so they
     run pre-install. The in-namespace Postgres is created BY the release,
     so on the first install they can only run post-install; on upgrades
     it already exists and they run pre-upgrade, before the new server
     version rolls out. */}}
{{- define "gp.db.hook" -}}
{{- ternary "post-install,pre-upgrade" "pre-install,pre-upgrade" (not (not .Values.postgres.enabled)) -}}
{{- end -}}

{{/* The pod securityContext of the postgres image (alpine: uid/gid 70). */}}
{{- define "gp.postgres.uid" -}}70{{- end -}}

{{/* Is the component rendered? (backup CronJobs need their data source.) */}}
{{- define "gp.backup.postgres.enabled" -}}
{{- if and .Values.backup.enabled .Values.backup.postgres.enabled .Values.postgres.enabled }}true{{ end -}}
{{- end -}}

{{- define "gp.backup.minio.enabled" -}}
{{- if and .Values.backup.enabled .Values.backup.minio.enabled .Values.minio.enabled }}true{{ end -}}
{{- end -}}

{{- define "gp.backup.restoreTest.enabled" -}}
{{- if and (include "gp.backup.postgres.enabled" .) .Values.backup.restoreTest.enabled }}true{{ end -}}
{{- end -}}

{{/* An rclone remote path "<remote>:<path>"; the remote's root is refused,
     because retention deletes below it. (dict "value" … "name" "…") */}}
{{- define "gp.backup.remote" -}}
{{- $v := required (printf "%s is required with backups on (an rclone remote path, e.g. storagebox:db/glossa-platform)" .name) .value -}}
{{- if not (regexMatch "^[A-Za-z0-9_.-]+:[^/].*[^/]$|^[A-Za-z0-9_.-]+:[^/]$" $v) -}}
{{- fail (printf "%s must be <remote>:<directory> without a leading or trailing /, never a remote's root (got %q)" .name $v) -}}
{{- end -}}
{{- $v -}}
{{- end -}}

{{/* rclone's environment and config Secret volume (mounted at /etc/rclone,
     where rclone.conf's key_file and known_hosts_file point). */}}
{{- define "gp.backup.rcloneEnv" -}}
- name: RCLONE_CONFIG
  value: /etc/rclone/{{ .Values.backup.rclone.configKey }}
- name: RCLONE_CACHE_DIR
  value: /tmp/rclone-cache
- name: HOME
  value: /tmp
{{- end -}}

{{- define "gp.backup.rcloneVolume" -}}
- name: rclone-config
  secret:
    secretName: {{ required "backup.rclone.configSecretName is required with backup.enabled (Secret with rclone.conf and the keys it references)" .Values.backup.rclone.configSecretName }}
    # Group-readable: the pod's fsGroup owns the files, the uid differs.
    defaultMode: 0440
{{- end -}}

{{/* age for the dumps, in an init container: no backup image installs
     anything at runtime (restricted profile, read-only root filesystem), so
     fetch-age downloads the pinned release into /tools and checks its
     SHA-256. (include "gp.backup.ageFetch" $) */}}
{{- define "gp.backup.ageFetch" -}}
- name: fetch-age
  image: {{ .Values.backup.age.image | quote }}
  imagePullPolicy: {{ .Values.image.pullPolicy }}
  command: ["/bin/sh", "-c"]
  args:
    - {{ .Files.Get "files/fetch-age.sh" | quote }}
  securityContext:
    {{- include "gp.containerSecurityContext" . | nindent 4 }}
  env:
    - name: AGE_VERSION
      value: {{ .Values.backup.age.version | quote }}
    - name: AGE_SHA256
      value: {{ .Values.backup.age.sha256 | quote }}
  volumeMounts:
    - name: tools
      mountPath: /tools
    - name: tmp
      mountPath: /tmp
{{- end -}}

{{/* The volumes of age: the fetched binaries and the drill identity (the
     Secret's key "identity", mounted at /etc/backup-age). */}}
{{- define "gp.backup.ageVolumes" -}}
- name: tools
  emptyDir:
    sizeLimit: 64Mi
- name: backup-age
  secret:
    secretName: {{ required "backup.age.drillSecretName is required with backup.postgres.enabled (Secret with the drill identity under the key identity)" .Values.backup.age.drillSecretName }}
    defaultMode: 0440
{{- end -}}

{{/* The MinIO mirror's crypt passwords (keys password and password2),
     mounted at /etc/minio-crypt. */}}
{{- define "gp.backup.minioCryptVolume" -}}
- name: minio-crypt
  secret:
    secretName: {{ required "backup.minio.cryptSecretName is required with backup.minio.enabled (Secret with the keys password and password2)" .Values.backup.minio.cryptSecretName }}
    defaultMode: 0440
{{- end -}}

{{/* ── NetworkPolicy fragments ──────────────────────────────────── */}}

{{- define "gp.np.dnsEgress" -}}
- to:
    - namespaceSelector:
        {{- toYaml .Values.networkPolicy.dns.namespaceSelector | nindent 8 }}
      podSelector:
        {{- toYaml .Values.networkPolicy.dns.podSelector | nindent 8 }}
  ports:
    - port: 53
      protocol: UDP
    - port: 53
      protocol: TCP
{{- end -}}

{{/* An egress rule from {to, ports}; an empty `to` means any destination. */}}
{{- define "gp.np.egressRule" -}}
- ports:
    {{- toYaml .ports | nindent 4 }}
  {{- with .to }}
  to:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end -}}

{{/* Egress to object storage: the in-namespace MinIO pod on 9000 with
     minio.enabled (replacing networkPolicy.egress.objectStorage), else
     networkPolicy.egress.objectStorage. */}}
{{- define "gp.np.objectStorageEgress" -}}
{{- if .Values.minio.enabled -}}
- to:
    - podSelector:
        matchLabels:
          {{- include "gp.selectorLabels" (dict "root" . "component" "minio") | nindent 10 }}
  ports:
    - port: 9000
      protocol: TCP
{{- else -}}
{{- include "gp.np.egressRule" .Values.networkPolicy.egress.objectStorage -}}
{{- end -}}
{{- end -}}

{{/* Egress to Postgres: the in-namespace Postgres pod on 5432 with
     postgres.enabled (replacing networkPolicy.egress.postgres), else
     networkPolicy.egress.postgres. */}}
{{- define "gp.np.postgresEgress" -}}
{{- if .Values.postgres.enabled -}}
- to:
    - podSelector:
        matchLabels:
          {{- include "gp.selectorLabels" (dict "root" . "component" "postgres") | nindent 10 }}
  ports:
    - port: 5432
      protocol: TCP
{{- else -}}
{{- include "gp.np.egressRule" .Values.networkPolicy.egress.postgres -}}
{{- end -}}
{{- end -}}

{{/* Egress to the rclone remote of the backups (outside the cluster). */}}
{{- define "gp.np.backupRemoteEgress" -}}
{{- include "gp.np.egressRule" .Values.networkPolicy.egress.backupRemote -}}
{{- end -}}

{{/* Egress to GitHub's release downloads, for fetch-age (the backup and restore pods). */}}
{{- define "gp.np.backupAgeDownloadEgress" -}}
{{- include "gp.np.egressRule" .Values.networkPolicy.egress.backupAgeDownload -}}
{{- end -}}

{{/* A NetworkPolicy for a hook pod: no ingress; egress to DNS plus the
     given rules. (dict "root" $ "component" "…" "hook" "…" "egress" "<yaml>") */}}
{{- define "gp.np.hookPolicy" -}}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ include "gp.componentName" (dict "root" .root "component" .component) }}
  labels:
    {{- include "gp.componentLabels" (dict "root" .root "component" .component) | nindent 4 }}
  annotations:
    helm.sh/hook: {{ .hook }}
    helm.sh/hook-weight: "-10"
    helm.sh/hook-delete-policy: before-hook-creation
spec:
  podSelector:
    matchLabels:
      {{- include "gp.selectorLabels" (dict "root" .root "component" .component) | nindent 6 }}
  policyTypes: [Ingress, Egress]
  ingress: []
  egress:
    {{- include "gp.np.dnsEgress" .root | nindent 4 }}
    {{- .egress | nindent 4 }}
{{- end -}}

{{/* Ingress from the ingress controller to a port. */}}
{{- define "gp.np.fromIngressController" -}}
- from:
    - namespaceSelector:
        {{- toYaml .root.Values.networkPolicy.ingressController.namespaceSelector | nindent 8 }}
      podSelector:
        {{- toYaml .root.Values.networkPolicy.ingressController.podSelector | nindent 8 }}
  ports:
    - port: {{ .port }}
      protocol: TCP
{{- end -}}

{{/* Ingress from metrics scrapers to a port. */}}
{{- define "gp.np.fromScrapers" -}}
- from:
    {{- toYaml .root.Values.metrics.allowFrom | nindent 4 }}
  ports:
    - port: {{ .port }}
      protocol: TCP
{{- end -}}
