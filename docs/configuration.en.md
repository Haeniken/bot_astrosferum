# Configuration reference

[Русская версия](configuration.ru.md)

This page covers the environment variables in `.env` and the related fields in
`config/config.yaml`. Copy `.env.example` to the ignored `.env`. Only
`POSTGRES_PASSWORD` is required by Compose; every other variable is optional.
Platform tokens deliberately do not belong in `.env`: keep them in
`secrets/telegram_token` and `secrets/vk_token` with mode `0600`.
`ASTRO_DB_PASSWORD` is populated inside the application container from
`POSTGRES_PASSWORD` and should not be set separately.

## Runtime and access

| Variable | Required | Recommended value | Effect |
|---|---|---|---|
| `POSTGRES_PASSWORD` | Yes | A unique random value of at least 32 characters | PostgreSQL and application database password. Changing it after database initialization also requires changing the database role password. |
| `POSTGRES_DB` | No | `bot_astrosferum` | PostgreSQL database name. Keep the default unless integrating with an existing PostgreSQL installation. |
| `POSTGRES_USER` | No | `bot_astrosferum` | PostgreSQL role name. Keep the default for the bundled Compose deployment. |
| `ASTRO_TELEGRAM_ADMIN_IDS` | No | Comma-separated numeric IDs, or empty | Grants the Telegram `/admin` and statistics UI. It does not filter statistics: every configured admin sees the combined Telegram + VK totals. |
| `ASTRO_VK_ADMIN_IDS` | No | Comma-separated numeric VK user IDs, or empty | Grants the VK admin/statistics UI. Use numeric user IDs, not screen names; the report is the same combined Telegram + VK report. |
| `ASTRO_LIGHT_POLLUTION_ATLAS_YEAR` | No | `2024` | Pins the operator-verified annual Light Pollution Atlas dataset; it is not advanced automatically. Change only after verifying and provisioning a supported dataset. |
| `ASTRO_FORECAST_CONCURRENCY` | No | `2` | Shared maximum number of ordinary forecast calculations running at once across Telegram, VK, and the website. Further requests wait in the same queue. |
| `ASTRO_ICON_DOWNLOAD_LIMIT_MBIT` | No | Unset | Aggregate decimal-Mbit/s limit shared by simultaneous ICON-EU and ICON Global downloads. Unset, empty, or `0` means unlimited. |
| `ASTRO_GEOS_CF_ENABLED` | No | `true` | Enables the independent NASA GEOS-CF AOD550/ozone enrichment used only by the night-time Reference V-band diagnostic. A disabled, stale, slow, or unavailable source removes the ring but never fails or changes the primary ICON Overall forecast. |

The related provider settings are in YAML because they describe one external
data source rather than user calibration:

```yaml
providers:
  geos_cf:
    enabled: true
    dataset_url: https://opendap.nccs.nasa.gov/dods/gmao/geos-cf/v2/fcst/xgc_tavg_1hr_glo_L1440x721_slv.latest
    request_timeout: 60s
    metadata_cache_ttl: 10m
    data_cache_ttl: 6h
    max_stale_age: 48h
    cache_entries: 256
```

The HTTP timeout lets a cold OPeNDAP request finish and populate the bounded
RAM cache. The application waits only five additional seconds after the ICON
inputs are ready; on timeout it renders without the Reference-V ring while a
service-owned operation may continue for at most 65 seconds. That operation is
cancelled during bot shutdown. Unique point-slab downloads are fail-fast
limited to `ASTRO_FORECAST_CONCURRENCY`; identical requests share one flight,
so a slow public source cannot create an unbounded background backlog. Metadata
refresh detects a new composition reference time, and the render-cache identity
includes the algorithm/passband versions and every interpolated AOD/ozone
value, so newer data or formulas cannot reuse an older image.

Bundled Compose always supplies `ASTRO_GEOS_CF_ENABLED`, defaulting it to
`true`; therefore set `ASTRO_GEOS_CF_ENABLED=false` in `.env` to disable the
provider under Compose. YAML-only `enabled: false` applies to direct execution
when the environment variable is absent.

## Optional ICON-EU horizon analysis

This heavier analysis is available only when the selected forecast provider is
ICON-EU. A forecast served by ICON Global never contains its action button.
Production execution shares one bounded directional FIFO and one configurable
active-slot limit with Astrodome; it remains independent of ordinary forecast
workers. The recommended rollout value is one active heavy calculation.

| Variable | Required | Recommended value | Effect |
|---|---|---|---|
| `ASTRO_HORIZON_ANALYSIS_ENABLED` | No | `true` | Overrides `horizon_analysis.enabled`. When `false`, the application does not initialize the Horizon data source or show the action button. The shared directional FIFO may still serve Astrodome; the ordinary seven-chart forecast is unaffected. |
| `ASTRO_HORIZON_CONCURRENCY` | No | `1` | Legacy Horizon constructor bound. Keep `1`; production scheduling is controlled only by `ASTRO_DIRECTIONAL_CONCURRENCY`, independently of `ASTRO_FORECAST_CONCURRENCY`. |

The bundled Compose file always sets this variable, using `true` when it is
absent from `.env`. Consequently its environment value always wins over YAML
under Compose: set `ASTRO_HORIZON_ANALYSIS_ENABLED=false` in `.env` to disable
the feature. A YAML-only `enabled: false` applies to direct/non-Compose runs
only when the environment switch is genuinely absent.

The operational limits live in `config/config.yaml`:

```yaml
horizon_analysis:
  enabled: true
  queue_size: 4
  concurrency: 1
  cdo_workers: 8
  job_timeout: 10m
  cache_ttl: 48h
  cache_entries: 128
  estimated_duration: 3m
```

| YAML field | Recommended default | Effect |
|---|---:|---|
| `enabled` | `true` | Enables the ICON-EU-only feature when no environment override is present. `false` prevents the queue and source from starting and removes the action button. Under bundled Compose, use the environment switch described above. |
| `queue_size` | `4` | Legacy local-constructor bound. Production admission is bounded by `directional.queue_size`; this field does not create a second queue. |
| `concurrency` | `1` | Legacy local-constructor bound. Keep `1`; it never raises the shared production worker concurrency. |
| `cdo_workers` | `8` | Shared limit for concurrent CDO subprocesses used by Horizon and Astrodome native-column preload in the isolated directional worker. Validation accepts `1..16`, and both runners consume this exact setting. Astrodome validates every source-message grid and uses `gennn` only to collect exact native-index targets before its own bilinear reconstruction; SCRIP addresses and bit-exact unit weights are proved before the plan is reused read-only through `remap`. Raising this value requires CPU, memory, and ordinary-forecast non-regression measurements. |
| `job_timeout` | `10m` | Maximum execution time of one horizon-analysis job before it is cancelled. |
| `cache_ttl` | `48h` | Maximum age of a completed cached result before it becomes ineligible for reuse. Expired entries are removed at startup, periodically while the worker runs, and during cache publication. |
| `cache_entries` | `128` | Maximum number of completed horizon-analysis results retained in its bounded cache. |
| `estimated_duration` | `3m` | User-facing duration estimate. It does not change the timeout or worker scheduling and is not a guaranteed completion time. |

Keep the recommended limits unless load measurements justify changing them.
Enabling Horizon does not make it available for ICON Global points.

## Shared directional execution and Astrodome

Horizon and the directional atmospheric Astrodome share one coordinator, one bounded FIFO and
one isolated worker service. The active-slot count is explicit and common to
the bot coordinator, worker and operator CLI. Keep it at one until full-dome
memory, CPU and ordinary-forecast latency benchmarks justify more.

| Variable | Required | Recommended value | Effect |
|---|---|---|---|
| `ASTRO_DIRECTIONAL_QUEUE_SIZE` | No | `8` | Maximum queued Horizon + Astrodome jobs. A full FIFO rejects new admission; identical identities may share one calculation. |
| `ASTRO_DIRECTIONAL_CONCURRENCY` | No | `1` until benchmarked | Shared active Horizon + Astrodome calculations. Validation accepts `1..32`. Every additional Astrodome slot can consume the full resident limit, so bot, worker, and operator CLI must use the same value and the cgroup/disk budgets must be revalidated before increasing it. |
| `ASTRO_ASTRODOME_ENABLED` | No | `true` after rollout | Public-access switch, not a worker kill switch. `true` permits every authenticated Telegram OIDC user; `false` permits only `ASTRO_TELEGRAM_ADMIN_IDS`; `false` plus an empty list denies everyone. |
| `ASTRO_ASTRODOME_RESIDENT_LIMIT` | No | `10GiB` | Maximum projected resident footprint of preloaded native ICON-EU columns for one Astrodome job. Validation accepts `1GiB..20GiB`; keep it below the worker cgroup hard limit with headroom for Go, CDO/ecCodes, and charged file cache. |
| `ASTRO_ASTRODOME_JOB_TIMEOUT` | No | `0s` | Astrodome wall-clock deadline. `0s` disables it so a live calculation is not cancelled by an estimate; positive values must be `1m..1h`. Explicit user cancellation and process shutdown remain effective. |
| `ASTRO_DIRECTIONAL_INTERNAL_REQUEST_TIMEOUT` | No | `0s` | Bot-to-worker HTTP deadline. `0s` disables the transport cutoff; positive values must be `1s..8h`. It must be `0s` when the Astrodome job timeout is disabled, or exceed a finite Astrodome job timeout. |
| `ASTRO_DIRECTIONAL_WORKER_MEMORY_LIMIT` | No | `32g` on the measured production host | Compose hard cgroup limit for Go, CDO/ecCodes children, and charged file cache. It preserves headroom above the measured 18.53 GB peak; it is not a measured minimum. |
| `ASTRO_DIRECTIONAL_WORKER_GOMEMLIMIT` | No | `12GiB` on the measured production host | Go heap target inside the worker hard limit. It is not a total-process or cgroup limit. |
| `ASTRO_DIRECTIONAL_WORKER_CPU_LIMIT` | No | `10.00` on the 12-thread production host | Compose CPU quota for the isolated worker. The retained single directional slot leaves two logical CPUs for model sync and ordinary forecasts. Rebenchmark before applying this host-specific value elsewhere. |

Relevant YAML defaults are:

```yaml
directional:
  queue_size: 8
  concurrency: 1
  completed_ttl: 48h
  completed_entries: 128
  estimated_horizon: 3m
  estimated_astrodome: 30m
  internal_request_timeout: 0s
astrodome:
  enabled: true
  job_timeout: 0s
  cache_ttl: 48h
  cache_entries: 64
  resident_limit: 10GiB
  project_disk_cap: 400GiB
  min_free_space: 100GiB
  min_free_inodes: 10000
```

Two complete current-writer measurements at 129 nodes over 72 frames used ten
node workers, eight CDO workers, `GOMEMLIMIT=12GiB`, and the bounded two-frame
prepared-state window. Run `2026081606` completed all 9,288 node-hours in
`26m07.679s`, including a `4m41.816s` preload. The later immutable run
`2026081612`, with a warmer model/file cache, completed in `23m33.700s`,
including a `3m52.033s` preload; its science phase was `19m41.667s`. This is
not a same-run cold/warm pair. One node on the later run retained a pre-existing
fail-closed 1.842-cm quadrature-floor result; a focused replay on the
unmodified `main` failed in the same cell, interval, and component with ratio
1.58747 versus 1.58745, so it is not a
prepared-state, persistent-pool, or FSAL regression. Under a 32-GiB cgroup the
later run peaked at 18,532,704,256 bytes with no `memory.events`; the earlier
24-GiB trial reached its hard boundary and accumulated pressure events. The
32-GiB value is operational headroom, not a minimum requirement. The `30m`
value is therefore
queue/UI scheduling guidance only, not a completion guarantee or an execution
deadline. Production uses `0s` for both the Astrodome job timeout and the
bot-to-worker transport timeout, so a healthy calculation is not truncated by
that estimate. Explicit cancellation, coordinator shutdown and process
shutdown still propagate through the request context. Operators may configure
finite guards within the validation ranges above; the transport guard must then
exceed the job guard, and it cannot remain finite while the job guard is
disabled. The historical v28/path-v22 baseline covered all 9,288
node-hours in 33 min 14 s: 9,284 were available and four sub-GL2 physical
spans failed closed. The current measurements do not replace a same-run
cold/warm pair, simultaneous-sync, payload, ordinary-Forecast/Horizon latency,
or observational gates.
Completed results are
bounded by TTL and entry count. Admission also accounts for staging, leases,
temporary data, the complete project disk ceiling, free bytes, and free
inodes. Queue capacity limits waiting jobs; directional concurrency limits
running jobs and is not inferred from queue size. Dense storage maps to the
production-v2 geometry: eight `10..80°` rings with 16 azimuths each plus one
zenith, or 129 nodes/frame and 9,288 node-hours over 72 frames. The versioned
97-node sparse profile is selected only when the storage budget requires it;
the historical 353-node dense-v1 profile is archive-only. Access mode does not
override this `DiskBudget` choice: administrators do not receive a forced
sparse profile while public access is disabled. Do not lower the worker limits
or introduce finite deadlines until repeated cold/warm full-dome benchmarks pass.

## External site integration

The public application, OIDC, sessions, preferences, saved-visualization
catalogue, browser renderer, edge configuration, and their environment
variables are owned by the independent
[site-astrosferum](https://github.com/Haeniken/site-astrosferum) repository.
No `ASTRO_WEB_*` setting is read by this bot.

This deployment continues to own PostgreSQL and the scientific runtime. It
exposes only an authenticated internal account API through the existing
directional gateway and credential. Ordinary Forecast and Horizon website jobs
return owner-scoped status and a versioned interactive JSON dataset retained
for 96 hours; they reuse the configured queues, preserve the prepared
scientific values exactly, skip PNG rasterization on a website-only cache miss,
and do not send platform messages. Forecast JSON pins
`overall-astronomy-index-v4-native-mh-logp`, `effective-cloud-obstruction-v1`, and the SHA-256
of the complete Overall calibration used for that result. The site may use a dedicated
least-privilege PostgreSQL role, but stopping or removing it must not affect
Telegram, VK, model synchronization, ordinary forecasts, Horizon, or
Astrodome calculation. Site access policy must be consistent with
`ASTRO_ASTRODOME_ENABLED` and `ASTRO_TELEGRAM_ADMIN_IDS`; disagreement fails
closed at the bot API boundary.

## Copernicus DEM GLO-30 terrain skyline

| Variable | Recommended | Effect |
|---|---:|---|
| `ASTRO_COPERNICUS_DEM_GLO30_ENABLED` | `true` | Enables the static Copernicus DEM GLO-30 skyline for Horizon and Astrodome. When enabled, an unavailable/invalid tile fails directional preparation; no flat or HHL-derived substitute is invented. When disabled, the serialized source is explicitly `disabled` and ICON HHL remains the only coarse model-terrain check. |
| `ASTRO_COPERNICUS_DEM_CACHE_LIMIT` | `20GiB` | Upper bound for cached immutable 1°×1° GLO-30 GeoTIFF tiles. Derived per-coordinate profiles are separately capped at 4096 entries. Oldest-accessed entries are pruned; source data and profiles stay below `data/terrain/` and are excluded from Git. Allowed range: `1GiB..100GiB`. |

The profile is calculated once per source version and coordinates rounded to
`1e-5°`, not once per forecast hour. A cold miss is admitted to the shared
directional queue before the isolated worker downloads and validates the public
Cloud Optimized GeoTIFF tiles. Bot and worker then reuse the shared bounded
profile cache. Calculation-request schema 6 binds that profile; Horizon cache
schema 7 and Astrodome dataset schema 8 invalidate older payloads by
construction.

## Shared Overall, Horizon, and Astrodome calibration

The remaining optional variables form one calibration shared by the Overall
Astronomy Index, the optional Horizon directional index, and Astrodome. The repository
defaults below are the recommended production profile; change them as one
reviewed calibration set rather than tuning individual values casually. The
complete calibration is part of each Horizon/Astrodome cache key. Astrodome
calculation-request schema v6 carries the SHA-256 of its canonical complete
calibration, the worker rejects a bot/worker digest mismatch, and the dataset
retains the same digest as provenance. A change therefore produces a new
identity instead of reusing a result calculated with old values. Invalid or
inconsistent values stop the application during configuration validation.

| Variable | Recommended | Effect |
|---|---:|---|
| `ASTRO_OVERALL_SEEING_WEIGHT` | `1.0` | Exponent applied to optical-seeing quality; larger values penalize mediocre seeing more strongly. |
| `ASTRO_OVERALL_CLOUD_WEIGHT` | `2.0` | Exponent applied to modeled cloud transmission; larger values make cloud obstruction dominate more strongly. |
| `ASTRO_OVERALL_COHERENCE_TIME_WEIGHT` | `0.25` | Bounded weight of wind-sensitive coherence time `tau0`; `0` disables this guard and `1` applies it fully. |
| `ASTRO_OVERALL_OPTICAL_TURBULENCE_MAX_PENALTY` | `0.25` | Maximum combined loss from the seeing/`tau0` utility term. It preserves at least a `0.75` factor and does not clip the physical diagnostics. |
| `ASTRO_OVERALL_POSSIBLE_FOG_FACTOR` / `ASTRO_OVERALL_HIGH_FOG_FACTOR` | `0.75` / `0.10` | Multipliers for the possible/high `FogHeuristic` warning levels. Smaller factors impose a stronger penalty; these are not calibrated fog probabilities. |
| `ASTRO_OVERALL_PRECIPITATION_DETECT_MM` | `0.05` | Detection threshold for deterministic hourly precipitation. At or above it, ordinary Overall applies a binary operational veto and returns index 1; intensity is deliberately not converted into a smooth penalty. This field is retained in the shared calibration identity but the current directional Horizon calculation does not ingest precipitation. |
| `ASTRO_OVERALL_GOOD_SEEING_ARCSEC` / `ASTRO_OVERALL_BAD_SEEING_ARCSEC` | `0.5` / `2.0` | Best and poor reference limits for logarithmically mapping modeled seeing to quality. |
| `ASTRO_OVERALL_BEST_COHERENCE_TIME_MS` / `ASTRO_OVERALL_BAD_COHERENCE_TIME_MS` | `5.2` / `1.6` | Best and poor reference limits for mapping `tau0` to its guard factor. |
| `ASTRO_OVERALL_GROUND_CN2_SCALE` | `1.0` | Multiplier for ground-layer `Cn2`; values above `1` increase the modeled near-ground turbulence contribution. |
| `ASTRO_OVERALL_UNRESOLVED_CLOUD_OBSTRUCTION` | `0.45` | Maximum low-cloud diagnostic guard when `CLC` is not represented by resolved `QC/QI`; middle/high guards are smaller fixed fractions. |
| `ASTRO_OVERALL_SURFACE_WIND_MAX_PENALTY` | `0.20` | Maximum fractional surface-wind/gust penalty; `0.20` preserves at least an `0.80` factor from this guard alone. |
| `ASTRO_OVERALL_SURFACE_WIND_START_MS` / `ASTRO_OVERALL_SURFACE_WIND_FULL_MS` | `8.5` / `15.0` | Sustained-wind speeds where the mild surface penalty starts and reaches its configured maximum. |
| `ASTRO_OVERALL_SURFACE_GUST_START_MS` / `ASTRO_OVERALL_SURFACE_GUST_FULL_MS` | `12.0` / `22.0` | Gust speeds where the same penalty starts and reaches its maximum. |
| `ASTRO_CLOUD_LIQUID_RADIUS_MICROMETERS` / `ASTRO_CLOUD_ICE_RADIUS_MICROMETERS` | `10` / `25` | Assumed effective droplet/crystal radii used to convert liquid/ice water path into visible optical depth. Smaller radii imply stronger extinction for the same condensate mass. |

Keep `.env` outside Git. After changing a value, validate the effective
configuration and recreate the application container:

```sh
docker compose run --rm bot_astrosferum doctor --config /app/config/config.yaml
docker compose up -d --no-deps --force-recreate bot_astrosferum
```
