# Astrodome production deployment

This directory contains templates only. Production keeps the existing host
nginx installations; no nginx container is introduced.

## Runtime layout

- `/opt/docker/bot-astrosferum`: bot, PostgreSQL, ICON synchronizer and the
  isolated `directional_worker`;
- `/opt/docker/bot-astrosferum-web`: public web process only;
- `alice-bg`: public TLS termination for `astrosferum.com`;
- `dragon-he`: pinned-TLS origin on TCP 18082 and loopback web port 18083.

Both Compose projects join the pre-created `astrosferum-internal` network.
The bot and web process also have separate egress networks. PostgreSQL and the
worker do not. Create the shared network once:

```bash
docker network inspect astrosferum-internal >/dev/null 2>&1 || \
  docker network create --driver bridge --internal astrosferum-internal
```

Before Compose starts, create the bind-mount sources without symlinks:

```bash
install -d -m 0750 \
  /opt/docker/bot-astrosferum/data/models \
  /opt/docker/bot-astrosferum/data/cache/directional \
  /opt/docker/bot-astrosferum/data/cache/horizon \
  /opt/docker/bot-astrosferum/data/web/astrodome \
  /opt/docker/bot-astrosferum/data/state/run-leases \
  /opt/docker/bot-astrosferum/data/tmp/directional-worker \
  /opt/docker/bot-astrosferum/secrets \
  /opt/docker/bot-astrosferum-web/secrets

# The web image runs as UID 1000. Preserve collaborative administration on
# the host while giving only this bounded result directory to that process.
sudo install -d -o 1000 -g admin -m 2770 \
  /opt/docker/bot-astrosferum/data/web/astrodome

# Mandatory pre-start write probe; it leaves no file behind.
docker run --rm --user 1000:$(getent group admin | cut -d: -f3) \
  -v /opt/docker/bot-astrosferum/data/web/astrodome:/volume \
  --entrypoint /bin/sh bot_astrosferum:latest \
  -c 'probe=$(mktemp /volume/.write-probe.XXXXXX) && rm -f "$probe"'
```

The bot is the only process with a writable model-store mount. The worker sees
`/app/data/models` read-only and sees the coordinator staging directory at the
same absolute `/app/data/cache/directional` path. It has write access only to
directional workspaces, shared lease files, and its private temp tree. A stable
N-slot lease gate in `data/state/run-leases` enforces
`ASTRO_DIRECTIONAL_CONCURRENCY` across accidental extra worker replicas and the
operator-only `render-horizon` CLI; cancellation while waiting follows the
request context. `ASTRO_DIRECTIONAL_QUEUE_SIZE` separately bounds waiting FIFO
entries and does not change the active-slot count.

## Secrets and environment

Generate the internal directional credential once under the bot deployment:

```bash
umask 077
openssl rand -base64 48 > /opt/docker/bot-astrosferum/secrets/directional_credential
```

The bot, worker, and web Compose projects mount that one file. Do not copy its
value into `.env`. Create the other web secret files named by
`deploy/web/docker-compose.yml` with mode `0600`; the Telegram OIDC client
secret comes from BotFather, while CSRF and edge credentials should contain at
least 32 random bytes. Give the web process a dedicated least-privilege
PostgreSQL role; it must not use the bot owner role.

In BotFather Web Login, register both `https://astrosferum.com` and the exact
redirect URI `https://astrosferum.com/auth/telegram/callback`. Keep the default
`RS256` signing algorithm: the server deliberately rejects any other ID-token
algorithm. Store the displayed Client Secret only in
`secrets/telegram_oidc_client_secret` and put only the numeric Client ID in
`.env`.

The bot owner must run database migrations before the web process starts. Once
the current schema exists, create the login with an interactive `\password`
prompt and apply only these privileges as the database owner:

```sql
CREATE ROLE bot_astrosferum_web
  LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION
  NOBYPASSRLS CONNECTION LIMIT 8;
\password bot_astrosferum_web

GRANT CONNECT ON DATABASE bot_astrosferum TO bot_astrosferum_web;
GRANT USAGE ON SCHEMA public TO bot_astrosferum_web;
GRANT SELECT, INSERT, UPDATE, DELETE
  ON TABLE web_auth_transactions, web_sessions, web_astrodome_visualizations
  TO bot_astrosferum_web;
GRANT SELECT, INSERT, UPDATE
  ON TABLE bot_users
  TO bot_astrosferum_web;
GRANT SELECT ON TABLE saved_points TO bot_astrosferum_web;
```

Replace the role/database identifiers if the deployment overrides their
documented defaults; never interpolate an untrusted identifier into SQL.

No sequence privilege is needed: the web process does not insert into the
`saved_points` `bigserial`, and the three web tables have no generated sequence.
For every schema-changing release, start the bot/owner first, verify its
migration, review any new web query, and grant only the newly required table or
sequence privilege before starting the web process. Do not solve migration
order with broad default privileges.

Completed Astrodome visualizations use the bind mount
`data/web/astrodome`, not PostgreSQL large objects and not browser cache. The
database stores only owner-scoped catalogue metadata. On the first server-side
observation of a ready job, its result is immediately validated and atomically
persisted as gzip JSON under an opaque internal name. The expiry is exactly 96
hours after that successful archival. Only a successful rerun by the same
owner at the same canonical coordinates replaces the old file and resets the
TTL; a pending, failed, or cancelled rerun preserves the prior ready file and
its original expiry. The web process reconciles pending jobs even after the
browser closes and performs cleanup at startup and hourly.

The decoded dataset and the stored gzip member are each capped at `128 MiB`;
the whole visualization directory is capped at `32 GiB` and `8192` files.
Files are already compressed at publication, so no 24-hour archive or
compression job exists or should be added. This directory must be writable by
UID `1000`, must never be committed, and must never be served directly by
nginx.

Copy `.env.example` to `.env` separately in each deployment directory and set
only real runtime values there. The web deployment intentionally requires an
explicit `ASTRO_ASTRODOME_ENABLED`; keep it and `ASTRO_TELEGRAM_ADMIN_IDS`
identical to the bot stack. `true` grants access to all authenticated Telegram
users. `false` restricts access to the configured IDs; an empty list then
denies everyone. The flag is a rollout control, not a worker or model-sync kill
switch. Both web and bot enforce it, so a mismatch fails closed but causes an
avoidable denial of service.

The worker defaults to a 24 GB hard cgroup limit, a 12 GiB Go heap target and
eight CPUs on the 12-thread production host. The memory envelope is the
conservative initial 12/16/24 GB benchmark candidate
for the full footprint, CDO/ecCodes children and charged file cache, not a
fixed scientific requirement. Reduce it only when the smaller candidate
passes cold/warm Astrodome, simultaneous ICON sync, PSI, `memory.events`,
major-fault and ordinary-forecast latency checks. A worker failure never blocks
the bot container from starting; ordinary Telegram/VK forecasts remain
available while directional requests fail closed.

## Private TLS from alice-bg to dragon-he

Cookies and Telegram OIDC codes must not cross the public network over plain
HTTP. Generate a private self-signed origin leaf on `dragon-he`; keep its key
there and copy only the public certificate to `alice-bg`:

```bash
sudo install -d -m 0700 /etc/nginx/astrosferum-origin
sudo openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 825 \
  -subj '/CN=astrosferum-origin.internal' \
  -addext 'subjectAltName=DNS:astrosferum-origin.internal' \
  -keyout /etc/nginx/astrosferum-origin/tls.key \
  -out /etc/nginx/astrosferum-origin/tls.crt
sudo chmod 0600 /etc/nginx/astrosferum-origin/tls.key
sudo chmod 0644 /etc/nginx/astrosferum-origin/tls.crt
```

Install `nginx/dragon-he/astrosferum-origin.conf.example` as the Dragon site.
Create `/etc/nginx/snippets/astrosferum-edge-header.conf` with mode `0640` and
exactly one `proxy_set_header X-Astrosferum-Edge "...";` directive containing
the same random edge credential mounted by the web container. Restrict TCP
18082 to the alice-bg source address in both the host firewall and nginx allow
list. Validate before reloading:

```bash
sudo /usr/sbin/nginx -t && sudo systemctl reload nginx
```

On `alice-bg`, install the copied public origin certificate at the existing
host mount
`/home/container/docker/landing-nginx/certs/astrosferum-origin.crt`; the nginx
container sees it as `/etc/nginx/certs/astrosferum-origin.crt`. The proxy
template enables certificate verification, pins that trust file, sends SNI
`astrosferum-origin.internal`, and never needs the Dragon private key.

## First public certificate: bootstrap before TLS vhost

`nginx/alice-bg/astrosferum.com.conf` references Let's Encrypt files that do
not exist on a first deployment. Enabling it before issuance makes `nginx -t`
fail. Use this order:

1. Install only `astrosferum.com.bootstrap.conf` under the existing host
   `/home/container/docker/landing-nginx/nginx/conf.d` mount; do not copy the
   final vhost there yet.
2. Run `docker exec landing-nginx nginx -t`, reload the existing container,
   then issue the certificate with Certbot's
   **host** webroot `/home/container/docker/landing-nginx/site` for
   `astrosferum.com`. The existing nginx container sees that mount as
   `/usr/share/nginx/html`; passing the container path to host Certbot would
   place the challenge in the wrong directory.
3. Install `00-astrosferum-zones.conf` under the host
   `/home/container/docker/landing-nginx/nginx/conf.d` mount and
   `astrosferum-proxy.conf` under
   `/home/container/docker/landing-nginx/nginx/snippets`.
4. Remove the bootstrap file, install `astrosferum.com.conf`, run
   `docker exec landing-nginx nginx -t` again, and only then reload that
   existing nginx process.

After each successful Alice validation, reload without replacing the
container:

```bash
docker exec landing-nginx nginx -s reload
```

The issuance command therefore uses the host path:

```bash
sudo certbot certonly --webroot \
  --webroot-path /home/container/docker/landing-nginx/site \
  --domains astrosferum.com
```

The final Alice proxy verifies the pinned Dragon origin certificate. A
successful public certificate alone is not a reason to disable origin TLS.

## Compose validation and start order

Validate interpolation without printing or committing real secrets:

```bash
cd /opt/docker/bot-astrosferum
POSTGRES_PASSWORD=validation-only docker compose config --quiet

cd /opt/docker/bot-astrosferum-web
ASTRO_WEB_OIDC_CLIENT_ID=123456789 ASTRO_ASTRODOME_ENABLED=false \
  docker compose config --quiet
```

Build and start the bot stack first so the stable image, PostgreSQL, internal
gateway and worker exist; then start the web Compose project. Useful probes:

```bash
cd /opt/docker/bot-astrosferum
docker compose exec directional_worker \
  /usr/local/bin/bot_astrosferum_directional_worker \
  --healthcheck http://127.0.0.1:18084/healthz

cd /opt/docker/bot-astrosferum-web
docker compose exec web /usr/local/bin/bot_astrosferum_web \
  --healthcheck http://127.0.0.1:8080/healthz

curl --fail --silent --show-error https://astrosferum.com/healthz
curl --fail --silent --show-error https://astrosferum.com/readyz
```

`healthz` is liveness. `readyz` additionally forces PostgreSQL to resolve and
authorize every required web table and verifies that the web process can
create and remove a probe in `data/web/astrodome/staging`. A successful probe
therefore checks both schema/grants and the UID-`1000` writable bind mount. On
the host, keep `/opt/docker/bot-astrosferum/data/web/astrodome` owned by UID
`1000`, group `admin`, and mode `2770`: the setgid bit preserves the shared
administrator group on newly created subdirectories while retaining the
container's bounded write access.
Admission and dataset delivery are asynchronous; the 15-minute nginx read
timeout applies to individual web responses and does not bound a queued or
running directional calculation. Production sets both the Astrodome job
deadline and the internal bot-to-worker request deadline to `0s`, so an active
calculation has no wall-clock cutoff; explicit cancellation, shutdown, process
failure or a broken worker connection still stop it. The user-facing cold-run
queue estimate is `30m` and is presentation metadata only, not a deadline.
