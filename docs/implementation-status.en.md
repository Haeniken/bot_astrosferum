# bot_astrosferum: implementation status

Date: 2026-08-14
Stage: Stage 3 live ICON → Telegram and VK; fast straight-ray Horizon v14 with a static informational Copernicus DEM GLO-30 native-cell skyline is current; Astrodome controlled rollout plus anonymous read-only fixture live, production-v2/v34/v24 current; v28/v22 full-run retained as the measured atmospheric-path baseline
Deployment target: operator-managed host

## Complete

- PostgreSQL 18.4 stores users, at most 10 points per user, and daily usage aggregates bounded to 90 days;
- the Telegram and VK keyboards support saving/selecting points; configured platform admins receive the same cross-platform account count and combined 30-day PNG;
- Light Pollution Atlas 2024 now has a separate World Atlas 2015 comparison; the required GeoTIFF is restored automatically at startup;
- runtime uses the official OSGeo GDAL 3.13.1 image and ecCodes 2.45.0.
- surface field names are normalized across ecCodes versions (`VMAX_10M`/`max_i10fg`);
- the admin chart uses stacked successful/failed bars and MSK (UTC+3) calendar days; point menus provide a Back button.

- Go `1.27.0`, `tzf v1.2.5`, and `gonum/plot v0.17.0`;
- Telegram accepts native locations and VK accepts geo attachments; both accept `59.9386, 30.3141` and `/forecast 59.9386 30.3141`;
- both thin platform adapters depend on the common `internal/app/bot` handler and never import each other; VK Group Long Poll is enabled at startup, messages and native geo are normalized to the common request, while texts, keyboards, PNG photos, and lossless document uploads are translated back to VK API calls;
- Telegram and VK run under independent retrying supervisors, so a platform API failure does not stop model synchronization or the other adapter;
- ordinary forecasts share one cross-platform calculation limit and report a queue position after both configured slots are occupied; Horizon and Astrodome share one bounded directional FIFO and a separately configured isolated-worker concurrency (`1` recommended until benchmarked);
- VK photo/document uploads validate the handshake and retry at most twice with `1 s`/`2 s` backoff and a fresh upload URL; this handles transient `pu.vk.ru` `405` or incomplete upload responses without unbounded retries;
- sequential outgoing messages in each VK request have a minimum `250 ms` interval without a global lock; parallel VK requests and Telegram delivery are unchanged;
- `/start` and `/help` explain requesting and interpreting all seven charts;
- the user summary reports the selected model, run ID, and model-run age against configurable `max_stale_age`; this threshold only controls the `⚠️ stale run` warning and never pins cached data. Point and render cache identities contain the run ID, so a newly published run is an automatic cache miss;
- Telegram language follows `User.language_code`: only `ru*` receives Russian, while every other or missing code receives English; help, statuses, errors, buttons, captions, the weather table, and all PNG titles/axes/legends are localized, and render-cache identity includes the language;
- VK currently defaults to Russian because Group Long Poll events do not include the Telegram-style language code;
- timezone lookup is offline and every PNG labels the coordinate timezone;
- DWD discovery selects the latest complete ICON-EU `00/06/12/18` cycle available through `+72 h`;
- coordinates outside the ICON-EU domain route to the latest complete ICON Global `00/06/12/18` cycle on its full native global grid; CDO uses the official static DWD grid geometry for nearest-native-cell point extraction, without cropping the source or creating a second world raster;
- ICON Global now synchronizes the same 79-hour cloud plus native-MH turbulence contract as ICON-EU. Its independent full-grid HHL proof selects consecutive full levels `87…120` and half levels `87…121`; DWD Global TKE ends at `+48 h`, so bundles are 260 messages/hour through that point and 225 thereafter, the cloud map remains complete, and hybrid Overall stops rather than extrapolating missing turbulence; direct `VIS` also remains unavailable in the DWD Global feed;
- atomic sync streams pressure-level `U/V/FI/T`, retains no `.bz2`, and validates every bundle with ecCodes and SHA-256;
- legacy pressure-level runs atomically download only missing `FI/T`; the scheduler independently publishes versioned hourly surface/cloud bundles, while ecCodes 2.45 `clwmr/QI` names normalize to stable internal `qc/qi`;
- ICON-EU and ICON Global keep the preceding complete `current` run available while every component of a newer run downloads; `current` changes through one atomic symlink operation only after complete validation;
- an exclusive lock prevents overlapping syncs; the scheduler probes once at startup and every 15 minutes thereafter, retaining two runs; user requests never download data and only read the last complete publication;
- point extraction reads 25 vertical profiles concurrently from the current manifest;
- an independent surface sync publishes 79 hourly `f000…f078` bundles containing `T_2M`, `TD_2M`, `RELHUM_2M`, `CLCT/CLCL/CLCM/CLCH`, `TOT_PREC`, `U_10M`, `V_10M`, `VMAX_10M`, `PMSL`, `VIS`, `TQV`, and exact total-column `TQC/TQI`; `PMSL` is retained for the weather product only and is never passed to SPECTRL2;
- both platforms show 72 hours; fog uses direct `VIS`, while the cloud chart shows effective obstruction by height from `CLC+QC+QI` and phase-resolved optical depth;
- dew is only an equipment-preparation advisory and does not lower seeing or the practical score;
- pure-Go astronomy calculates Sun/Moon state and hourly topocentric planning ephemerides plus rise/set events for Mercury, Venus, Mars, Jupiter, Saturn, Uranus, Neptune, and Pluto; Sun/Moon event regressions remain within two minutes of reference data, and an all-body JPL Horizons `AIRLESS` control remains within `0.15 degrees` while preserving the planning-accuracy qualification;
- both platforms deliver the same seven PNG files; the enlarged `4320×1600`
  Overall chart combines hybrid ICON TKE/MH + HMNSP99 seeing, `tau0`, effective
  `CLCT+TQC+TQI` cloud transmission, fog, mild surface wind, and a binary
  precipitation veto at the configurable `R_1h >=0.05 mm` detection threshold;
- one retained piecewise-linear `Cn2` profile now supplies seeing, `tau0`,
  `theta0`, effective turbulence height/wind, fixed-height
  `FracGL250/500/1000`, free-atmosphere seeing above 500 m, and structural
  vertical and `h^(5/3)`-moment coverage; dynamic-PBL compatibility fields
  remain explicitly distinct from MASS-like fixed-height fractions. Overall rejects either coverage below
  `90%` or a profile top below `15 km AGL`; `complete` requires `99%/99%` and
  at least `18 km AGL`, and every other quality state marks `!`;
- chart 2 shows the retained index below an exact five-color Shapley
  decomposition of `10-Overall`; stacks close at 10, precipitation is an
  explicit veto, and `!` means partial inputs rather than forecast probability;
- during astronomical night chart 2 may overlay a separate Reference
  Johnson-V grid-cell-mean geometric-zenith efficiency ring for the declared
  faint-source, background-limited, seeing-limited, non-AO mode. Contract
  `reference-v-band-zenith-efficiency-v2` and atmosphere
  `reference-v-band-spectrl2-ks91-v3` use ICON pressure/PWV/all-sky clouds and
  NASA GEOS-CF AOD550/ozone; KS91 supplies the Moon background and a
  photon-weighted PSF mixture supplies the noise-equivalent area. Clouds
  attenuate the source while clear-air natural/lunar background is held as a
  conservative floor, so the modeled cloud response is approximately `T²`;
  this is explicitly limited by missing cloud-scattered radiance. Opaque
  transmission omits `ExtinctionMag` and sets `OpaqueTransmission`; missing
  seeing/composition is partial, while a high `FogHeuristic` signal or precipitation makes the
  result operationally unavailable and omits the marker. Artificial light
  remains excluded, and the result is not multiplied into generic Overall;
- Reference V derives actual model-surface pressure from the lowest available
  same-hour native ICON full-level `P/T` and full/surface `HHL` using
  `p_s=p_l exp[g_0(z_l-z_s)/(R_d T_l)]`; provenance is
  `icon-lowest-model-level-p-hydrostatic-to-hhl-surface-v1`. Missing inputs
  or a transfer gap over `1 km` make the result partial, with no `PMSL` or
  standard-atmosphere fallback;
- the bounded GEOS-CF OPeNDAP client records its independent run/freshness,
  limits timeout/response/cache/concurrent misses, and fails open: missing or
  stale composition removes the Reference-V result without blocking ICON;
  its HTTP timeout is `60 s`; the service-root operation has a `65 s` deadline
  and shutdown cancellation, while an ordinary user response joins it for at
  most `5 s`. Identical slabs coalesce, distinct loads are gated by
  `ASTRO_FORECAST_CONCURRENCY`, and a full gate fails open without starting a
  new optional load;
- target-conditioned helpers implement Kasten-Young optical air mass,
  atmospheric FWHM scaling by `X^(3/5) lambda^(-1/5)`, and an explicitly
  Gaussian-only delivered-IQ quadrature; no target-dependent value is silently
  inserted into coordinate-only Overall;
- the text block gets a point light-pollution estimate from Light Pollution Atlas 2024: bilinear LPI/SQM at approximately `30″` plus an honestly labelled approximate Bortle value; light pollution is not part of the Overall Index;
- heat-map values are now 8 pt semibold, with slightly larger axes and bar labels;
- the old wind-only index remains a separate diagnostic chart; direction delta becomes `0°` below `2 m/s`, while `NaN` means missing data only;
- the container runs as `1000:1000` with a read-only root filesystem and bot/worker bind mounts only below `/opt/docker/bot-astrosferum`;
- `go test ./...`, `go vet ./...`, and `doctor` pass.

Freshness and bilingual rendering were deployed on 2026-07-21: the production binary generated seven non-empty PNG files for each of `ru` and `en`, temporary verification directories were removed, the main container remained `running` with `restart_count=0`, and PostgreSQL remained `healthy`.

The VK adapter was deployed on 2026-07-22. Production starts Telegram and VK Group Long Poll under independent supervisors; VK settings report API `5.199`, Long Poll enabled, and `message_new=1`. The selected server was under `*.vk.ru`, the application container remained `running` with `restart_count=0`, PostgreSQL remained healthy, and the active ICON-EU/Global run symlinks were unchanged. The old platform-named 16 MiB render cache was removed after the shared bounded cache was initialized.

A live GEOS-CF contract probe returned DAS metadata in about `3.7 s`; the
first 72-hour subset exceeded `30 s`, while a repeat completed in approximately
`30 s`. These observed cold/warm timings justify the `60 s` HTTP timeout,
`65 s` service-root operation deadline, and `5 s` user-facing join instead of
extending the ordinary forecast critical path.

## Horizon analysis — implemented

The optional Horizon analysis is an existing application function controlled
by `ASTRO_HORIZON_ANALYSIS_ENABLED`. It is deliberately available only for an ordinary
ICON-EU forecast and covers the immutable native `f001..f072` interval. ICON
Global exposes neither a Horizon button nor a runnable Horizon job. The
implementation consists of:

- an environment/configuration switch with validation, plus a disabled-state
  path that does not require Horizon operational limits;
- one small versioned application action router and normalized Telegram/VK
  callback events; platform adapters contain no Horizon formula; the
  composition root now constructs one shared Horizon service and attaches its
  button/action handlers to both enabled platforms;
- one shared bounded heavy-job queue with the configured directional active-slot
  limit, per-user admission,
  identical-key fan-out, timeout/cancellation, signed compact actions, and a
  separate bounded two-worker delivery path with bounded cache-hit admission;
- an atomic bounded disk cache with startup, periodic, and post-publication
  cleanup; short-lived hard-link leases keep an admitted PNG stable across
  eviction and abandoned staging/lease artifacts are removed at startup;
- eight azimuths using a spherical straight line of sight launched at geometric
  elevation `10°`, partitioned into 500-m ground-track panels up to 22.3 km
  MSL; Astrodome alone retains the Ciddor/Dormand--Prince refracted path, while
  prepared seeing, `tau0`, cloud obstruction, index, and quality are never
  interpolated in either product;
- ICON-EU-only footprint checks and immutable `f001..f072` acquisition:
  exact hourly surface/cloud/TKE/MH/visibility, same-run linear interpolation
  of raw three-hour pressure `U/V/T/Z`, and batched CDO extraction of
  deduplicated model cells. One request-scoped `gennn` nearest-neighbour
  weights file is reused read-only through `remap` for every field/step; the
  shared `horizon_analysis.cdo_workers` limit is eight subprocesses for both
  Horizon and Astrodome preload;
- a 72-frame calculation contract that recomputes HMNSP/TKE, `Cn2`, seeing,
  `tau0`, and the index for every hour rather than interpolating nonlinear
  outputs, using the same complete calibration as Overall; that calibration is
  also part of Horizon cache identity;
- the shared target-agnostic utility mapping preserves physical seeing/`tau0`
  but bounds their combined score loss to 25%;
- current-run guards before cache reuse/heavy work, at the start and end of
  acquisition, after calculation/rendering, and immediately before every send,
  including cache hits; delivery-time captions use the configured ICON-EU
  `max_stale_age`;
- one localized `3200x1400` time-by-eight-direction heatmap for all 72 terms,
  with day/twilight/night shading, unavailable cells, run period, freshness,
  and compact limiter/quality disclosure;
- the per-cell data-quality strip uses the deterministic lead-time quality
  heuristic: good at
  `C_lead >= 0.85`, usable at `0.75 <= C_lead < 0.85`, and limited at
  `C_lead < 0.75`; unavailable paths remain missing, while the composite
  data-quality heuristic below `0.60` can only downgrade a result;
- unit tests in the affected packages for callback normalization, queue/cache
  behavior, series identity, geometry, local-ENU wind projection,
  dateline/high-latitude cases, provider boundaries, sparse/missing profiles,
  and physical closures;
- a `render-horizon` command for a real current-run ICON-EU verification render;
- a geometric homogeneous-path reference derived from the implemented
  spherical ray itself, rather than a molecular-air-mass approximation; the
  physical slant seeing and `tau0` remain unchanged, while reference anchors
  use the exact path ratio raised to the Fried `3/5` power.

The preceding v9 atmospheric-path production gate passed on 2026-08-13 against immutable ICON-EU
run `2026081300` (manifest SHA-256
`8f329b58721a4244d13725e6c02d6610b87e7e1af05d85a576c6822dd5f49cc6`).
It published exactly 72 frames `f001..f072` from 01:00 UTC on 13 August through
00:00 UTC on 16 August. The cold control calculation used 213 deduplicated
footprint points, completed all 169 extraction batches with zero partial
values, spent `2 min 3.285 s` in directional series extraction and
`3 min 20.792 s` end-to-end, and produced a non-empty `3200×1400` PNG with
SHA-256 `77b36fd53a4cf258d2fe59f9f71f164cf74555930256f7622522517763e97a06`.
The gate used ecCodes `2.45.0` and CDO `2.5.4`; therefore it exercises the
batched real `TOT_PREC packingError` path rather than only a synthetic fixture.
An ordinary seven-chart non-regression render then completed from the same run
in `65 s` and produced all seven non-empty PNG files. Doctor, startup logs,
PostgreSQL, directional-worker and website health/readiness passed; stopping
the site left the bot, worker and PostgreSQL running independently, after which
the site returned healthy. The full Go test/vet/build, pinned golangci-lint
`2.12.2`, govulncheck `1.6.0`, and 20 browser-contract Node tests passed from
isolated production source trees.

The older validation below measured the retired v7 straight path and is
retained only as a historical latency baseline. It used ICON-EU run `2026072218` and ICON
Global run `2026072212`. At the saved Plavsk point, the first ordinary
seven-chart calculation took `83.097 s`, the warm calculation took `9.193 s`,
and the former 73-frame Horizon calculation took `123.541 s`. An ordinary
forecast completed in `10.074 s` while Horizon was actively extracting data,
below the `14.193 s` non-regression threshold. The Global smoke produced seven
charts in `174.586 s`; its Horizon command failed closed and created no PNG.
Post-deployment doctor, PostgreSQL readiness, current-run identity, startup
logs, restart/OOM state, bounded-cache cleanup, and secret/coordinate log scans
all passed for that historical gate.

Every deployment validates repository tests, race/vet/lint/build, one real
current-run ICON-EU calculation, the explicit ICON Global no-button/no-job
case, health and startup logs, ordinary and Horizon smoke tests, ordinary
forecast latency during concurrent Horizon work, and the administrator report.
Failure of any mandatory gate requires rollback or a failure report instead of
a success notification.

## Astrodome — controlled rollout and public reference live, full-run measured

The ICON-EU-only directional atmospheric web product is deployed with controlled calculation access as
one asynchronous dataset of up to 72 consecutive native hourly frames from the explicit 10° calculation boundary
to a single azimuth-independent zenith. Completed code includes:

- versioned `production-v2` (eight `10..80°` rings × 16 azimuths plus one
  zenith = 129 nodes/frame, 9,288 node-hours over 72 frames) and
  `sparse-storage-v1` (97 nodes/frame, 6,984 node-hours) grids, canonical
  ordering, geometry descriptors/digests, spherical Voronoi cells, and strict
  browser contracts. The former 353-node `dense-v1` profile is historical and
  archive-only;
- ICON-sphere straight geometry and full Ciddor moist-air refraction with a
  coupled adaptive Dormand–Prince 5(4) ECEF ray and event closure. Refraction
  `dormand-prince-5-4-fsal-event-v4` retains the accepted seventh-stage field
  sample, computes the derivative once from the exact normalized endpoint,
  and reuses that normalized-start derivative for the next bit-identical state,
  while still requiring two converged forward production passes; the reverse
  pass is reserved for reference/strict regression and release verification;
- `astrodome-science-kernel-v34-native-mh-glo30-informational-skyline` /
  `astrodome-science-path-v24-native-mh`: 0.2-mm physical/horizontal root localisation,
  0.4-mm distinct-root proximity detection with fail-closed handling, a 0.5-mm
  side guard, a 0.2-mm proof scale, the unchanged 1-mm position/coordinate
  evaluation ceiling, a versioned limited midpoint result at or below
  `0.002366025404… m` only for a three-probe single-partition short panel,
  independent GL2/GL1, GL3/GL2, and GL5/GL3 below their respective
  `0.004436491673…`, `0.010658690662…`, and `0.117032584345… m` upper
  boundaries, and no extrapolatory Q5/Q3;
  dense point evaluation uses componentwise absolute-monomial scales with an
  audited `gamma128` and outward L2 envelope, while the broader Bernstein
  derivative/acceleration bounds retain `gamma1024`;
  endpoint-aware ordinary strict floors are `0.001000000000001… m` for two
  physical endpoints without a certified-short proof,
  `0.0005000000000005… m` for one physical endpoint,
  and `0.01 m` for two numerical endpoints; the evidence-aware short-panel
  path uses its certified open-safe span. Midpoint-panel lengths plus the union
  of accepted endpoint slivers are capped at exactly 1 m; an accepted result is
  `limited`, has zero quadrature-convergence quality, and carries
  `short_path_approximation`. Bit-identical simultaneous event
  coordinates alone form one geometric breakpoint, with the sorted event-ID
  union retained; bit-distinct coordinates are never merged.
  All retained rule pairs have fixed positive weights and sample only inside
  the certified panel. No value outside the endpoint guards is used to infer a
  missing interval. Production uses the accumulated embedded selected-rule
  estimator; the independent
  half-tolerance repeat with its one-time fine-pass split is reserved for
  regression, calibration, and release verification. Neither allowance is a
  rigorous enclosure;
- one-sided endpoint-sliver derivative certificates are limited to HHL,
  full-level, cloud-tier, and raw bilinear PBL predicates. Their complete
  outward-rounded one-sided residual enclosure accepts a sliver only when zero
  is excluded; this covers bounded motion both away from and toward zero
  without evaluating the ambiguously owned endpoint. If neither proof excludes
  a root, path v24 records only that omitted sliver in the one-metre limited
  budget; it must still certify every interior WMO breakpoint before quadrature,
  and any kernel partition mismatch fails closed;
- exact per-sample evaluation errors: 1 mm is only the rejection ceiling;
  ECEF predicates use the actual dense-position bound, bilinear fields use
  field-specific coordinate uncertainty, physical height residuals sum their
  actual ray/operand terms, and 200 hPa propagates native H/P coordinate and
  arithmetic intervals;
- the path-v24 monotone-root contractor preserves the ancestor existence and
  uniqueness proof, then intersects a separate root enclosure with
  outward-rounded safeguarded interval-Newton images at endpoints and midpoint.
  An insufficient midpoint contraction is followed by paired off-centre probes
  on both sides of the central window; a residual interval containing zero is
  never given its nominal sign, and an irreducible information floor remains
  fail-closed. Acceptance still requires both root radii to be at most 0.2 mm;
  the 0.4-mm distinct-root threshold and 0.5-mm side guard are unchanged;
- PBL is the single certified bilinear surface `HSURF+MH` for positive native
  `MH`; no 500/2000-m clamp decisions remain and missing/non-positive `MH`
  fails closed;
- four-dimensional reconstruction of native ICON-EU primitives. Spatial and
  temporal interpolation happens before every nonlinear formula; no finished
  seeing, `tau0`, cloud transmission, Overall, or quality value is
  interpolated. Native full-level pressure must strictly increase from model
  top toward the surface, native `PS` must exceed the lowest full-level
  pressure, and reconstructed pressure must satisfy `dP/dz < 0`; any violation
  fails closed before refraction or science integration;
- certified CLC upper envelopes over the four raw stencil columns, native
  temporal-bracket endpoints, and exact active adjacent full-level pair (or
  one top/bottom extension level). Convex reconstruction and an outward
  binary64 allowance bound each atomic interval; vertical-support identity is
  part of the partition signature/cache key, and missing provider certificates
  or sampled changes fail closed. The envelope does not maximize over an
  entire tier or column;
- separate nominal and conservative cloud transmissions. Each exact cell/tier
  block accumulates its own liquid/ice optical depth and embedded error; the
  conservative value uses the CLC envelope plus `tau_block + error_block` and
  is the only cloud value used by directional Overall;
- joint line-of-sight integration of `Cn2`, transverse-wind-weighted `Cn2`,
  slant water vapour, and phase-resolved cloud extinction, followed by the
  common bounded Overall mapping and precipitation veto;
- one configured Overall/cloud calibration shared by ordinary Overall,
  Horizon, and Astrodome. Calculation-request schema v6 and every current
  dataset retain the SHA-256 of the complete validated Astrodome calibration;
  a configuration change invalidates the cache by construction and a
  bot/worker mismatch fails closed;
- complete dome manifest/acquisition/volume contracts, run identity and
  leases, resumable augmentation of a matching immutable base run, storage
  admission, and atomic compressed result publication;
- request-scoped native-column preload that verifies the GRIB source grid,
  creates CDO `gennn` targets only for exact native indices, persists the
  source-plan digest, and inspects the generated SCRIP file to require exactly
  one link per target, the expected source and destination addresses, and a
  weight whose binary64 value is exactly one. Every remapped GRIB message is
  required to have the same regular-grid geometry and scanning order as the
  proven HHL source. The plan is reused through `remap` for all fields/steps and runs under the
  shared eight-worker CDO limit also used by Horizon. Ordinary ICON-EU
  `grib_get` outputs remain protected by the existing provider-versioned point
  caches;
- one bounded FIFO shared by Horizon and Astrodome with
  `ASTRO_DIRECTIONAL_CONCURRENCY` active heavy jobs (`1` recommended until
  benchmarked), plus an isolated directional worker that reads models
  read-only; process-shared, context-aware slot leases also cap accidental
  extra worker replicas and the operator-only `render-horizon` path;
- in the independent private `site-astrosferum` repository, a thin web process with Telegram OpenID Connect, server-side sessions,
  CSRF/Origin checks, least-privilege saved-point reads, job ownership,
  asynchronous job/status/dataset APIs, and matching bot-side access checks;
- localized `/en` and `/ru` public landing pages, a private account dashboard,
  and separate ordinary-forecast, Horizon, and Astrodome tools; Telegram sign-in
  is owned by the landing page and the whole site remains `noindex` plus
  `robots.txt: Disallow /`;
- owner-scoped ordinary-forecast and Horizon web jobs that reuse the existing
  forecast queue, render/Horizon caches, calibration, shared directional FIFO,
  statistics, and prepared scientific values without duplicating calculation;
  the bot publishes `forecast-interactive-v6-observing-ephemerides` and `horizon-interactive-v7-glo30-informational-skyline` JSON
  into a bounded 96-hour owner-scoped result store, while the site polls status
  and draws interactive charts without sending result files to Telegram or VK;
  an explicitly enabled website preference may send one best-effort Telegram
  terminal-status message without an attachment;
- the ordinary interactive contract preserves separate native timelines:
  one exact hourly weather/cloud axis, its possibly narrower exact Overall
  subset, and an independent three-hour pressure-level diagnostics axis, with
  no resampling between them; Overall never extends outside pressure-profile
  support;
  Go serializes the additive Overall penalty points and the browser only draws
  them. The payload pins `overall-astronomy-index-v4-native-mh-logp`,
  `effective-cloud-obstruction-v1`, and `overall_calibration_sha256`; a
  website-only cache miss skips PNG rasterization. It additionally carries ten
  exact, canonical hourly topocentric tracks under
  `celestial-observing-ephemerides-jpl-meeus-wgs84-h0-v4`; shortest-arc interpolation is confined
  to dashed presentation geometry and never changes a selected-hour or
  meteorological value. The same contract carries a pinned exhaustive-hourly
  model-distance scale, an integer forecast-mean closeness label, the exact-hour
  closeness displayed to two decimals, signed range rate, angular diameter,
  phase, illuminated fraction, apparent V magnitude with explicit domain status,
  solar elongation, daily culmination, Mars season, and Saturn ring-opening
  angle; Mars/Pluto magnitudes are explicitly labeled planning approximations
  rather than published-domain values, and none of these diagnostics changes
  Overall. Astrodome also carries a strict versioned Polaris overlay and fixed
  ICRS/J2000 SynScan coordinate. The separate Johnson-V diagnostic exposes NASA GEOS-CF provenance when
  composition is available and never becomes an implicit Overall penalty;
- the Horizon interactive payload pins its exact cache artifact, ICON HHL
  observer-surface elevation, ICON-EU provider/run/grid, Horizon science
  version, canonical Overall-calibration digest, and the native `f001..f072`
  hourly axis; continuous server-derived solar-phase intervals cover
  `f001−30 min .. f072+30 min`, and the worker must match the coordinator's
  pinned science cache key before publication. Canonical terrain-primary unavailable cells remain visible and
  fail closed rather than invalidating the full result;
- shared saved points can be selected, created, or deleted on the website up to
  the existing limit of ten; a forecast may also use validated transient
  coordinates entered directly, received from an explicit browser-geolocation
  action, or selected on the bundled offline map;
- an authenticated `ru`/`en` web-language preference stored in PostgreSQL and
  synchronized across active sessions while retaining explicit localized URLs;
- no VK ID login or linking yet: the configured VK community token is not a VK
  ID application credential, so unsafe identity emulation is deliberately absent;
- gzip visualization archives with an ordinary 96-hour TTL, plus one explicit
  non-expiring `admin_fixture` visible read-only to signed-out visitors and to
  every configured Telegram administrator, but not to authenticated non-admins. The
  fixture and saved-result decoder accept only v34/v24 with a supported pinned
  profile; production web output is `production-v2`. An older payload is
  rejected until a successful current same-coordinate result replaces the
  shared fixture when exact integer cross multiplication proves a smaller or
  equal `unavailable / total` fraction; only a strictly worse result does not
  replace it;
- a self-contained WebGL2 inside-dome view, Canvas 2D and accessible-table
  fallbacks, fixed quantitative legends, keyboard/touch controls, and strict
  fail-closed dataset validation; the star layer stays behind the mesh, the
  non-data context shows an ordinary `0°` label without a separate horizon
  line, twilight sky below the `10°` calculation boundary, and decorative
  terrain; `0°` and `10°` use the ordinary elevation-grid treatment, only one
  selected-cell inspector is visible in both normal and fullscreen layouts,
  the same hourly timeline remains interactive in fullscreen without
  duplicating its selected frame and auto-reveals from the lower pointer zone
  or keyboard focus, its eight requested field explanations are available in `ru`/`en`, and
  view/fullscreen controls clear the compass;
- a bot-owned isolated worker and internal account API here; the separate site
  Compose and pinned-TLS nginx templates live in `site-astrosferum` and deploy
  to `/opt/docker/site-astrosferum`.

The rollout switch is implemented as calculation access control, not as a worker kill
switch: `ASTRO_ASTRODOME_ENABLED=true` permits every authenticated Telegram
OIDC user; `false` restricts access to `ASTRO_TELEGRAM_ADMIN_IDS`; `false`
with an empty list denies everyone. Web and bot both enforce the rule. Access
mode never forces sparse geometry. Anonymous users can only read the permanent
fixture and cannot access points or job operations. Both calculation modes use
the profile selected by `DiskBudget`; dense storage maps to `production-v2`, while
`sparse-storage-v1` remains the declared storage fallback.

On immutable ICON-EU run `2026080812` (manifest SHA-256
`85d94a87e24a5eba4775baf4e46a4151c8c30021f51af6c54299cdbbabc5482e`), the
  historical production-v2/v28/v22 baseline covered all 72 native hours
`f002..f073` and all 129 nodes, or 9,288 node-hours. It completed in 33 min
14 s after a 357.118-s preload of 2,863 source columns; peak container memory
was 15,127,642,112 bytes. Exactly 9,284 nodes were available. Four nodes
(f005/56, f007/9, f025/40, and f026/40) failed closed with
`integration_nonconvergence`: their physical spans were
`0.45534076..2.12574664 mm`, below the shortest applicable positive-weight
GL2/GL1 rule. No extrapolatory Q5/Q3 result was published, and there were no
data, CDO/remap, refraction, cloud, or Overall failures. The strict diagnostic
therefore exited non-zero by design, while the product contract represents
these isolated cells as unavailable. The report SHA-256 is
`00977e620d60029e3e3f23f4aeb31352d614c9e75af6fa9cb8050f1830afdb31` and the
executed test-binary SHA-256 is
`7831ec7a451930890645e6baba42cb5ea39322e4075ee6c935909c4002fef518`.

This v28/v22 measurement and the older v25/v26 measurements remain diagnostic
provenance; they do not describe the current v34/v24 writer.

The exact phase-1 hot-path writer was then measured twice on the production
host with ten node workers, eight CDO workers, a persistent pool, a two-frame
prepared-state window, `GOMEMLIMIT=12GiB`, and the complete 72×129 grid. On
immutable run `2026081606` (manifest
`e2eeae9d3617089e8008d5f5abaf24b06707569c2cc0c5490a96e9491c0ecdb0`), all
9,288 node-hours were calculated in `26m07.679s`, including a `4m41.816s`
preload: 8,514 were valid and 774 were calculated precipitation vetoes. Report
SHA-256 was `2eadfeb1aea1a56da0f7536c631850630b9729997b535b8088386b50f49f9c45`;
test-binary SHA-256 was
`2702e5f3dbea572c6efac8a8813052655d64a1fbc10489ac1bf8619b721bead4`.

On later immutable run `2026081612` (manifest
`8352ab80818e5126b108b0676a68202bce17d881a65a3f6338614b2442030f71`), the
same shape completed in `23m33.700s`, including a `3m52.033s` preload and a
`19m41.667s` science phase: 8,642 nodes were valid, 645 were calculated
precipitation vetoes, and one was fail-closed `integration_nonconvergence`.
That node (`f067/node 99`) contained an 18.421382-mm numerical child that could
not be split above the unchanged 10-mm endpoint floors. A focused replay on
unmodified `main` failed in the same cell, interval, and component with ratio
1.58747 versus 1.58745, proving
that the unavailable state is not introduced by the prepared cache, pool, or
FSAL path. Report SHA-256 was
`ae5ad04ac5672addb16d04d92e48b99d03541acfc9f957445585138eca8da845`;
test-binary SHA-256 was
`061b854968e5ed8ca4f172880c0046f3d9df42da127fcc041db85e4cfd8fb506`.
The second run peaked at 18,532,704,256 cgroup bytes with no pressure or OOM
events under a 32-GiB hard limit. A prior 24-GiB trial reached the hard boundary
and accumulated `memory.events.max`, so 32 GiB is retained as headroom rather
than claimed as a minimum. The two measurements used different immutable runs
and therefore are not a controlled same-run cold/warm pair.

The independent angular-discretization diagnostic on the same run evaluated
five native hours (`f002`, `f020`, `f038`, `f056`, `f073`) on the union of
production-v2, dense-v1, and a 513-node uniform-32 reference: 789 distinct
directions per hour, with no calculation or availability mismatch. Against
the reference's piecewise-constant nearest-support comparison, production-v2
had a maximum Overall delta of `4.495704`, a worst hourly area-weighted P95 of
`0.821550`, and at most `8.565771%` of the cap above a `0.5`-point delta. In the
reference-cell-centre `<20°` subset (cells covering `10..17.5°`) its worst
hourly P95 reached `3.457452`. Dense-v1 reduced
the corresponding maxima to `1.744252`, `0.794451`, `7.393201%`, and
`0.455779`. The report SHA-256 is
`3811f777a2c7ca471838d276bbb3acf974ac182587ef9c58fb257d1db8b31466`.
This is a measured sensitivity result, not a convergence certificate: no
scientifically sourced angular-grid acceptance threshold was fixed before the
run, and the production-v2 low-elevation discretization is visibly material.

The following work is still pending and must not be reported as completed:

- define and validate angular-discretization acceptance criteria on multiple
  sites/runs; the five-hour result above does not establish that
  production-v2 is scientifically sufficient;
- a controlled same-run cold/warm 129-node comparison plus CPU, PSI,
  disk-peak, and gzip-payload measurements, including simultaneous model sync;
- ordinary-forecast and live Horizon latency non-regression while the
  directional worker is busy, plus worker OOM/timeout/rollback checks;
- end-to-end browser, Telegram OIDC, edge/origin restriction, security-log,
  and cache-invalidation smoke tests;
- observational validation against turbulence, all-sky cloud, GNSS/radiosonde
  water-vapour, visibility, and precipitation references.

## Production Overall change

The audit of historical control case (an anonymized historical control case) found that the
deployed legacy HMNSP99 calculation does not resolve the PBL and yields a narrow series
around `0.70…0.77″`; the old smoothstep best boundary of `0.7″` and condensate
without a mismatched-`CLC` guard produce too many `10` values.

Production uses the following replacement:

- `surface-hourly-v17` publishes 17 single-level fields at each of 79 hours,
  including ICON `MH`/ecCodes `mld` in metres;
- `cloud-hourly-v6-native-mh` publishes exactly 245 messages per hour:
  `CLC/P/T/QC/QI` at all 27 retained cloud levels, a deduplicated consecutive
  `P/T/U/V` turbulence chain on `44…74`, `TKE` on half levels `44…75`, plus
  separate `HHL` geometry;
- Masciadri `Cn²` is integrated trapezoidally from the surface to the positive
  native hourly `h_PBL=ICON_MH m AGL`; HMNSP99 applies only above that same
  boundary, and total `J` gives model-derived seeing at 500 nm;
- `tau0` comes from `integral(Cn²·|V|^(5/3)dz)`; there is no second direction
  penalty because vector shear already includes wind rotation;
- every `J_V` trapezoid averages endpoint values of `Cn²·|V|^(5/3)` rather
  than raising interval-mean speed to `5/3`, avoiding Jensen low bias across
  wind gradients;
- physical seeing and `tau0` map into one bounded turbulence utility
  `f=0.75+0.25*q_turbulence`; they cannot veto a clear general-purpose hour,
  while cloud transmission and fog retain their stronger obstruction role;
- surface wind is a mild factor capped at 20%; dew is excluded, while possible
  and possible/high `FogHeuristic` signals use factors `0.75/0.10`;
- the heatmap derives each native layer's air mass as
  `P_Pa/(287.05·T)·abs(HHL[k]−HHL[k+1])`, so sparse levels cannot inflate
  condensate path;
- unresolved-CLC guard is tier-aware: low `0.45·C`, middle
  `0.55·0.45·C=0.2475·C`, and high `0.18·0.45·C=0.081·C`; these are
  conservative engineering uncertainty factors, not physical opacity, and
  stronger `QC/QI` optical-depth obstruction always wins;
- the heatmap applies the common optical-depth kernel/guard policy at each
  native level, whereas Overall uses total-column `TQC/TQI` and
  random overlap of the aggregated `CLCL/CLCM/CLCH` tiers; their numeric values are not
  claimed to be identical;
- ICON-EU point-cache schema is `point-v8-native-mh-support` and ICON Global
  uses `point-v4-native-mh-support`; bundles with the former exported Gob
  field names, as well as entries without `MH`, `T`, or native layer
  thickness, cannot be reused as current data;
- the expanded chain changes the potential-temperature-gradient stencil at
  the former upper endpoint as well as turning some deep-MH results from
  unavailable into available. Therefore seeing v9, Overall v4, Horizon v14,
  and Horizon cache v8 reject prior same-run results. Astrodome science/path
  v34/v24 remain unchanged because that product already reconstructs the
  complete native volume independently; its base-bundle layout digest is
  nevertheless changed so a same-run volume is rebuilt against cloud v6;
- version markers match the new contract:
  `seeing-hybrid-tke-native-mh-hmnsp99-logp-v9`,
  `overall-astronomy-index-v4-native-mh-logp`,
  `horizon-spherical-straight-los-native-mh-logp-glo30-informational-v14`,
  `horizon-cache-v8-native-mh-support`,
  `conditions-v8-precip-veto-penalty-decomposition`,
  `render-v18-celestial-distance`, and
  `shared-render-v24-explicit-heuristics`; Reference V is
  independently keyed by `reference-v-band-zenith-efficiency-v2`,
  `reference-v-band-spectrl2-ks91-v3`, and
  `reference-v-band-benchmark-v1`.

A server-side fixed-2-km calculation without fitting (`ground Cn² scale=1`)
produced control-case seeing of `2.221″` at `f042` and `3.644″` at `f048`; the
ground layer held 88.36% and 94.43% of `J`, respectively. After adding
`MH=396 m` at both leads, the native cutoff gave `1.9075″/2.5158″` and the
former 500 m minimum gave `1.9145″/2.5478″`. This is a historical server-only
root-cause regression, not observational calibration. Production sync of
`surface-hourly-v17`/`cloud-hourly-v4` for run `2026072106` is complete, the
new image is deployed, and the live Telegram CLI path passed for the historical control case. See the
[scientific method and calculation note](scientific-method.en.md).

## First complete run

Published run `2026071912`:

- levels: `1000…50 hPa`, 20 levels;
- times: `0…72 h` every three hours, 25 times;
- messages: 1,000;
- published size: approximately 1.0 GiB;
- sync duration: 46 seconds with four workers;
- runtime: `/opt/docker/bot-astrosferum/data/models/icon-eu/runs/2026071912`;
- current: `/opt/docker/bot-astrosferum/data/models/icon-eu/current`.

Last verified live state before the new deployment: extraction and all seven PNG files from the old version passed again on production run `2026072100` for an anonymized historical control case on 2026-07-21. The atomic publication contains 79×16 surface fields (`surface-hourly-v16`, 1.2 GiB) and 79×19×4 model-layer fields plus HHL (`cloud-hourly-v2`, 2.4 GiB); superseded versioned directories were removed after switching. The old cloud-physics check gave `99.90%` transmission and Overall `9.98` at 2026-07-22 09:00 UTC with `CLCT=72.8%`, `TQI=0.000015 kg/m²`, and `τ=0.00105`; at 02:00, `CLCT=100%` and `τ=2.78` gave `6.20%` transmission and Overall `1.03`. The first case is now a regression input for the unresolved-CLC floor, not the desired result. Old-version output is `3200×1080` weather, `3200×960` overall, `3200×1100` cloud obstruction, and four `1280×960` charts.

A bounded performance layer is now present: surface/wind/cloud extraction runs in parallel into one point bundle, concurrent identical misses are coalesced, `gob.gz` cache retention is two runs × 512 cells, and the in-memory LRU is bounded by both 512 cells and 20 GiB. Seven-chart render bundles live for 48 hours, capped at 256. Production uses provider-versioned point caches; obsolete schemas are ignored automatically. Each platform uses six peer-affine workers with a 15-minute request timeout so the first full-native Global point extraction can complete; ecCodes/CDO share a global eight-process limit. Each DWD object download still has a bounded two-minute HTTP timeout with three retries, while the scheduler itself is not capped by a user-request timeout. Cache staging older than one hour and Docker logs beyond `3 × 10 MiB` are removed automatically.

The first production request after the cache-schema change on run `2026072100` spent `38.5 s` extracting, `1.82 s` rendering seven PNG files, and `1.75 s` sending, for `42.24 s` total. A repeated CLI run loaded the point bundle from disk in `2 ms`; the seven-PNG set occupies about `2.7 MiB`. Render cache contains exactly seven files and remains bounded to 48 hours/256 sets.

The new production run `2026072106` was checked at historical control case. Initial
extraction of the expanded `187×79` cloud bundle took `1m14s`; a disk-cache hit
then takes `2 ms`, and a repeated full seven-PNG CLI render takes about `4.1 s`.
The 69 available future hours have Overall `1.00…4.58` with no exact `10`,
seeing `0.72…2.84″`, `tau0=1.72…3.50 ms`, under the former applied MH clamp
`500…2000 m`. Cloud transmission is below 50% in 47 hours and seeing is at
least `2″` in 21 hours, explaining the low distribution for this run.

The light-pollution provider is pinned to the validated Light Pollution Atlas 2024, downloads only required `5°×5°` tiles to the production host, atomically caches them below `data/light-pollution/lorenz-atlas`, and retains at most two years. It does not discover new versions automatically; an operator changes the configured year after validation. There is no separate World Atlas 2026 release as of 2026-07-21.

## Next vertical slice

1. Complete the controlled same-run cold/warm and simultaneous-sync resource
   gates; keep the exact phase-1 hot path as the fallback while the separate
   single-pass refraction certificate is developed and validated.
2. Maintain Telegram OIDC, the pinned Alice→Dragon origin, browser smoke,
   canary, and rollback procedures in the private site repository.
3. Continue accumulating observational verification data for every forecast
   method and monitor both platform adapters in production.

`seeing-hybrid-tke-native-mh-hmnsp99-logp-v9` is not observationally calibrated until compared
with DIMM/MASS/SCIDAR data or observing logs in the priority regions.
