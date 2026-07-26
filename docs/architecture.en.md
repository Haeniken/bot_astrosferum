# bot_astrosferum: KISS architecture

Status: implemented MVP, architecture revision 0.3; the
`surface-hourly-v17`/`cloud-hourly-v4` data contract is live in production.
The optional ICON-EU Horizon extension is implemented as a configuration-gated
application capability.
External sources last checked: 2026-07-19; last revision: 2026-07-22
Deployment target: operator-managed host
Deployment directory: `/opt/docker/bot_astrosferum`

## 1. Decision in one paragraph

`bot_astrosferum` is one Go binary running in one main Docker container. It serves Telegram and VK through long polling, updates ICON, calculates conditions, renders PNG charts, and returns them to the user. PostgreSQL stores users, saved points, and daily aggregates; Redis, a message broker, Kubernetes, a separate Python renderer, and a public HTTP port are unnecessary. Every bind mount remains below `/opt/docker/bot_astrosferum`.

### PostgreSQL, saved points, and statistics

PostgreSQL 18 is an internal Compose service with a `./data/postgres` bind mount. The application applies an idempotent schema on startup. It stores platform-namespaced numeric user keys, at most 10 named coordinates per user, and one daily forecast-count aggregate per user. Telegram and VK administrators are configured independently through `ASTRO_TELEGRAM_ADMIN_IDS` and `ASTRO_VK_ADMIN_IDS`, but both receive the same statistics aggregated across both platforms: the total platform-account count and one combined 30-day PNG usage chart. A person using both platforms counts as two accounts because the bot does not link external identities.

Daily aggregates older than 90 days are removed at startup and once a day; incomplete point-saving conversations expire after two hours.

### Light-pollution atlases

The primary line uses Light Pollution Atlas 2024 with lazy, atomic 30-arcsecond tile downloads. A separate comparison line uses the Falchi/GFZ World Atlas 2015. The mandatory GeoTIFF is checked at startup; when missing, the official ZIP is downloaded, safely extracted, and removed. Light pollution is not part of the Overall Astronomy Index.

The runtime is pinned to the official OSGeo GDAL 3.13.1 image. World Atlas coordinates are sampled bilinearly from a 2×2 source GeoTIFF window.

## 2. Core decisions

| Area | MVP decision | Reason |
|---|---|---|
| Executable | One `bot_astrosferum` Go binary | One build and one domain implementation |
| Toolchain | Go `1.26.5`, pinned in `go.mod` and the Docker builder | Reproducible current build |
| Runtime | One main `app` container | Minimal operational dependencies |
| Telegram | Bot API long polling | No ingress or webhook required |
| VK | Bots Long Poll API | No public callback endpoint required |
| Scheduler | Embedded in `bot_astrosferum serve` | No host cron or scheduler container |
| Operations | `sync`, `doctor`, `render-sample`, and point/Horizon render subcommands | Production code and image are reused; `render-horizon` remains a predeployment verification path |
| GRIB2 | ecCodes for ordinary extraction; CDO for ICON Global points and optional ICON-EU Horizon batches | No custom decoder or per-point intermediate GRIB |
| Priority model inside the European domain | DWD ICON-EU | Open ~7 km grid and complete surface/model-level field set |
| Model outside the European domain | DWD ICON Global | Stable worldwide official open GRIB feed |
| ICON-Ru | Documented candidate for a future shadow adapter | Its public WIS product is coarser and has fewer fields than the native model |
| Application storage | PostgreSQL for users; atomic files for model/cache data | Durable user state without adding Redis or a broker |
| Point cache | Versioned `gob.gz` plus an in-memory LRU | Preserves `NaN`, stays compact, and needs no Redis |
| Render cache | PNG files | Directly uploadable to both platforms |
| Work queue | Bounded Go channel plus per-key `singleflight` | No external queue is needed |
| Rendering | Deterministic pure-Go mixed-size PNG | Readable 72-hour tables without a Python service |
| Logs | Plain structured messages to stdout | Docker handles collection and rotation |

## 3. Model choice: accuracy and availability

Higher spatial resolution alone does not prove better accuracy for every variable. The final routing policy must be supported by local verification against observations. The initial provider should nevertheless have the strongest technical fit and the complete fields needed by the product.

### 3.1. ICON-EU is the primary candidate for priority regions

DWD publishes ICON-EU on a regular `0.0625°` output grid, approximately 7 km. A server-side spike on 2026-07-19 confirmed that the current open element packages contain `1377 × 657` points and cover `23.5° W–62.5° E`, `29.5°–70.5° N`. Saint Petersburg and Moscow are all inside this actual product domain. Geometry is still read from each manifest rather than hard-coded into routing.

Relevant properties:

- native 6.5 km grid, regular open output at about 7 km;
- four main runs at `00`, `06`, `12`, and `18 UTC` through +120 h;
- hourly output through +78 h, then every three hours;
- additional `03`, `09`, `15`, and `21 UTC` short runs through +30 h;
- surface fields and model-level `U`, `V`, `T`, `P`, `QV`, `TKE`, and `HHL` are present in DWD Open Data;
- weather, dew, vertical charts, and seeing features can use one physically consistent model run.

Sources: [DWD — NWP forecast data](https://www.dwd.de/EN/ourservices/nwp_forecast_data/nwp_forecast_data.html) and [DWD Open Data — ICON-EU GRIB](https://opendata.dwd.de/weather/nwp/icon-eu/grib/).

ICON-EU is therefore the default production provider for the four priority regions and other points inside the open domain. This is a justified starting point, not a claim that it is universally more accurate; the provider remains under continuous verification.

### 3.2. ICON Global is the worldwide fallback

ICON Global has a coarser grid of roughly 13 km, but covers the complete globe, has a stable official DWD GRIB feed, and supplies the same required class of fields. It is used:

- for any point outside the available ICON-EU domain;
- as the mandatory baseline in local verification;
- optionally for an extended forecast horizon.

Open ICON Global files use an `unstructured_grid` whose GRIB messages reference, but do not embed, the node coordinates. The provider keeps the complete native global bundles and downloads the official DWD `icon_grid_0026_R03B07_G.nc` geometry once. For a requested point, in-container CDO applies that geometry and selects the nearest native node; the tiny regular one-point result then enters the shared ecCodes extractor. The source is not cropped and no second world raster is stored.

Source: [DWD Open Data — ICON Global GRIB](https://opendata.dwd.de/weather/nwp/icon/grib/).

### 3.3. ICON-Ru: capable native model, limited public product

The native `ICON-Ru13/6N29` configuration and the publicly distributed product must not be conflated.

The Hydrometcentre describes a computational domain of `29.5°–90° N` across all longitudes, approximately 6.5 km grid spacing, 74 levels, four runs per day, and a +120 h horizon. The documented public WIS 2.0 product, however, has:

- a regular `0.25° × 0.25°` grid rather than the native 6.5 km grid;
- coverage of `35°–87° N`, `19.5°–193.5° E`;
- only `00` and `12 UTC` runs through +72 h;
- upper-air wind and temperature only at `925`, `850`, `700`, `500`, and `250 hPa`;
- no published TKE, HHL, or complete model-level output.

The discovery spike confirmed origin topic
`origin/a/wis2/ru-roshydromet/data/core/weather/prediction/forecast/short-range/deterministic/limited-area`. A future shadow adapter should subscribe to the equivalent `cache/a/...` topic on a TLS WIS 2.0 Global Broker rather than depend on the unencrypted `mqtt://wis2box.mecom.ru:1883` origin.

The current official system description also states that data assimilation is not yet used and initial conditions come from DWD ICON Global. A nested model can still improve mesoscale processes, but that benefit cannot be assumed for a public product regridded to 0.25°.

Sources: [Hydrometcentre — system description](https://mpr.meteoinfo.ru/en/srf-system-about), [ICON-Ru products for WIS 2.0](https://meteoinfo.ru/en/wis2-srf-products-of-wipps-dc-moscow), and the [Roshydromet product catalogue](https://meteoinfo.ru/images/media/books-docs/RHM/catalog-ASDT-20260116.pdf).

ICON-Ru WIS is therefore only a **candidate for a future shadow provider**. No production code currently downloads or scores it. If a later implementation and verification demonstrate a stable advantage for a specific region, variable, and lead-time band, routing can be reconsidered. A stable native 6.5 km feed must be evaluated as a separate product.

### 3.4. Initial production policy

```text
point inside open ICON-EU domain -> ICON-EU for the complete bundle
point outside ICON-EU domain     -> ICON Global for the complete bundle
ICON-Ru WIS                      -> not wired; future shadow study only
```

Using one provider for a production bundle keeps surface and upper-air fields physically consistent and simplifies the MVP. Every user response exposes the exact product, grid, and base time.

### 3.5. Proving which model is more accurate

The production host should retain matching ICON-EU, ICON Global, and ICON-Ru WIS forecast slices for control points in Saint Petersburg and Moscow. After valid time, forecasts are joined with trustworthy SYNOP, METAR, or equivalent observations.

Minimum metrics:

- temperature and dew-point MAE/bias;
- wind-speed MAE and circular wind-direction error;
- gust error;
- Brier score and contingency metrics for precipitation and cloud thresholds;
- separate scoring for `0–24`, `24–48`, and `48–72 h` lead times;
- separate statistics by region and season.

An initial decision may be reviewed after 30 days, but the router must not flap on a small sample. Verification continues indefinitely. Seeing requires a separate calibration source such as observing logs, DIMM/MASS, or at least upper-air observations; good surface-temperature scores do not prove seeing-index quality.

## 4. MVP boundaries

### Included

- Telegram and VK with equivalent commands and output;
- geo attachments and textual `latitude, longitude` input;
- a 72-hour horizon: hourly surface weather and three-hourly upper-air profiles;
- weather, cloud-tier, dew-risk, fog-risk, and celestial-event information in the first chart;
- a `1…10` forecast seeing index;
- an hourly `1…10` overall astronomy-suitability index;
- seven charts: hourly weather, the hourly overall index, hourly model-layer cloud, and four upper-air/seeing charts;
- automatic run synchronization and safe use of the last complete run;
- localized Russian/English UI: Telegram uses `language_code`, while VK currently defaults to Russian because Group Long Poll does not carry it;
- explicit model, run, grid, and freshness information.

### Excluded

- claims of physically measured seeing or a calibrated Pickering scale;
- telescope-specific optical predictions;
- web UI, accounts, payments, and subscriptions;
- long-term storage of conversations or exact user coordinates;
- non-ICON model families;
- a default user response beyond 72 hours;
- distributed multi-instance execution;
- automated ICON-Ru shadow ingestion and observational scoring: the source study is documented, but no production adapter exists yet.

## 5. System context

```text
DWD ICON-EU -------------+
                         |     +-------------------+
DWD ICON Global ---------+---->| Model providers   |
                         |     +---------+---------+
ICON-Ru WIS (planned) ---+               |
                                         v
                               +-------------------+
                               | Atomic model store|
                               +---------+---------+
                                         |
Telegram long polling ---+               v
                          |     +-------------------+
VK Bots Long Poll --------+---->| bot_astrosferum serve    |
                                | request pipeline  |
                                +---------+---------+
                                          |
                     +--------------------+-------------------+
                     v                    v                   v
               point cache        forecast calculator   PNG renderer
                     +--------------------+-------------------+
                                          |
                                          v
                                 Telegram / VK response
```

Everything after external APIs runs in one process. Components are Go packages, not networked microservices.

## 6. Binary and execution modes

```text
bot_astrosferum serve          # bots, scheduler, and request workers
bot_astrosferum sync           # one manual synchronization cycle
bot_astrosferum doctor         # config, secrets, ecCodes, disk, and run checks
bot_astrosferum render-sample  # render fixture charts without network access
```

`serve` starts:

1. configuration and directory validation;
2. Telegram poller;
3. VK poller;
4. bounded user-request worker pool;
5. model synchronization scheduler;
6. run and cache retention task;
7. `SIGTERM/SIGINT` graceful shutdown.

Manual `sync` and the embedded scheduler share one file lock so two refreshes cannot mutate the store concurrently.

## 7. Repository layout

```text
bot_astrosferum/
├── cmd/bot_astrosferum/              # main and subcommand parsing
├── internal/
│   ├── app/                   # lifecycle and shared bot application handler
│   │   └── bot/               # platform-neutral commands, forecast, persistence
│   ├── config/                # configuration loading/validation
│   ├── model/                 # providers, sync, GRIB extraction
│   ├── platform/
│   │   ├── telegram/          # Telegram Bot API adapter
│   │   └── vk/                # VK Group Long Poll/API adapter
│   ├── forecast/              # weather, dew, wind, seeing, astro score
│   ├── render/                # PNG and color scales
│   ├── store/                 # atomic files and caches
│   └── verify/                # shadow forecasts and model scoring
├── config/
│   ├── config.example.yaml    # tracked
│   └── config.yaml            # runtime, untracked
├── secrets/                   # tokens, untracked
├── docs/
│   ├── architecture.ru.md
│   └── architecture.en.md
├── data/                      # runtime only
├── logs/                      # reserved for optional file logs
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── .gitignore
```

Packages represent useful responsibilities. The project will not create ceremonial `entities/usecases/repositories` layers around every object.

## 8. Runtime directories on the production host

```text
/opt/docker/bot_astrosferum/
├── docker-compose.yml
├── Dockerfile
├── config/
│   ├── config.example.yaml
│   └── config.yaml
├── secrets/
│   ├── telegram_token
│   └── vk_token
├── data/
│   ├── models/
│   │   ├── icon-eu/
│   │   │   ├── incoming/<run-id>/
│   │   │   ├── runs/<run-id>/
│   │   │   └── current.json
│   │   ├── icon-global/
│   │   │   ├── incoming/<run-id>/
│   │   │   ├── runs/<run-id>/
│   │   │   └── current.json
│   │   └── icon-ru/                    # reserved; not created by current code
│   ├── cache/
│   │   ├── points/
│   │   └── renders/
│   ├── light-pollution/
│   │   └── lorenz-atlas/<year>/       # requested 5-degree tiles only
│   ├── verification/                  # forecasts, observations, metrics
│   └── tmp/
└── logs/
```

Compose uses only project-local bind mounts:

```yaml
volumes:
  - ./config:/app/config:ro
  - ./secrets:/run/secrets:ro
  - ./data:/app/data
```

There are no named or external Docker volumes. Token files are mounted at `/run/secrets`, have mode `0600`, and remain outside Git.

## 9. Minimal domain model

```go
type Location struct {
    Latitude  float64
    Longitude float64 // canonical range: -180..180
    TimeZone  string  // IANA name; UTC on resolver failure
}

type RunRef struct {
    Provider string
    RunID    string
    BaseTime time.Time
    Grid     string
    Horizon  time.Duration
}

type ForecastFrame struct {
    ValidAt time.Time
    Surface SurfaceFields
    Levels  []PressureLevel
    Astro   AstroFields
}

type ForecastBundle struct {
    Location Location
    Run      RunRef
    Frames   []ForecastFrame
}

type AstroConditions struct {
    SeeingIndex     float64 // 1..10 forecast index
    ConditionsScore float64 // 1..10 overall astronomy suitability
    Weather         WeatherSummary
    Dew             DewRisk
    Algorithm       string
}
```

Internal fields use SI units. Celsius, hPa, compass names, and localized strings are introduced only at response/render boundaries.

Every derived series retains provenance: provider, run ID, grid, selected cell coordinates, extraction time, and field-set version.

## 10. Provider boundary

The implemented serving boundary is deliberately smaller than a general provider framework:

```go
type ForecastStore interface {
    Vertical(context.Context, forecast.Location) (forecast.VerticalSeries, error)
    Surface(context.Context, forecast.Location) (forecast.SurfaceSeries, error)
    Cloud(context.Context, forecast.Location) (forecast.CloudSeries, error)
}
```

Implemented stores:

- `iconeu`: primary DWD ICON-EU provider within the available domain;
- `iconglobal`: DWD ICON Global store outside the ICON-EU domain;
- `model.CoverageFallback`: the small two-way router between them.

Synchronization clients and schedulers remain concrete because only two real providers need them. `Coverage` records geometry and longitude normalization for routing.

A future verified provider can implement `ForecastStore` without changing platform handlers, calculations, or rendering; no speculative adapter exists today.

## 11. Provider routing

| Condition | Production bundle | Notes |
|---|---|---|
| Point inside available ICON-EU element-package domain | ICON-EU | Surface and atmosphere from one run |
| Point outside available ICON-EU domain | ICON Global | Complete remaining world territory |
| Lead time beyond the 72-hour main horizon | Provider-specific through +120 h | Future/optional response |
| ICON-Ru WIS | Not selected | Verification pipeline only |

The domain comes from the actual product manifest. The spike confirmed an open boundary through `62.5° E`, but the router does not assume it can never change.

Fallback is never silent: provider, grid, and run are shown in text and in each chart footer. The initial router does not blend providers inside one production bundle.

## 12. Model synchronization

### 12.1. Complete cycle

1. The provider discovers the newest candidate run.
2. It confirms the mandatory final step and required field inventory exist remotely.
3. It creates `data/models/<provider>/incoming/<run-id>/`.
4. Files download to `*.part` names with bounded parallelism.
5. HTTP size, decompression, `grib_ls`, parameters, levels, and steps are validated. Valid time is derived from `step`, `stepUnits`, and `stepRange`; live DWD files may encode `180 m` instead of `3 h`.
6. ICON Global messages are retained on the full native grid and grouped by forecast step. At point extraction CDO applies the official static DWD grid geometry and performs nearest-native-node remapping; ICON-EU skips this step.
7. `manifest.json` is written and critical files are synced.
8. `incoming/<run-id>` is renamed to `runs/<run-id>`.
9. Only after the pressure, thermodynamics, hourly surface, and native-level cloud datasets are all ready does an atomic `current` symlink switch expose the run.
10. Old runs and invalidated caches are pruned after publication.

An incomplete run can never become current.

The current hourly ICON-EU contract consists of two independently validated
sets. `surface-hourly-v17` contains 17 single-level messages at every
`f000…f078`, including `MH`/`mld` in metres. `cloud-hourly-v4` contains exactly
187 messages per hour: `CLC/P/T/QC/QI` on all 27 retained full levels, `U/V`
on consecutive lower levels `58…74`, and `TKE` on half levels `58…75`; a
separate time-invariant bundle carries the required `HHL`. The manifest
switches only after every file passes message-count, shortName, level, size,
and SHA-256 validation.

ICON Global publishes the same cloud/PBL contract on the full native grid. Its
height-equivalent indices are `71,76,81,86,91,94,96,98,
100,102,104…120` for `CLC/P/T/QC/QI`, `104…120` for lower `U/V`, and
`104…121` for half-level `TKE/HHL`. These are the ICON-EU indices shifted by
`+46`, verified against actual HHL heights in the common domain. The complete
79-hour extension is validated in a staging directory and exposed by one
atomic manifest replacement. Point extraction applies the official Global
grid before the shared cloud extractor, so rendering and Overall use the same
types and equations for both providers.

DWD Global publishes model-level TKE only through `+48 h`. Bundles through
that time contain 187 messages; `+49…+78 h` bundles retain every
`CLC/P/T/QC/QI/U/V` message (169 total) and intentionally omit TKE. The cloud
heatmap therefore remains hourly for the full horizon, while the hybrid
TKE/HMNSP99 Overall series ends at the last native-TKE time instead of
extrapolating turbulence. Pressure-level wind/seeing diagnostics continue.

For subsequent EU and Global cycles, the `current` symlink is not switched
after a smaller intermediate stage. It remains on the preceding complete run
until every dataset required by the user forecast is ready, then switches once
atomically.

### 12.2. Run states

```text
discovered -> downloading -> validating -> ready -> published
                    |              |
                    +---- failed --+
```

State is written to `manifest.json`, allowing a restart to resume safe downloads without refetching already validated files.

### 12.3. Manifest

The manifest contains:

- provider and product;
- run ID and UTC base time;
- actual domain/grid;
- fields, levels, and forecast steps;
- final file size and SHA-256;
- preprocessing field-set version;
- download and validation times;
- a final `complete` flag.

### 12.4. Retention and disk policy

- retain two complete published runs for production providers, including
  their surface and model-level cloud bundles;
- retain one ICON-Ru shadow run or only extracted control-point series;
- remove abandoned `incoming` directories older than 24 hours;
- retain at most two point-cache runs and 512 cells per run; retain at most 256 render bundles for 48 hours;
- remove incomplete cache/render staging files after one hour and model `incoming` directories after 24 hours;
- do not begin sync below `min_free_space`;
- start with `min_free_space: 150GiB` on the production host;
- download only required fields and three-hour upper-air steps first;
- on low disk, remove disposable cache, then an old run; never delete current first.

## 13. Point extraction

No custom GRIB parser is required. ecCodes supports nearest-point access on regular grids through `grib_get -l latitude,longitude,1`; see the [official ecCodes documentation](https://confluence.ecmwf.int/display/ECC/grib_get). ICON-EU is regular already. For ICON Global, CDO first uses the official native grid geometry to produce a single regular point, after which the same normalized extractor is used.

Flow:

1. Normalize and validate input coordinates.
2. The provider selects the nearest point on its grid.
3. Derive grid cell ID from provider, grid identity, and selected grid coordinates.
4. Check point cache.
5. On a miss, invoke ecCodes for each merged `fNNN.grib2`.
6. Validate units, missing values, and ranges.
7. Convert output to a common `PointSeries`.
8. Atomically write `gob.gz` to point cache and place the bundle in a bounded in-memory LRU.

User coordinates are never manually rounded. Nearby users share a cache entry only when the provider selects the same model cell.

The optional Horizon path is intentionally different: it needs the full period
at many spatial cells, so each immutable pressure/surface/cloud/HHL bundle is
read by one CDO `remapnn + outputtab` batch across all deduplicated lookup cells.
It neither launches a separate `grib_get` loop for every midpoint nor stores
intermediate GRIB. Results enter the separate bounded Horizon cache described
in section 29, not the ordinary point cache.

Provider-specific point cache keys:

```text
ICON-EU:     point-v6-native-cloud-mass-mh/<run>/<grid-cell>.gob.gz
ICON Global: point-v2-native-cloud/<run>/<grid-cell>.gob.gz
```

## 14. Normalization and quality checks

Before calculations:

- normalize longitude and UTC timestamps;
- perform explicit K/°C and Pa/hPa conversions;
- require monotonically increasing valid time;
- convert accumulated precipitation to interval precipitation;
- reject cloud/humidity outside `0…100%`;
- preserve GRIB missing values as a mask rather than zero;
- reject physically impossible values;
- preserve the hourly surface/cloud timeline and interpolate the three-hour
  pressure-level profile only to those valid times;
- carry source and confidence metadata forward.

If one optional field is unavailable, its metric becomes `unavailable`; the complete response should not fail because a nonessential layer is missing.

## 15. Calculations

### 15.1. Wind

```go
speed := math.Hypot(u, v)
direction := math.Mod(math.Atan2(-u, -v)*180/math.Pi+360, 360)
```

Minimum angular difference:

```go
d := math.Mod(math.Abs(a-b), 360)
if d > 180 {
    d = 360 - d
}
```

Primary dynamic feature between adjacent levels:

```text
vector shear = hypot(u₂-u₁, v₂-v₁) / abs(z₂-z₁ in km)
```

Scalar speed delta is no longer plotted because unequal vertical spacing makes it misleading. When `min(speed₁,speed₂) < 2 m/s`, direction delta is displayed as `0°`: near-calm direction is unstable and its observational impact is small. `NaN` remains reserved for genuinely missing model data.

### 15.2. Vertical scale

Target chart levels:

```text
1000, 975, 950, 925, 900,
850, 800, 750, 700, 650,
600, 550, 500, 450, 400,
350, 300, 250, 200, 150,
100, 70, 50 hPa
```

Pressure is labeled on the left. Every heatmap shows actual mean geopotential height `FI/g` on a second right axis. The cloud chart uses full-layer midpoint height from adjacent `HHL` values; a standard-atmosphere conversion is only a legacy fallback.

The cloud/PBL input subset retains sparse free-atmosphere levels and a
continuous lower section:

```text
25, 30, 35, 40, 45, 48, 50, 52, 54, 56, 58, 59, 60, 61, 62,
63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74
```

Every one of the 27 levels carries `CLC/P/T/QC/QI`; consecutive full levels
`58…74` also carry `U/V`, while TKE comes from bounding half levels `58…75`.
`HHL` provides actual
geometry and the model surface. The continuous lower section is required for
the centred `theta` gradient and trapezoidal PBL integration; sparse levels
aloft support the cloud chart. The subset is versioned and every run manifest
records its exact list.

The ordinary point calculation already uses the ICON cell's HHL model-surface
elevation. It is the AGL origin for native cloud heights, the dynamic
mixed-layer boundary, and the ground-layer turbulence integral. Elevation is
therefore not ignored, but it is not added again as a standalone score bonus or
penalty: that would double-count model geometry and imply a local precision
which a roughly 7 km HHL cell does not have. HHL is neither a local DEM nor an
optical skyline model and does not resolve local terrain or obstructions. Horizon
analysis reuses the same HHL field only for a coarse directional ray/terrain
intersection.

Model-level data are interpolated to this scale only within the actual source range. No vertical extrapolation is allowed; cells beyond available data remain missing.

### 15.3. Forecast Wind Seeing Index

UI name: **Forecast Wind Seeing Index**, not “exact seeing” or “Pickering scale.”

This is the retained comparative wind-only diagnostic, not the physical
seeing used by Overall.

`seeing-v1` features:

- vector shear between adjacent upper-air levels;
- jet-region wind, primarily around 300–200 hPa;
- potential-temperature gradient/static stability;
- lower/free-atmosphere TKE when genuinely available;
- surface wind and gusts;
- penalties for missing fields and stale runs.

Each feature maps to a configurable piecewise-linear `0…1` penalty:

```text
penalty = weighted_mean(available feature penalties)
seeing  = clamp(1, 10, 10 - 9 * penalty)
```

Unavailable-feature weights are excluded from the denominator, while confidence drops. Output includes `confidence: low|medium|high`. Thresholds and weights are versioned and included in cache hashes.

Until compared with DIMM/MASS or observing logs, this is a comparative
heuristic forecast. Overall does not multiply by it: vector shear already
enters HMNSP99 and wind additionally enters physical `tau0`, so a second full
penalty would double-count the same flow.

### 15.4. Weather — mandatory separate line

`WeatherSummary` is calculated independently of seeing and overall score.

Classification order:

1. Determine precipitation type and intensity.
2. If precipitation is insignificant, classify total cloud cover.
3. Add notable gust/fog modifiers.

Initial configurable cloud categories:

- `0–20%`: clear;
- `20–50%`: partly/variably cloudy;
- `50–80%`: cloudy;
- `80–100%`: overcast/heavily cloudy.

Required response example:

```text
Weather: cloudy 21:00–03:00, light rain after 03:00; gusts up to 9 m/s.
```

### 15.5. Dew — mandatory separate line

The bot must not output an uncalibrated percentage probability. It reports `low / medium / high` risk plus an expected window.

`dew-v1` features:

- `T₂m - Td₂m` spread;
- relative humidity;
- surface temperature `T_G` when available;
- surface wind;
- low cloud and precipitation;
- recent temperature and spread trends.

If `T_G` is unavailable, spread/RH/wind are used and confidence is reduced. Thresholds are configuration/domain logic, never platform-adapter logic.

Required response example:

```text
Dew: high risk 00:00–05:00; minimum T−Td 0.8 °C.
```

Dew risk is an operational advisory only: it helps the observer prepare a heater, dew shield, or lens-care supplies. It does not reduce forecast seeing, lower the overall conditions score, or act as a hard gate; observing can remain fully worthwhile when the equipment is protected.

### 15.6. Hourly overall suitability index

This is neither another physical “seeing” scale nor a universal scientific
`1…10` scale. It is an auditable mapping of hourly suitability for visual
astronomy and astrophotography. Dew does not enter the index.

Optical turbulence uses a hybrid calculation. Each hour takes its PBL boundary
from single-level `ICON MH` and applies
`h_PBL=clamp(MH,500,2000) m AGL`. From the surface to `h_PBL`, native ICON
`T/P/TKE/HHL` supplies `Cn²` through the Masciadri expression
`3.35e−6·P^[2(1−2R/cp)]·theta^(−10/3)·|dtheta/dz|^(4/3)·TKE^(2/3)`;
the centred gradient is evaluated on consecutive model levels `58…74`, then
integrated trapezoidally from `HHL75`. Above that same hourly `h_PBL`, HMNSP99
uses pressure-level `T/U/V/FI`. `J_total=J_ground+J_free` becomes zenith long-exposure seeing at
`500 nm` through the standard integral. Wind enters
`tau0=[2.910·(2π/lambda)²·integral(Cn²·|V|^(5/3)dz)]^(−3/5)`. HMNSP99 vector
shear already includes direction changes, so no separate `direction delta`
multiplier is added.

Seeing and `tau0` map logarithmically between `.env` boundaries
`0.5…2.0 arcsec` and `5.2…1.6 ms`; `tau0` contributes inside the turbulence
quality with a mild weight of `0.25`. A convex utility mixture caps the
combined seeing/`tau0` penalty at `25%`, leaving physical values unchanged.
These anchors follow
[ESO categories](https://www.eso.org/sci/observing/phase2/ObsConditions.CRIRES.html),
but the composition itself remains an engineering mapping.

Overall uses phase-resolved optical depth from `TQC/TQI`:
`tau_phase=3·Qext·CWP/(4·rho·r_eff)` and
`B_cond=C·(1−exp(−tau/C))`. Diagnostic `CLC/CLCT` may coexist with almost zero
grid-scale condensate, so a tier-aware unresolved-cloud guard is applied: low
`0.45·C`, middle `0.55·0.45·C=0.2475·C`, and high
`0.18·0.45·C=0.081·C`. These are conservative engineering uncertainty
factors, not assigned physical opacity for the three tiers; physical
`B_cond` always wins when stronger. Overall combines the
`CLCL/CLCM/CLCH` tier guards with random overlap and caps them by
diagnosed cover.

The heatmap applies the same optical-depth kernel and tier-aware guard at each
native level. Its condensate path uses `QC/QI` and that layer's actual mass,
`m_air=P_Pa/(287.05·T)·abs(HHL[k]−HHL[k+1])`, rather than `Delta p` inferred
between sparsely sampled levels. Because the heatmap is native-level while
Overall is total-column, their values need not be numerically identical; the
shared contract is the optical physics and guard policy.

The tier-aware guard remains a configurable fallback: matching diagnostic `QC_DIA/QI_DIA`
is absent from public ICON-EU Open Data, while available `CLCT_MOD` is a
visualization field that DWD documents as ignoring cirrus when only high cloud
is present—unacceptable for astronomy.

Possible/high fog multiplies by `0.75/0.10`. Surface wind is only a mild
practical factor: smoothstep starts at `8.5 m/s` mean wind and `12 m/s` gust,
reaches its maximum at `15/22 m/s`, and the total penalty is capped at 20%.
With defaults `w_seeing=1` and `w_cloud=2`:

```text
q_turbulence = q_seeing^w_seeing * [1−0.25·(1−q_tau)]
f_turbulence = 0.75 + 0.25·q_turbulence
normalized = f_turbulence * q_cloud^w_cloud
             * q_surface_wind * q_fog
index = 1 + 9 * clamp(normalized, 0, 1)
```

All thresholds and multipliers enter the cache/version hash. An anonymized
historical control case demonstrates the old bias: pressure-level
HMNSP99 gave about `0.70…0.77″` without the PBL. A fixed-2-km diagnostic gave
`2.221″/3.644″`; after adding hourly `MH=396 m`, the clamp-500 rule gave
`1.9145″` at `f042` and `2.5478″` at `f048`. These are server-only regression
checks, not observational calibration. Production run `2026072106` is
published and passed post-deploy control validation. Absolute accuracy still requires DIMM/MASS/SCIDAR
or observing logs in the target regions, and the UI continues to label the
result model-derived. Full derivation and sources are in the
[scientific method and calculation note](scientific-method.en.md).

### 15.7. Point light pollution

Light pollution is a static site property rather than an hourly meteorological variable. It is shown in the text block and **does not enter the Overall Astronomy Index**.

The source is the latest published [David Lorenz Light Pollution Atlas](https://djlorenz.github.io/astronomy/lp/). As of 21 July 2026 this is Atlas 2024: annual 2024 VIIRS source radiance is transformed by a light-transfer, extinction, and atmospheric-scattering model into artificial zenith sky brightness. This is more current than World Atlas 2015 and more physically appropriate than directly converting a fresh VIIRS pixel: the satellite measures surface emissions, while an observer receives scattered light from surrounding sources.

Numeric tiles use a `1/120°`, approximately `30″`, grid. At the exact submitted coordinates the service bilinearly interpolates the four surrounding **LPI** values and computes `SQM = 22 − 2.5·log10(1+LPI)` in `mag/arcsec²`. LPI is artificial brightness divided by the adopted natural background. The response gives LPI, SQM, atlas year, and an approximate Bortle reference.

Bortle is a subjective all-sky visual classification, whereas the atlas models zenith only. The UI therefore says “approximate Bortle”, never presents it as a measurement, and merges the brightest conditions into `8–9`, where SQM alone cannot honestly distinguish the two classes. Actual conditions vary with transparency, aerosol, snow, immediate lamps, and the direction of light domes; only a site SQM/all-sky measurement can improve on the map.

The atlas year is explicitly pinned by `providers.light_pollution.atlas_year` / `ASTRO_LIGHT_POLLUTION_ATLAS_YEAR`; production currently uses `2024`. Runtime neither searches for nor enables a new version automatically: an operator changes the year only after validating the source and a test query. Only the coordinate's `5°×5°` tile is downloaded and atomically cached below `data/light-pollution/lorenz-atlas/<year>`. At most two annual directories are retained, bounding disk growth.

## 16. Astronomy and local time

- all internal calculations use UTC;
- each request resolves timezone from the coordinates submitted by that user;
- display uses the IANA timezone of that exact point, including the date-specific UTC offset;
- timezone is resolved offline; uncertain resolution falls back to explicitly labelled UTC;
- polygon lookup uses `tzf v1.2.3` with embedded boundary data and no network API;
- Go timezone data are embedded through `time/tzdata`;
- sunrise, sunset, moonrise, moonset, phase, illumination, and lunar-cycle day are calculated offline with Meeus algorithms (`github.com/soniakeys/meeus/v3`, MIT); horizon crossings are refined to the second inside each local civil day and rounded to the minute by the UI;
- a Saint Petersburg 20 July 2026 regression test limits every one of the four events to two minutes from the supplied reference;
- an independent Moscow (`55.7558, 37.6173`) regression on the same date uses the [USNO Complete Sun and Moon Data for One Day service](https://aa.usno.navy.mil/data/api.html): reference Sun `04:14/20:57`, Moon `12:25/22:33`; the pure-Go result stays within two minutes;
- every timestamp and chart in a response uses and labels the same resolved point timezone, for example `Europe/Moscow · MSK (UTC+3)` or fallback `UTC`.

## 17. Reference charts and render contract

The MVP renders seven images:

1. **72-hour hourly weather** — weather, dew/fog risk, transparency percentage, and flow-direction arrows. Sun, Moon, Jupiter, and Saturn rows contain only event icons and times; Moon phase stays separate. The background and its legend distinguish day, bright twilight (`0…−12°`), astronomical twilight (`−12…−18°`), and night (`<−18°`). Boundaries use the computed solar-altitude crossing and are rasterized at their sub-hour pixel position rather than rounded to a model term.
2. **Overall Astronomy Index** — hourly `1…10` bars; the top label is the result, the in-bar label is `ε/T%` (hybrid seeing and effective cloud transmission), and `F` marks high fog risk. The formula also contains `tau0`, possible fog, and the mild surface-wind factor. Background bands and the axis legend use the same four solar-altitude classes as chart 1/7, with sub-hour crossing positions; they do not change the score.
3. **Effective ICON cloud obstruction vs height/time** — hourly blocked-sky fraction across 27 model levels: sparse in the free atmosphere and consecutive `58…74` below. Each cell uses direct `CLC`, `T`, liquid `QC`, ice `QI`, and native thickness `|HHL[k]−HHL[k+1]|`; air mass is `P/(Rd·T)·dz`. Base obstruction `C·(1−exp(−τ_layer/C))` receives tier-aware low/middle/high guards of `45%/24.75%/8.1%` of `C`, while stronger physical `QC/QI` obstruction always remains. These percentages express engineering uncertainty, not opacity. Overall uses total-column fields and random overlap of the three aggregated tiers, so its `T%` need not equal a heatmap cell. Color and label show effective obstruction `0…100%`, not cover or volumetric density.
4. **Wind speed vs pressure/time** — wind-speed heatmap in m/s.
5. **Vertical vector wind shear** — `hypot(Δu,Δv)/|Δz|` in m/s/km.
6. **Wind direction delta** — adjacent-level minimum angle; weak wind below `2 m/s` is displayed as `0°`.
7. **Forecast Wind Seeing Index** — the retained wind-only `1…10` diagnostic; in-bar text such as `96%` shows the lead-time confidence heuristic.

PNG contract:

- `3200×1080` weather, `3200×960` overall index, `3200×1100` cloud, and `1280×960` remaining charts;
- labels readable on a phone;
- pressure axis with `50 hPa` at top and `1000 hPa` at bottom;
- hourly weather axis and three-hour upper-air axes;
- in-cell values only at adequate contrast;
- missing cells shown gray with `×`, never zero;
- calendar-day boundaries and daylight shading;
- every heatmap has a continuous color legend with minimum, midpoint, maximum, and dark/light meaning;
- coordinates displayed at limited precision;
- footer with provider/product, UTC base run, grid, algorithm version, and generation time;
- stable color scales within one renderer version;
- no `Pickering Scale` label until physically calibrated.

The weather legend also includes two fog levels. High risk requires direct `ICON VIS <1 km`, `RH ≥95%`, and `T−Td ≤1.5°C`; possible fog uses `VIS <5 km`, `RH ≥90%`, and `T−Td ≤2.5°C`. Saturation guards against calling smoke, dry haze, or precipitation fog solely from low visibility. The 1 km boundary follows the [WMO International Cloud Atlas](https://cloudatlas.wmo.int/fog-compared-with-mist.html). Official `CLCL/CLCM/CLCH` cover remains white below `10%`, blue at `10–49%`, and orange at `≥50%`; [ESO notes](https://www.eso.org/sci/observing/phase2/ObsConditions.CRIRES.html) that even thin cirrus can vary transparency by more than 10%.

The displayed `Transparency %` row is a versioned heuristic, not physical transmission. Its dominant factor is the maximum of `CLCT/CLCL/CLCM/CLCH`, refined by surface horizontal visibility `VIS` and total-column water vapour `TQV`/PWV. For `transparency-proxy-v0`, `C=max(CLCT,CLCL,CLCM,CLCH)`, `V=clip((VIS_km−5)/45)`, `W=1−clip((PWV_mm−5)/35)`, and the result is `100·(1−C/100)·(0.65+0.25V+0.10W)`. The coefficients are an uncalibrated KISS ordering aid, so the legend and `/start` retain the proxy qualification even though the compact row label says “Transparency %”.

DWD defines [`VIS` in metres and `TQV` in kg/m²](https://isabel.dwd.de/DWD/publikationen/dokumentation/grib/DWD_GRIB2_PARAMETER.htm); kg/m² is numerically equivalent to mm PWV. True optical transparency needs direct AOD/extinction observations: [DWD derives AOD and PWV through Sun, Moon, and stellar photometry](https://www.dwd.de/EN/research/observing_atmosphere/lindenberg_column/radiation/photometry.html). The proxy is therefore only for comparing hours in one forecast and is unsuitable for absolute photometry.

A droplet means a small `T−Td` spread and possible dew and never penalizes seeing. Each event icon is placed at its actual X position. The pure-Go Jupiter/Saturn approximation uses mean orbital elements plus a light-time iteration. It is explicitly an approximate planning aid; scientific validation should compare it against [JPL Horizons](https://ssd.jpl.nasa.gov/horizons/manual.html) for the site and date.

Preferred implementation: one pure-Go renderer based on `gonum/plot` and `golang.org/x/image` with an embedded Cyrillic-capable font. A Python/Matplotlib container is unnecessary.

Render cache key:

```text
shared-render-v1/<sha256-bundle-key>/<chart>.png
```

The SHA-256 identity currently includes the internal marker
`shared-render-v18-phase-structure-coherence`; changing it invalidates derived
images without renaming or duplicating the bounded cache root.

## 18. User flow

### 18.1. Input

MVP accepts coordinates through two equivalent input paths:

1. **Native location sharing:** Telegram `location` or a VK `geo` attachment. The bot offers the platform-native “Share location” button where supported.
2. **Plain text:** `59.9386, 30.3141` or `59.9386 30.3141`. `/forecast 59.9386 30.3141` remains an optional convenience syntax, not a requirement.

Both paths produce the same `Location` and enter the same pipeline. The bot confirms parsed latitude/longitude and the resolved timezone before or together with the result.

City names and street addresses are not parsed in the MVP because an address geocoder would add an external dependency and coordinate ambiguity.

### 18.2. Processing

1. A platform adapter converts an update to the common `bot.Update`.
2. Coordinates and command syntax are validated.
3. Local timezone is resolved.
4. Router selects provider and compatible current run.
5. Render cache is checked.
6. On a miss, point cache is loaded or extracted.
7. Data are normalized, calculated, and rendered.
8. A concise model/freshness/light-pollution summary is sent first, then seven sequential attachments: the three large charts as documents and four smaller diagnostics as photos.
9. The shared handler calls a small messenger interface implemented by the active platform adapter.

### 18.3. Response contract

```text
ICON-EU run 2026072206 UTC
Data freshness: 7 h 59 min
Period: 22.07 09:00 — 25.07 09:00
Grid: ICON-EU 0.0625°
Optical turbulence: seeing-hybrid-tke-mh-hmnsp99-v6; hybrid ICON model estimate …

Light pollution: Bortle reference 8–9 (LPI …, SQM …, Light Pollution Atlas 2024).
Light pollution comparison: Bortle reference 8–9 (LPI …, SQM …, World Atlas 2015).
```

Only platform adapters handle attachment-count and message-length limits. Domain text and charts remain shared.

## 19. Caching, queueing, and concurrency

- point cache keys use actual model cells, not rounded user coordinates;
- render cache includes run ID plus algorithm/renderer versions;
- one point bundle contains wind, surface, and cloud data; all three ecCodes extractions run concurrently;
- the RAM LRU is bounded by both 512 cells and an estimated 20 GiB; `GOMEMLIMIT=24GiB` leaves headroom for ecCodes and the runtime;
- identical concurrent point misses collapse through a per-key flight;
- each enabled platform has six peer-affine workers, so different peers run concurrently while each peer stays ordered;
- each adapter queue is bounded and applies backpressure to its own poller when full;
- one shared ordinary-forecast queue limits active calculations across Telegram and VK to `ASTRO_FORECAST_CONCURRENCY` and reports a position to waiting users;
- Horizon uses the independent `ASTRO_HORIZON_CONCURRENCY` limit and its existing bounded queue;
- ecCodes has a shared eight-process semaphore;
- publishing a new run cannot alter an in-flight immutable manifest reference;
- every cache write uses `temp + fsync + rename`.
- light-pollution tiles are cached independently of forecasts; the atlas year changes only through an operator action, and at most two years are retained.

Redis provides no useful benefit for one process.

## 20. Resilience and fallback

| Failure | Behavior |
|---|---|
| Candidate run incomplete | Previous current remains active |
| Provider unreachable | Keep using the last complete run and visibly warn when its age exceeds `max_stale_age` |
| Point outside ICON-EU coverage | Use ICON Global and disclose the selected model |
| ICON-Ru research source unavailable | Production unaffected; no production adapter depends on it |
| Some upper levels missing | Missing cells and lower confidence |
| One chart cannot render or upload | Send the summary and every successful chart; report failures after attempting all attachments |
| Telegram unavailable | VK continues, and vice versa |
| Container restarts | Each adapter requests a fresh long-poll cursor from its platform; no local offset file is maintained |
| Low disk | Do not sync; prune disposable cache; preserve current |
| ecCodes error | Mark file/run invalid and do not cache success |

Freshness limits are warning thresholds configured per provider; they do not select or pin a cache entry. Base time and run ID are always visible. Point and render cache keys include run ID, so publication of a newer complete run automatically bypasses every artifact from the previous run.

VK media upload uses at most three attempts. Each failed attempt waits `1 s`,
then `2 s`, and requests a fresh platform upload URL before retrying. Incomplete
upload responses are rejected before `photos.saveMessagesPhoto`/`docs.save`;
the retry loop remains bounded by the request context and never applies to
arbitrary external hosts. Successful VK media deliveries are paced at a
minimum `150 ms` interval within one sequential forecast delivery. There is no
shared lock, so concurrent VK requests do not block each other; the delay
exists only in the VK adapter and does not affect Telegram.

## 21. Configuration

Proposed minimal `config.yaml`:

```yaml
app:
  locale: ru
  horizon: 72h
  step: 3h
  workers: 6
  forecast_concurrency: 2
  eccodes_workers: 8
  point_cache_entries: 512
  point_cache_memory_limit: 20GiB
  request_timeout: 15m

horizon_analysis:
  enabled: true
  queue_size: 4
  concurrency: 1
  cdo_workers: 2
  job_timeout: 10m
  cache_ttl: 48h
  cache_entries: 128
  estimated_duration: 3m

paths:
  data: /app/data
  temp: /app/data/tmp

providers:
  icon_eu:
    enabled: true
    role: primary
    keep_runs: 2
    max_stale_age: 12h
  icon_global:
    enabled: true
    role: fallback
    keep_runs: 2
    max_stale_age: 18h
  icon_ru:
    enabled: true
    role: shadow
    mode: wis
    keep_runs: 1
    max_stale_age: 18h

sync:
  poll_interval: 15m
  download_parallelism: 4
  download_limit_mbit: 0
  min_free_space: 150GiB

algorithms:
  seeing_version: seeing-hybrid-tke-mh-hmnsp99-v6
  dew_version: dew-v1
  conditions_version: conditions-v7-phase-structure-coherence
  overall_seeing_weight: 1.0
  overall_cloud_weight: 2.0
  overall_coherence_time_weight: 0.25
  overall_optical_turbulence_max_penalty: 0.25
  overall_possible_fog_factor: 0.75
  overall_high_fog_factor: 0.10
  overall_good_seeing_arcsec: 0.5 # compatibility name: best threshold
  overall_bad_seeing_arcsec: 2.0
  overall_best_coherence_time_ms: 5.2
  overall_bad_coherence_time_ms: 1.6
  overall_boundary_layer_min_m: 500
  overall_boundary_layer_top_m: 2000
  overall_ground_cn2_scale: 1.0
  overall_unresolved_cloud_obstruction: 0.45
  overall_surface_wind_max_penalty: 0.20
  overall_surface_wind_start_ms: 8.5
  overall_surface_wind_full_ms: 15.0
  overall_surface_gust_start_ms: 12.0
  overall_surface_gust_full_ms: 22.0
  cloud_liquid_radius_micrometers: 10
  cloud_ice_radius_micrometers: 25

render:
  version: render-v9-dynamic-mh
  width: 1280
  height: 960

platforms:
  telegram:
    enabled: true
    token_file: /run/secrets/telegram_token
  vk:
    enabled: true
    token_file: /run/secrets/vk_token
    group_id: <community-id>
```

Every listed calibration parameter also has a matching `ASTRO_OVERALL_*`
environment variable, enters the version/cache key, and is test-covered. The
historical `overall_good_seeing_arcsec` name remains for compatibility but now
means the best saturation threshold, `0.5″`. Startup logs effective
configuration with all secrets redacted.

## 22. Docker deployment on the production host

### 22.1. Image

Multi-stage Dockerfile:

1. Go builder compiles `bot_astrosferum`.
2. The OSGeo GDAL Ubuntu runtime contains CA certificates, timezone data, ecCodes/CDO tools, and fonts.
3. Public Russian Trusted Root/Sub CA files are embedded only in the VK adapter's TLS pool; its transport accepts only `vk.ru`, `*.vk.ru`, `vk.com`, and `*.vk.com`, and VK API calls use `https://api.vk.ru`.
4. Process runs as non-root.
5. No public port is declared.

The Russian CA extension is not installed container-wide. It is compiled into
the VK adapter and combined with the system pool only for that adapter's
private transport. The transport rejects redirects and upload/Long Poll URLs
outside `vk.ru`, `*.vk.ru`, `vk.com`, and `*.vk.com`. Hostname and expiry
checks remain enabled. This only permits an alternative chain when VK serves
one; it does not manufacture a replacement for a revoked leaf, and Go does not
perform general CRL/OCSP revocation checking automatically.

The same image runs `serve`, `sync`, `doctor`, and fixture rendering.

### 22.2. Compose

Compose has PostgreSQL plus one application service:

- `restart: unless-stopped`;
- `init: true`;
- graceful `stop_grace_period`;
- project-local bind mounts only;
- bounded Docker log rotation;
- PostgreSQL healthcheck gates application startup;
- host-compatible UID/GID.

No nginx dependency or host-port collision is introduced.

### 22.3. Backup

Back up:

- source repository, Compose, and configuration;
- secret files through the existing secure secret backup process;
- `data/state` if user preferences are later introduced.

Downloaded GRIB, point cache, render cache, and verification raw cache are reproducible and need not be in mandatory backups.

Runtime retention is two model runs, at most 512 point bundles per run, at most 256 render bundles no older than 48 hours, and three Docker log files of 10 MiB each. Cache staging files older than one hour are removed at startup and on later writes. GRIB remains persistent on disk while Linux page cache uses spare RAM without a second tmpfs copy.

## 23. Observability

Structured events include:

- `sync_started`, `sync_completed`, `sync_failed`;
- provider, run ID, bytes, duration, and file count;
- `request_received`, `request_completed`, `request_failed`;
- platform, coarse region, cache status, and stage durations;
- current-run age and free disk;
- queue depth and active workers;
- verification joins and model scores.

Do not log tokens, full update payloads, private message text, or exact user coordinates. Use a grid cell ID or coarse region for diagnostics.

`doctor` checks configuration, secret readability, data write access, ecCodes version, disk space, current manifests, provider freshness, and fixture rendering.

## 24. Secure and private defaults

- non-root container with no Docker socket;
- no published TCP ports;
- token files only in untracked `secrets/` with mode `0600`;
- outbound requests have timeouts and size limits;
- platform-provided long-poll cursors prevent normal replay during a running process;
- bounded per-platform peer-affine queues prevent unbounded in-process request accumulation;
- command length and coordinate-range validation;
- `os/exec` receives an argument slice; user input never enters a shell command;
- downloaded GRIB is validated before publication;
- exact coordinates enter persistent user records only after an explicit save action. Unsaved coordinates may remain transiently in bounded render caches, including coordinate text embedded in generated PNGs, but those artifacts contain no user ID and expire under cache retention policy;
- debug output omits internal paths, secrets, and commands.

## 25. Testing

### Unit tests

- longitude normalization and coverage, including the date line;
- wind direction and angular difference around `0/360°`;
- vector shear and vertical interpolation;
- accumulated-to-interval precipitation;
- weather, dew, seeing, and conditions thresholds;
- provider routing/fallback and cache versioning.

### Fixture and integration tests

- small fixed GRIB2 fixtures for ecCodes;
- incomplete/corrupt runs never publish;
- atomic `current.json` survives restart;
- equivalent locations select the same cell;
- Telegram/VK adapters use redacted recorded API responses;
- graceful shutdown during sync and rendering.

### Golden image tests

- five baseline PNGs;
- dimensions, axes, labels, footer, and missing cells;
- Cyrillic and mobile readability review;
- explicit golden updates only with a renderer-version change.

### Model verification tests

- forecasts are stored before their valid time and cannot be backfilled with observations;
- observation joins use station/time tolerances explicitly;
- circular metrics are used for wind direction;
- score reports include sample count and confidence interval;
- router promotion requires a configured minimum sample and stable margin.

## 26. Delivery stages

### Stage 0 — mandatory data-source spike

Measured results and remaining checks are consolidated in the [scientific method](scientific-method.en.md#6-data-source-contracts-and-server-verification). ICON-EU keys, domain, current full-run volume, and timing are measured; ICON Global pressure, surface, model-level cloud/PBL data and native-grid point extraction are validated; ICON-Ru discovery metadata and topic are known. A real WIS notification and observational calibration remain outstanding.

1. Download a minimal complete ICON-EU field set and record real filenames/GRIB keys.
2. Confirm the open data geometry contains Saint Petersburg and Moscow.
3. Verify model-level `U/V/T/P/QV/TKE/HHL`, surface fields, units, and publication delay.
4. Download and validate the equivalent ICON Global set for fallback and baseline. Completed for pressure, surface, cloud/PBL, and HHL.
5. Connect to ICON-Ru WIS 2.0 for shadow verification.
6. Measure run size, download time, and `grib_get -l` latency on the production host.

Exit criterion: one fixture plus a reviewed “required field → actual GRIB key/source” table. `seeing-v1` must not be finalized without it.

### Stage 1 — vertical slice

- Go module and config loader;
- primary ICON-EU provider;
- manual synchronization of one forecast step;
- one-point extraction;
- seven fixture PNGs;
- `doctor`.

### Stage 2 — complete data path

- atomic runs and scheduler;
- ICON Global fallback adapter (implemented);
- ICON-Ru WIS source study; a shadow adapter remains planned;
- domain-based routing and single-provider bundles;
- point/render cache;
- weather/dew/seeing/conditions-v7-phase-structure-coherence;
- observational verification storage and scoring remain planned.

### Stage 3 — platform adapters

- common request/response contract;
- Telegram adapter;
- VK adapter;
- independent retry supervisors, bounded worker queues, native location conversion, and media upload.

### Stage 4 — production

- Dockerfile and Compose;
- deploy below `/opt/docker/bot_astrosferum`;
- PostgreSQL healthcheck and log rotation;
- restart/incomplete-sync recovery checks;
- disk and retention limits;
- first control-station verification dashboard/report.

## 27. MVP acceptance criteria

- the same coordinates produce equivalent Telegram and VK results;
- Saint Petersburg and Moscow use a complete ICON-EU production bundle unless verification changes policy;
- outside the available ICON-EU domain, ICON Global is used;
- ICON-Ru WIS cannot affect production because its shadow adapter is not yet implemented;
- provider, grid, run, and freshness are visible;
- weather, dew, and fog information is visible in the first chart;
- one readable `3200×1080` weather PNG, one `3840×1200` Overall PNG, one `3200×1100` cloud PNG, and four `1280×960` PNGs are produced;
- seeing is called a forecast index and includes confidence;
- incomplete runs never publish;
- current model manifests survive restart; platform cursors are reacquired from the APIs;
- tokens are absent from Git and logs;
- every bind mount is below `/opt/docker/bot_astrosferum`;
- failure of one platform API does not stop the other;
- the application publishes no host TCP port.

## 28. Open questions that must be measured

1. Actual size and wall time of a complete 72-hour ICON-EU synchronization; the current figure is sample-based.
2. ICON-Ru WIS broker and topic are known; object URLs, names, sizes, and redelivery behavior still require a real notification.
3. Whether a stable native 6.5 km ICON-Ru product can be legally and operationally accessed by this project.
4. Which observation feeds can be used reliably and lawfully for automated verification in the four priority regions.
5. Whether ICON-Ru demonstrates a statistically stable advantage over ICON-EU/Global for any field and lead-time band.
6. The user-data export/deletion policy before public launch; individual point deletion is already available.
7. Which observations will calibrate `seeing-v1` and `dew-v1`.
8. Whether users prefer four separate images or a combined two-page report; rendering can support either without changing calculations.

These items are resolved through the data-source spike and measured operation. They do not require changing the overall architecture.

## 29. Optional ICON-EU Horizon analysis

### 29.1. Product and provider boundary

Horizon analysis is an optional second-stage action after the ordinary seven-chart
forecast. It compares the eight fixed azimuths `N, NE, E, SE, S, SW, W, NW` at
a fixed `10 deg` geometric-elevation reference for every available hourly term in
the 72-hour ICON-EU period. It returns one compact time-by-direction heatmap and
summary; it does not regenerate or duplicate the ordinary weather charts.
The localized button displays the configured measured estimate (for example,
`Horizon (~3 min)`); this is scheduling guidance, not the job timeout.

This action is deliberately **ICON-EU-only**. The button may be offered only
when all of the following are true:

- the feature flag is enabled;
- the ordinary forecast provider is exactly ICON-EU;
- the period has pressure, surface, cloud, and HHL data from the same immutable
  run, with no cross-run extrapolation;
- the observer and every actual line-of-sight sample point remain inside the
  published ICON-EU coverage.

An ICON Global response exposes no Horizon button, and there is no Global
Horizon job or synthetic replacement. Callback data is untrusted and may be
stale or forged, so the workflow revalidates its signed provider/run/coordinate
contract, rebuilds the fixed period and footprint, and derives the cache key
from the current algorithm versions. A run change asks the user to build a new
ordinary forecast instead of mixing runs.

### 29.2. KISS package boundaries

The extension follows the existing dependency direction:

```text
Telegram callback ----+
                      +--> internal/app/bot action + Horizon job workflow
VK message_event -----+                  |
                                         +--> internal/model/iconeu batch snapshot
                                         +--> internal/forecast calculation
                                         +--> internal/render compact PNG
```

- `internal/app/bot` owns the versioned action route, hourly-period assembly, job
  lifecycle, deduplication, cache policy, localization, and delivery;
- `internal/model/iconeu` reads one pinned run/period and returns normalized
  provider-neutral snapshots; it contains no index formula or user flow;
- `internal/forecast` owns spherical geometry and all directional physical and
  engineering calculations; it imports no platform or model adapter;
- `internal/render` turns an already calculated result into one deterministic
  image and performs no network access;
- Telegram and VK remain peer transport adapters. Neither contains Horizon
  science, queueing, or an import of the other adapter;
- `cmd/bot_astrosferum` remains the composition root and constructs one shared
  Horizon workflow for both platforms when the feature is enabled.

The generic callback envelope is versioned and capped at the common Telegram
`callback_data` limit. Horizon uses the explicit action ID `horizon.v1`; its
compact payload is authenticated with a persistent HMAC key stored at mode
`0600`, but it is still revalidated against current model state. This is
sufficient for later actions without introducing a message broker, Redis,
another service, or a second bot handler. The callback contract follows the
[Telegram Bot API](https://core.telegram.org/bots/api); VK payloads are
normalized into the same application action.

### 29.3. Full hourly period from one immutable run

The heatmap uses the fixed `f000..f072` interval of the immutable ICON-EU run:
73 hourly terms spanning 72 hours, not a selected “best” hour. All eight
directions share exactly the same time axis. Daylight,
bright twilight (`0…−12°`), astronomical twilight, and night are visually marked at the computed sub-hour crossing positions; polar day
remains a valid, visibly daylight-only period rather than triggering a special
time selection. The caption identifies the run, live freshness, and complete
valid-time interval so elapsed early terms remain explicit.

The model adapter pins the whole period to one ICON-EU run. It batches every
required pressure-level `U/V/T/Z` step, every hourly surface, cloud, TKE, MH,
and visibility step, and the shared HHL geometry across all lookup cells. Raw
three-hour pressure variables are linearly interpolated only between bracketing
terms of that same run onto the hourly grid; HMNSP/TKE, `Cn2`, seeing, `tau0`,
and the index are recomputed after interpolation and are never themselves
interpolated. There is no extrapolation beyond `f072`, across a missing edge,
or across a run boundary. Lookup coordinates in
the same `0.0625 deg` output cell are deduplicated. CDO writes tabular values
directly; no intermediate per-point GRIB copies are retained. The scientific
equations and limitations are specified in
[the Horizon section of the scientific method](scientific-method.en.md#7-directional-horizon-analysis).

Run identity is checked before any cache reuse or heavy work. The source checks
the same current run before and after acquisition, the workflow checks again
after calculation/rendering, and the delivery workers recheck immediately
before every send, including a cache hit. If any check observes a newer run or
cannot confirm the current run, the PNG is not sent and the user is asked to
request a new ordinary forecast. The caption is generated at delivery time and
uses the configured ICON-EU `max_stale_age`, so cached and newly rendered
results report freshness by the same rule.

### 29.4. Separate heavy-work class

Horizon work must not consume the ordinary point-forecast workers. The current
source implements this single-process boundary:

- one shared bounded queue for both Telegram and VK, with the independently
  configurable `ASTRO_HORIZON_CONCURRENCY` worker count;
- a separate Horizon CDO semaphore controlled by
  `horizon_analysis.cdo_workers`, at most two subprocesses by default;
- two delivery workers and a separate bounded delivery queue, with bounded
  admission for cache-hit uploads, so slow platform uploads cannot create an
  unbounded goroutine or memory backlog;
- immediate callback acknowledgement followed by queue position and an honest
  approximate duration;
- one active request per platform/user identity, a three-second repeat-click
  guard, and fan-out for identical job keys bounded to
  `max(32, queue_size*32)` waiters;
- a job timeout and cancellation context owned by the background workflow, not
  by the short callback request;
- ordinary forecast workers, their semaphore, and their timeout remain
  independent.

Each extracted CDO table is normalized as its bounded worker finishes and the
raw table is released; only the normalized 73-frame result remains for the
calculation. Together with the two-process default this bounds Horizon's
incremental RAM use. The application container has no separate Docker hard
memory limit; `GOMEMLIMIT=24GiB` is a Go GC target, not an allocation guarantee.
Any increase must follow measured peak RSS during the mandatory full-run smoke
test and leave headroom for CDO, PostgreSQL, and the host page cache.

A bounded in-process channel is enough. In-flight jobs are not durable across a
restart: the user can retry, while an already completed atomic disk-cache entry
survives. This is a simpler and more honest failure model than introducing a
database-backed job system for one optional calculation.

### 29.5. Cache and cleanup

The Horizon cache is separate from the ordinary point/render caches. Its identity
includes E5 coordinates, rounded observer HHL, provider and run ID, the fixed
`window=f000-f072-hourly`, Horizon algorithm version, fixed geometry parameters,
the complete calibration shared with Overall (`ASTRO_OVERALL_*` and
`ASTRO_CLOUD_*` inputs), renderer algorithm version, and language. A calibration
change therefore produces a different key rather than reusing an old result.
The key material is hashed for the path, so raw coordinates do not appear in
filenames or logs. A cache hit is valid only for the exact immutable run and
calculation contract.

The scientific marker is `horizon-spherical-los-tke-hmnsp99-v6`; the application
cache schema is `horizon-cache-v1`. Changing either a formula or the
serialized/rendered contract requires changing the corresponding marker.

PNG and metadata are published by staging plus atomic rename. Retention is
bounded by a hard `CreatedAt` TTL and entry count; mtime is used only for LRU
ordering, so repeated hits do not extend lifetime. Cleanup runs at cache startup,
after publication, and periodically at `min(cache_ttl/4, 1h)` while the heavy
worker is running. A completed PNG admitted for delivery is hard-linked to a
temporary `.lease-*` file, so eviction of its keyed directory cannot invalidate
an in-progress upload; the delivery worker removes the lease after the attempt.
Startup removes abandoned `.incoming-*` and `.lease-*` artifacts. Cleanup
failures are surfaced or logged rather than being treated as successful cleanup.
The current development defaults are 48 hours and 128 entries. Runtime cache and
temporary data remain below the existing `data/` volume and are excluded from
Git. The PNG contains displayed coordinates rounded to four decimal places, but
the cache does not associate them with a user ID. Logs contain run ID,
period/term count, point count, duration, and safe error classes, but not
callback payloads, user identifiers, or exact coordinates.

### 29.6. Operational gate

The branch must not be described as deployed until all local checks pass, one
real ICON-EU calculation succeeds, and a negative ICON Global case proves that
neither button nor job is available. After deployment, `doctor`, startup logs,
an ordinary forecast, the Horizon button/action, and ordinary-forecast latency
during a concurrent Horizon job are mandatory gates. A failed gate requires
stopping or rolling back the deployment and reporting the failure rather than
sending a success notification.
