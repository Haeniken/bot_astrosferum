# bot_astrosferum: implementation status

Date: 2026-07-22
Stage: Stage 3 live ICON → Telegram and VK; optional ICON-EU Horizon analysis implemented
Deployment target: operator-managed host

## Complete

- PostgreSQL 18.4 stores users, at most 10 points per user, and daily usage aggregates bounded to 90 days;
- the Telegram and VK keyboards support saving/selecting points; configured platform admins receive the same cross-platform account count and combined 30-day PNG;
- Light Pollution Atlas 2024 now has a separate World Atlas 2015 comparison; the required GeoTIFF is restored automatically at startup;
- runtime uses the official OSGeo GDAL 3.13.1 image and ecCodes 2.45.0.
- surface field names are normalized across ecCodes versions (`VMAX_10M`/`max_i10fg`);
- the admin chart uses stacked successful/failed bars and MSK (UTC+3) calendar days; point menus provide a Back button.

- Go `1.26.5`, `tzf v1.2.3`, and `gonum/plot v0.17.0`;
- Telegram accepts native locations and VK accepts geo attachments; both accept `59.9386, 30.3141` and `/forecast 59.9386 30.3141`;
- both thin platform adapters depend on the common `internal/app/bot` handler and never import each other; VK Group Long Poll is enabled at startup, messages and native geo are normalized to the common request, while texts, keyboards, PNG photos, and lossless document uploads are translated back to VK API calls;
- Telegram and VK run under independent retrying supervisors, so a platform API failure does not stop model synchronization or the other adapter;
- ordinary forecasts share one cross-platform calculation limit and report a queue position after both configured slots are occupied; Horizon retains its independent bounded queue and independently configurable worker count;
- VK photo/document uploads validate the handshake and retry at most twice with `1 s`/`2 s` backoff and a fresh upload URL; this handles transient `pu.vk.ru` `405` or incomplete upload responses without unbounded retries;
- sequential media deliveries in each VK request have a minimum `150 ms` interval without a global lock; parallel VK requests and Telegram delivery are unchanged;
- `/start` and `/help` explain requesting and interpreting all seven charts;
- the user summary reports the selected model, run ID, and model-run age against configurable `max_stale_age`; this threshold only controls the `⚠️ stale run` warning and never pins cached data. Point and render cache identities contain the run ID, so a newly published run is an automatic cache miss;
- Telegram language follows `User.language_code`: only `ru*` receives Russian, while every other or missing code receives English; help, statuses, errors, buttons, captions, the weather table, and all PNG titles/axes/legends are localized, and render-cache identity includes the language;
- VK currently defaults to Russian because Group Long Poll events do not include the Telegram-style language code;
- timezone lookup is offline and every PNG labels the coordinate timezone;
- DWD discovery selects the latest complete ICON-EU `00/06/12/18` cycle available through `+72 h`;
- coordinates outside the ICON-EU domain route to the latest complete ICON Global `00/06/12/18` cycle on its full native global grid; CDO uses the official static DWD grid geometry for nearest-native-cell point extraction, without cropping the source or creating a second world raster;
- ICON Global now synchronizes the same 79-hour, 27-level `CLC/P/T/QC/QI + lower U/V/TKE + HHL` contract as ICON-EU, using height-equivalent model indices shifted by `+46`; DWD Global TKE ends at `+48 h`, so bundles are 187 messages/hour through that point and 169 thereafter, the cloud map remains complete, and hybrid Overall stops rather than extrapolating missing turbulence; direct `VIS` also remains unavailable in the DWD Global feed;
- atomic sync streams pressure-level `U/V/FI/T`, retains no `.bz2`, and validates every bundle with ecCodes and SHA-256;
- legacy pressure-level runs atomically download only missing `FI/T`; the scheduler independently publishes versioned hourly surface/cloud bundles, while ecCodes 2.45 `clwmr/QI` names normalize to stable internal `qc/qi`;
- ICON-EU and ICON Global keep the preceding complete `current` run available while every component of a newer run downloads; `current` changes through one atomic symlink operation only after complete validation;
- an exclusive lock prevents overlapping syncs; the scheduler probes once at startup and every 15 minutes thereafter, retaining two runs; user requests never download data and only read the last complete publication;
- point extraction reads 25 vertical profiles concurrently from the current manifest;
- an independent surface sync publishes 79 hourly `f000…f078` bundles containing `T_2M`, `TD_2M`, `RELHUM_2M`, `CLCT/CLCL/CLCM/CLCH`, `TOT_PREC`, `U_10M`, `V_10M`, `VMAX_10M`, `PMSL`, `VIS`, `TQV`, and exact total-column `TQC/TQI`;
- both platforms show 72 hours; fog uses direct `VIS`, while the cloud chart shows effective obstruction by height from `CLC+QC+QI` and phase-resolved optical depth;
- dew is only an equipment-preparation advisory and does not lower seeing or the practical score;
- pure-Go astronomy calculates Sun, Moon, Jupiter, and Saturn events; Saint Petersburg and Moscow Sun/Moon regressions constrain the result to two minutes against reference data, while planetary events remain explicitly approximate;
- both platforms deliver the same seven PNG files; the production `3200×960` overall index combines hybrid ICON TKE/MH + HMNSP99 seeing, `tau0`, effective `CLCT+TQC+TQI` cloud transmission, fog, and a mild surface-wind factor;
- the text block gets a point light-pollution estimate from Light Pollution Atlas 2024: bilinear LPI/SQM at approximately `30″` plus an honestly labelled approximate Bortle value; light pollution is not part of the Overall Index;
- heat-map values are now 8 pt semibold, with slightly larger axes and bar labels;
- the old wind-only index remains a separate diagnostic chart; direction delta becomes `0°` below `2 m/s`, while `NaN` means missing data only;
- the container runs as `1000:1000` with a read-only root filesystem and bind mounts only below `/opt/docker/bot_astrosferum`;
- `go test ./...`, `go vet ./...`, and `doctor` pass.

Freshness and bilingual rendering were deployed on 2026-07-21: the production binary generated seven non-empty PNG files for each of `ru` and `en`, temporary verification directories were removed, the main container remained `running` with `restart_count=0`, and PostgreSQL remained `healthy`.

The VK adapter was deployed on 2026-07-22. Production starts Telegram and VK Group Long Poll under independent supervisors; VK settings report API `5.199`, Long Poll enabled, and `message_new=1`. The selected server was under `*.vk.ru`, the application container remained `running` with `restart_count=0`, PostgreSQL remained healthy, and the active ICON-EU/Global run symlinks were unchanged. The old platform-named 16 MiB render cache was removed after the shared bounded cache was initialized.

## Horizon analysis — implemented

The optional Horizon analysis is an existing application function controlled
by `ASTRO_HORIZON_ANALYSIS_ENABLED`. It is deliberately available only for an ordinary
ICON-EU forecast and covers the complete immutable `f000..f072` interval. ICON
Global exposes neither a Horizon button nor a runnable Horizon job. The
implementation consists of:

- an environment/configuration switch with validation, plus a disabled-state
  path that does not require Horizon operational limits;
- one small versioned application action router and normalized Telegram/VK
  callback events; platform adapters contain no Horizon formula; the
  composition root now constructs one shared Horizon service and attaches its
  button/action handlers to both enabled platforms;
- one shared bounded heavy-job queue with independently configurable workers, per-user admission,
  identical-key fan-out, timeout/cancellation, signed compact actions, and a
  separate bounded two-worker delivery path with bounded cache-hit admission;
- an atomic bounded disk cache with startup, periodic, and post-publication
  cleanup; short-lived hard-link leases keep an admitted PNG stable across
  eviction and abandoned staging/lease artifacts are removed at startup;
- provider-neutral spherical 10-degree/8-azimuth geometry at 500 m midpoint
  spacing and directional
  turbulence, transverse-wind, cloud, observer-fog, coarse-HHL terrain, index,
  and deterministic data-quality calculations;
- ICON-EU-only footprint checks and full immutable `f000..f072` acquisition:
  exact hourly surface/cloud/TKE/MH/visibility, same-run linear interpolation
  of raw three-hour pressure `U/V/T/Z`, and batched CDO extraction of
  deduplicated model cells with the separate `horizon_analysis.cdo_workers`
  subprocess limit;
- a 73-frame calculation contract that recomputes HMNSP/TKE, `Cn2`, seeing,
  `tau0`, and the index for every hour rather than interpolating nonlinear
  outputs, using the same complete calibration as Overall; that calibration is
  also part of Horizon cache identity;
- the shared target-agnostic utility mapping preserves physical seeing/`tau0`
  but bounds their combined score loss to 25%;
- current-run guards before cache reuse/heavy work, at the start and end of
  acquisition, after calculation/rendering, and immediately before every send,
  including cache hits; delivery-time captions use the configured ICON-EU
  `max_stale_age`;
- one localized `3200x1400` time-by-eight-direction heatmap for all 73 terms,
  with day/twilight/night shading, unavailable cells, run period, freshness,
  and compact limiter/quality disclosure;
- the per-cell data-quality strip follows forecast lead confidence: good at
  `C_lead >= 0.85`, usable at `0.75 <= C_lead < 0.85`, and limited at
  `C_lead < 0.75`; unavailable paths remain missing, while composite
  confidence below `0.60` can only downgrade a result;
- unit tests in the affected packages for callback normalization, queue/cache
  behavior, series identity, geometry, local-ENU wind projection,
  dateline/high-latitude cases, provider boundaries, sparse/missing profiles,
  and physical closures;
- a `render-horizon` command for a real current-run ICON-EU verification render;
- a geometric homogeneous-path reference derived from the implemented
  spherical ray itself, rather than a molecular-air-mass approximation; the
  physical slant seeing and `tau0` remain unchanged, while reference anchors
  use the exact path ratio raised to the Fried `3/5` power.

Production validation on 2026-07-22 used ICON-EU run `2026072218` and ICON
Global run `2026072212`. At the saved Plavsk point, the first ordinary
seven-chart calculation took `83.097 s`, the warm calculation took `9.193 s`,
and the complete 73-frame Horizon calculation took `123.541 s`. An ordinary
forecast completed in `10.074 s` while Horizon was actively extracting data,
below the `14.193 s` non-regression threshold. The Global smoke produced seven
charts in `174.586 s`; its Horizon command failed closed and created no PNG.
Post-deployment doctor, PostgreSQL readiness, current-run identity, startup
logs, restart/OOM state, bounded-cache cleanup, and secret/coordinate log scans
all passed.

Every deployment validates repository tests, race/vet/lint/build, one real
current-run ICON-EU calculation, the explicit ICON Global no-button/no-job
case, health and startup logs, ordinary and Horizon smoke tests, ordinary
forecast latency during concurrent Horizon work, and the administrator report.
Failure of any mandatory gate requires rollback or a failure report instead of
a success notification.

## Production Overall change

The audit of historical control case (an anonymized historical control case) found that the
deployed legacy HMNSP99 calculation does not resolve the PBL and yields a narrow series
around `0.70…0.77″`; the old smoothstep best boundary of `0.7″` and condensate
without a mismatched-`CLC` guard produce too many `10` values.

Production uses the following replacement:

- `surface-hourly-v17` publishes 17 single-level fields at each of 79 hours,
  including ICON `MH`/ecCodes `mld` in metres;
- `cloud-hourly-v4` publishes exactly 187 messages per hour:
  `CLC/P/T/QC/QI` at all 27 retained model levels, `U/V` on consecutive
  `58…74`, `TKE` on half levels `58…75`, plus separate `HHL` geometry;
- Masciadri `Cn²` is integrated trapezoidally from the surface to the hourly
  `h_PBL=clamp(ICON_MH,500,2000) m AGL`; HMNSP99 applies only above that same
  boundary, and total `J` gives model-derived seeing at 500 nm;
- `tau0` comes from `integral(Cn²·|V|^(5/3)dz)`; there is no second direction
  penalty because vector shear already includes wind rotation;
- physical seeing and `tau0` map into one bounded turbulence utility
  `f=0.75+0.25*q_turbulence`; they cannot veto a clear general-purpose hour,
  while cloud transmission and fog retain their stronger obstruction role;
- surface wind is a mild factor capped at 20%; dew is excluded, while possible
  and high fog use factors `0.75/0.10`;
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
- point-cache schema is `point-v6-native-cloud-mass-mh`, preventing reuse of
  old entries without `MH`, `T`, or native layer thickness;
- version markers match the new contract:
  `seeing-hybrid-tke-mh-hmnsp99-v6`,
  `conditions-v7-phase-structure-coherence`, `render-v15-four-solar-bands`, and
  `shared-render-v18-phase-structure-coherence`.

A server-side fixed-2-km calculation without fitting (`ground Cn² scale=1`)
produced control-case seeing of `2.221″` at `f042` and `3.644″` at `f048`; the
ground layer held 88.36% and 94.43% of `J`, respectively. After adding
`MH=396 m` at both leads, the unbounded cutoff gave `1.9075″/2.5158″` and the
production 500 m minimum gave `1.9145″/2.5478″`. This is a server-only
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
- runtime: `/opt/docker/bot_astrosferum/data/models/icon-eu/runs/2026071912`;
- current: `/opt/docker/bot_astrosferum/data/models/icon-eu/current`.

Last verified live state before the new deployment: extraction and all seven PNG files from the old version passed again on production run `2026072100` for an anonymized historical control case on 2026-07-21. The atomic publication contains 79×16 surface fields (`surface-hourly-v16`, 1.2 GiB) and 79×19×4 model-layer fields plus HHL (`cloud-hourly-v2`, 2.4 GiB); superseded versioned directories were removed after switching. The old cloud-physics check gave `99.90%` transmission and Overall `9.98` at 2026-07-22 09:00 UTC with `CLCT=72.8%`, `TQI=0.000015 kg/m²`, and `τ=0.00105`; at 02:00, `CLCT=100%` and `τ=2.78` gave `6.20%` transmission and Overall `1.03`. The first case is now a regression input for the unresolved-CLC floor, not the desired result. Old-version output is `3200×1080` weather, `3200×960` overall, `3200×1100` cloud obstruction, and four `1280×960` charts.

A bounded performance layer is now present: surface/wind/cloud extraction runs in parallel into one point bundle, concurrent identical misses are coalesced, `gob.gz` cache retention is two runs × 512 cells, and the in-memory LRU is bounded by both 512 cells and 20 GiB. Seven-chart render bundles live for 48 hours, capped at 256. Production uses provider-versioned point caches; obsolete schemas are ignored automatically. Each platform uses six peer-affine workers with a 15-minute request timeout so the first full-native Global point extraction can complete; ecCodes/CDO share a global eight-process limit. Each DWD object download still has a bounded two-minute HTTP timeout with three retries, while the scheduler itself is not capped by a user-request timeout. Cache staging older than one hour and Docker logs beyond `3 × 10 MiB` are removed automatically.

The first production request after the cache-schema change on run `2026072100` spent `38.5 s` extracting, `1.82 s` rendering seven PNG files, and `1.75 s` sending, for `42.24 s` total. A repeated CLI run loaded the point bundle from disk in `2 ms`; the seven-PNG set occupies about `2.7 MiB`. Render cache contains exactly seven files and remains bounded to 48 hours/256 sets.

The new production run `2026072106` was checked at historical control case. Initial
extraction of the expanded `187×79` cloud bundle took `1m14s`; a disk-cache hit
then takes `2 ms`, and a repeated full seven-PNG CLI render takes about `4.1 s`.
The 69 available future hours have Overall `1.00…4.58` with no exact `10`,
seeing `0.72…2.84″`, `tau0=1.72…3.50 ms`, and an applied MH clamp of
`500…2000 m`. Cloud transmission is below 50% in 47 hours and seeing is at
least `2″` in 21 hours, explaining the low distribution for this run.

The light-pollution provider is pinned to the validated Light Pollution Atlas 2024, downloads only required `5°×5°` tiles to the production host, atomically caches them below `data/light-pollution/lorenz-atlas`, and retains at most two years. It does not discover new versions automatically; an operator changes the configured year after validation. There is no separate World Atlas 2026 release as of 2026-07-21.

## Next vertical slice

1. Accumulate observational verification data for the existing forecast methods and monitor both platform adapters in production.

Production `seeing-hybrid-tke-mh-hmnsp99-v6` is not observationally calibrated until compared
with DIMM/MASS/SCIDAR data or observing logs in the priority regions.
