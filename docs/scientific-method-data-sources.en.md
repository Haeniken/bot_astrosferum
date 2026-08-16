# Astrosferum scientific method: data-source contracts

[← Main scientific method](scientific-method.en.md) ·
[Directional Horizon →](scientific-method-horizon.en.md)

**Status:** canonical section 6 of scientific method version 2.2. Splitting
the method across files changes presentation only; measured contracts,
versions, and verification evidence are unchanged.

## 6. Data-source contracts and server verification

Status: original Stage 0 spike updated with the current data contract;
`surface-hourly-v17`/`cloud-hourly-v6-native-mh` is the current contract.
Initial measurement date: 2026-07-19; updated 2026-07-22
Host: the production host
Runtime directory: `/opt/docker/bot-astrosferum/data/verification`

### 6.1. Conclusion

The initial production policy is technically viable:

- use ICON-EU directly for Saint Petersburg, Moscow, and other points inside its actual product domain;
- retain ICON Global as the worldwide fallback, with mandatory native-grid geometry for one-point extraction from `unstructured_grid`;
- keep ICON-Ru WIS 2.0 only as a candidate for a future shadow-verification adapter: none is implemented, its public field set cannot drive the complete seeing pipeline, and delivery is notification-based MQTT;
- no model file exists on the local workstation. Every downloaded artifact remains on the production host below the project directory.

This spike proves availability and technical processing, not comparative forecast accuracy. Accuracy is established separately against observations.

### 6.2. ICON-EU

Run `2026071906`, forecast step `+3 h`, was tested with 14 surface fields, six dynamic fields at five representative model levels, and all 75 `HHL` half levels.

#### Actual grid

| Property | Value |
|---|---:|
| `gridType` | `regular_ll` |
| `Ni × Nj` | `1377 × 657` |
| Latitude | `29.5° … 70.5° N` |
| Longitude | `336.5° … 62.5°`, equivalent to `23.5° W … 62.5° E` |
| Increment | `0.0625° × 0.0625°` |

Live element packages extend through `62.5° E`, rather than stopping at `45° E` as an older description indicated. Routing must use domain geometry from the downloaded product manifest, not a hard-coded document value.

Nearest cells for the control cities:

| Location | ICON-EU cell | Distance |
|---|---|---:|
| Saint Petersburg `59.9386, 30.3141` | `59.94, 30.31` | 0.15 km |
| Moscow `55.7558, 37.6173` | `55.75, 37.62` | 0.81 km |

#### Confirmed GRIB keys

| DWD field | `shortName` | Units | Use |
|---|---|---|---|
| `T_2M`, `TD_2M`, `RELHUM_2M` | `2t`, `2d`, `2r` | K, K, % | weather and dew |
| `U_10M`, `V_10M`, `VMAX_10M` | `10u`, `10v`, `VMAX_10M` | m/s | wind and gust |
| `PMSL` | `prmsl` | Pa | mean-sea-level weather pressure; never a SPECTRL2 input |
| `PS` | `sp` | Pa | catalogued direct surface pressure; not used by the current Reference-V reconstruction |
| `TOT_PREC` | `tp` | kg/m² | accumulated precipitation |
| `CLCL`, `CLCM`, `CLCH`, `CLCT` | same | % | cloud layers and total cloud |
| `T_G` | `T_G` | K | ground temperature |
| `U`, `V`, `T`, `P`, `QV` | `u`, `v`, `t`, `pres`, `q` | m/s, K, Pa, kg/kg | full model-level profile; lowest `P/T` plus `HHL` reconstruct station pressure for Reference V |
| `TKE` | `tke` | J/kg = m²/s² | prognostic turbulence on half levels |
| `HHL` | `HHL` | m | half-level heights |
| `MH` | `mld` | m | hourly mixed-layer depth |

For the sampled `+3 h` message, ecCodes reports `step=180`, `stepUnits=m`, and `stepRange=180`, not a three-hour integer. Accumulations use ranges such as `0-180`, while gust uses `120-180`. Code must parse `stepUnits` and `stepRange` instead of assuming that `step` is in hours.

#### Initial spike vertical subset

All `HHL` values across the two public control cities initially produced this
provisional 35-model-level subset:

```text
1, 3, 6, 10, 15, 18, 21, 25, 28, 31, 35, 38, 41, 44, 46, 49,
51, 53, 54, 56, 57, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69,
71, 72, 74
```

It covered mean heights from approximately 10 m to 22.3 km AGL. This is a
historical result of the first volume spike, not the current contract: sparse
lower-troposphere sampling proved inadequate for ground-layer seeing
gradients.

#### Measured volume

| Metric | Value |
|---|---:|
| Validated files | 119 |
| Compressed sample | 85,977,005 bytes |
| Expanded GRIB sample | 89,101,215 bytes |
| Repeated validation run duration | 49 s |
| Rough 72 h MVP run estimate | 6,307 files, 5,861,763,376 compressed bytes |

The `~5.46 GiB` figure was an early sample extrapolation. Controlled full run
`2026072106` of the current contract occupies about `13 GiB`: `9.6 GiB` cloud
`v4`, `1.3 GiB` surface `v17`, and `1.9 GiB` pressure-level `steps-v4`.
The complete staged sync took `8m34s`. Production discards `.bz2` after
validation, and the raw diagnostic spike was removed after recording results.

#### Refinement after the first complete live sync

The first user-facing charts use a simpler official DWD product: ready-made pressure-level `U/V`, rather than converting model levels to pressure. Run `2026071912` confirmed 20 levels:

```text
1000, 950, 925, 900, 875, 850, 825, 800, 775, 700,
600, 500, 400, 300, 250, 200, 150, 100, 70, 50 hPa
```

The sync fetched 25 valid times from `0…72 h` at three-hour intervals. Each set of 40 messages (`U/V × 20 levels`) was concatenated into one GRIB2 bundle, independently validated with `grib_count` and `grib_get`, hashed with SHA-256, and published through an atomic manifest. Measured results:

- 25 bundles;
- 1,000 source `.bz2` objects decompressed as streams without retaining compressed copies;
- approximately 1.0 GiB of published GRIB2;
- 46 seconds from start to publication on the production host with four workers.

Pressure-level `U/V/FI/T` drives wind charts and HMNSP99 above the hourly
boundary layer. Hourly model-level `CLC/P/T/QC/QI` plus `HHL` drives effective
cloud obstruction: `QC/QI` are `kg/kg`, while native-layer air mass is

```math
m_{\mathrm{air},k}=
\frac{P_{k,\mathrm{Pa}}}{287.05\,T_k}
\left|\mathrm{HHL}_k-\mathrm{HHL}_{k+1}\right|.
```

Sparse upper-level sampling therefore
does not assign the mass of skipped layers to a retained cell.

#### Current PBL and cloud contract

An anonymized control case confirms that pressure levels do
not resolve the first few hundred metres above the model surface: HMNSP99 gave
about `0.70…0.77″` without the PBL. The current versioned bundle therefore uses
sparse cloud levels `25,30,35,40,45,48,50,52,54,56` plus consecutive levels
`58…74`. All 27 cloud levels require `CLC/P/T/QC/QI`. A separate minimal
turbulence chain requires `P/T/U/V` on consecutive full levels `44…74` and
`TKE` on bounding half levels `44…75`; P/T already present in the cloud subset
are not duplicated. Level 44 keeps the native chain above the currently
encoded `MH=3000 m` ceiling throughout the ICON-EU domain; a future deeper
value fails closed rather than extrapolating missing state. The
`cloud-hourly-v6-native-mh` contract contains exactly 245 messages at every
`f000…f078` plus separate time-invariant `HHL` geometry.

Server-side verification for only `f042/f048` confirmed availability and units
of those DWD fields. The first fixed-2-km calculation produced `2.221″` and
`3.644″`, respectively; the ground layer contained 88.36% and 94.43% of total
`J`. The current design takes single-level `MH` every hour and uses

```math
h_{\mathrm{PBL}}=\mathrm{MH}\quad\mathrm{AGL},\qquad \mathrm{MH}>0,
```

applying HMNSP99 only above the same boundary. `MH` was `396 m` at both
control leads: the native cutoff gave `1.9075″/2.5158″`, while the former
500 m minimum gave `1.9145″/2.5478″`. This is a historical server-only
comparison without observational calibration. Full hourly
publication of run `2026072106` and post-deploy control validation are complete.

Diagnostic `CLC` can be large while grid-scale `QC/QI` is almost zero. The
current contract therefore applies the condensate obstruction and tier-aware
guard exactly as specified in [F7]–[F8]. The guard coefficients are `0.45` for
low, `0.2475` for middle, and `0.081` for high cloud; the latter two are 55%
and 18% of the low-cloud base. They represent conservative engineering
uncertainty, not physical opacity; resolved-condensate obstruction is retained
whenever it is stronger.

The **Effective ICON cloud obstruction** heatmap applies the shared
optical-depth kernel and guard policy at each native level. Overall instead
uses total-column `TQC/TQI` and `CLCT/CLCL/CLCM/CLCH`, combining tiers with
random overlap across the three aggregated tiers. A heatmap cell and Overall
therefore need not have the
same numeric value; the chart describes vertical optical obstruction, not
volumetric density.

DWD confirms the distinction between sub-grid diagnostic cover and grid-scale
condensate in its [ICON tutorial](https://www.dwd.de/EN/ourservices/nwp_icon_tutorial/pdf_volume/icon_tutorial2020_en.pdf?__blob=publicationFile&v=9).
The model has `QC_DIA/QI_DIA`, but the checked public
[ICON-EU directory](https://opendata.dwd.de/weather/nwp/icon-eu/grib/00/) does
not publish them. Available `CLCT_MOD` is also excluded: the official
[ICON database description](https://isabel.dwd.de/SharedDocs/downloads/DE/modelldokumentationen/nwv/icon/icon_dbbeschr_aktuell.pdf?nn=16102&view=nasPublication)
calls it a visualization field and warns that cirrus is ignored when only
high cloud is present. That is systematically unsafe for astronomy, so the
configurable tier-aware CLC guard remains the honest fallback.

#### Production surface extension

The same run has an independent atomic publication of 79 hourly `f000…f078`
surface bundles. The `surface-hourly-v17` contract contains 17 fields: the
existing weather data, exact total-column `TQC/TQI` (`kg/m²`), and
single-level `MH` (`mld`, metres). `CLCL/CLCM/CLCH` provide tier cover, `vis`
provides horizontal visibility, `TQV` provides PWV, `TQC/TQI` supports
phase-resolved Overall optical depth, and `MH` supplies the hourly hybrid
turbulence boundary. Message count, short names, `MH` units, size, and SHA-256
are validated per time; the independent `surface_steps` manifest section
appears only after all 79 files are complete. The new field set is published
in a versioned directory, the manifest switches atomically, and only then is
the previous directory removed. The enriched point caches use
`point-v8-native-mh-support` for ICON-EU and
`point-v4-native-mh-support` for ICON Global. The directory/schema change
rejects Gob bundles carrying the former heuristic field names, as well as old
entries without native thickness or `MH`, instead of silently decoding absent
fields as zero values.

`PMSL` in this surface contract remains the pressure reduced to mean sea
level for the weather product. Reference V does not reuse it: the same-run,
same-hour lowest native model-level `P/T` and full/surface `HHL` geometry form
station pressure through [F12c].

`TOT_PREC` accumulates from model initialization, so user-facing `mm/h`
values are derived only from adjacent hourly forecast times. The two GRIB
messages can use different packing resolutions. For each message the provider
therefore reads the ecCodes `packingError`, which bounds the unknown native
value by

```math
\widehat P_k-e_k<P_k\leq\widehat P_k+e_k .
```

For the first physical interval `[f000,f001]`, `f000` is the exact
zero-length origin of the accumulation: `P_0=0` and `e_0=0`. The provider
still requires the native `f001` accumulation and its packing error. Every
later interval requires both adjacent published accumulations and both packing
errors.

Positive or negative de-accumulated values satisfying

```math
\left|\widehat P_k-\widehat P_{k-1}\right|\leq e_k+e_{k-1}
```

are indistinguishable from zero and are set to zero. A decrease below
`-(e_k+e_{k-1})` fails validation instead of being hidden. Bilinear
interpolation does not enlarge the bound because its non-negative weights sum
to one. This follows the official
[ECMWF ecCodes guidance for small spurious values in start-of-forecast accumulations](https://confluence.ecmwf.int/display/UDOC/Why+are+there+sometimes+small+negative+precipitation+accumulations+-+ecCodes+GRIB+FAQ).
The response uses a rolling window of up to 72 future hours without
interpolating or inventing unavailable times. The `T−Td` spread is only used
to advise about possible dew and equipment protection; dew does not penalize
seeing or practical time ranking.

### 6.3. ICON Global

Run `2026071906`, field `T_2M`, forecast step `+3 h`, was tested.

The source file has:

- `gridType=unstructured_grid`;
- 2,949,120 points;
- `step=180`, `stepUnits=m`;
- compressed size 2,990,636 bytes.

ecCodes 2.28 does not implement nearest-neighbour lookup for this grid type, so ICON Global cannot reuse ICON-EU's direct `grib_get -l` path.

The initial spike validated a full-world remap with the official DWD
`ICON_GLOBAL2WORLD_0125_EASY` weights.  That path produced a `2879 × 1441`
regular grid and a single remapped `T_2M` message of 8,297,464 bytes, but doing
this for every required field and hour would store a second, much larger copy
of the global forecast.  Production therefore uses the more direct point path:

```text
DWD ICON Global GRIB (unstructured)
  + official DWD icon_grid_0026_R03B07_G.nc geometry
  -> CDO nearest-neighbour selection of one native node
  -> one-point regular GRIB2
  -> the shared ecCodes extractor
```

For target position `(lambda, phi)`, CDO selects native node `i*` by minimum
great-circle angular distance:

```math
i^*=\arg\min_i\arccos\left(\sin\phi\sin\phi_i+
\cos\phi\cos\phi_i\cos(\lambda-\lambda_i)\right).
```

This preserves full global coverage, does not crop Russia or any other region,
and does not pretend that bilinear interpolation adds information beyond the
roughly 13 km native mesh.  The static grid is downloaded once, atomically,
into the model volume. Forecast bundles remain immutable. All 25 pressure
steps and 79 hourly surface steps first pass message-count and SHA-256
validation; all 79 hourly model-level bundles and HHL geometry then pass
field/level/count/size/SHA-256 checks. A new run becomes `current` through one
atomic symlink replacement only after both stages are complete, so requests
continue using the preceding complete run during the larger cloud download.
Adding cloud data to a legacy already-current run uses one atomic manifest
replacement.

ICON Global uses the same physical input contract as ICON-EU. It retains 27
full levels `71,76,81,86,91,94,96,98,100,102,104…120` with `CLC/P/T/QC/QI`,
consecutive levels `87…120` with the native `P/T/U/V` turbulence chain, half
levels `87…121` with `TKE`, and the bounding `HHL`. The turbulence indices are
selected independently from the complete Global HHL grid rather than by
assuming a fixed numerical offset from ICON-EU. On immutable run
`2026081600`, the level-87 full-level midpoint remains at least `3119.33 m`
AGL over the whole grid, while level 88 reaches `2981.83 m` and is therefore
insufficient for the encoded `MH=3000 m` ceiling; the level-120 midpoint is
`9.99078…10.7408 m` AGL. The shared extractor and equations
therefore produce the same cloud-obstruction heatmap and hybrid TKE/HMNSP99
Overall method on either provider. Official DWD publishes the required
[CLC](https://opendata.dwd.de/weather/nwp/icon/grib/00/clc/),
[QC](https://opendata.dwd.de/weather/nwp/icon/grib/00/qc/),
[QI](https://opendata.dwd.de/weather/nwp/icon/grib/00/qi/),
[TKE](https://opendata.dwd.de/weather/nwp/icon/grib/00/tke/), and
[HHL](https://opendata.dwd.de/weather/nwp/icon/grib/00/hhl/) native products.
DWD Global TKE ends at `+48 h`, although the other selected model-level fields
remain hourly through `+78 h`. The first 49 bundles therefore contain 260
messages and the last 30 contain 225. Cloud obstruction remains complete; the
hybrid Overall sequence stops at `+48 h` rather than extrapolating TKE or
silently reverting to the known-optimistic free-atmosphere estimate. Global
still has no public `VIS` in this feed, so fog is not inferred from a
missing value and the transparency heuristic is marked unavailable. CDO is
installed only in the application image; the host requires no meteorological
packages.

### 6.4. ICON-Ru WIS 2.0

On 2026-07-19, discovery metadata are available from the DWD Global Discovery Catalogue and directly over HTTP from `wis2box.mecom.ru`. They confirm:

- public `ICON-Ru13/6N29` output at `0.25° × 0.25°`;
- `00/12 UTC`, three-hourly through `+72 h`, with more frequent precipitation through `+48 h`;
- origin topic:
  `origin/a/wis2/ru-roshydromet/data/core/weather/prediction/forecast/short-range/deterministic/limited-area`;
- origin broker `mqtt://<public-access>@wis2box.mecom.ru:1883`;
- Global Broker cache topics use the same suffix below `cache/a/wis2/ru-roshydromet/...`.

Observed limitation: HTTPS on `wis2box.mecom.ru:443` timed out from the production host after eight seconds, while the HTTP endpoint returned `200`. The advertised origin metadata and MQTT URI are not TLS-protected. The shadow adapter should therefore prefer a TLS WIS 2.0 Global Broker from the discovery catalogue instead of the origin endpoint; the public URI credentials are not project secrets.

Metadata files remain server-only under `/opt/docker/bot-astrosferum/data/verification/icon-ru-wis-spike/`. Object naming, message sizes, and redelivery semantics still require capturing a real broker notification after a run is published.

### 6.5. NASA GEOS-CF atmospheric composition

Reference V acquires hourly `AOD550` (the sum of the available aerosol
components) and total-column ozone from the public NASA GEOS-CF forecast via
OPeNDAP. These data are global and independent from ICON: spatial interpolation
is bilinear on the GEOS-CF grid, temporal interpolation is linear between
bracketing hourly fields, and dateline/pole handling is explicit. The client
uses bounded request/response sizes, a `60 s` HTTP timeout, LRU cache, and
singleflight. The optional operation belongs to the service-root context, has
a `65 s` total deadline, and is cancelled at shutdown rather than when one
user disconnects. The user-serving calculation joins it for at most `5 s`;
after that the ICON result continues while a successful bounded operation may
warm RAM. Identical slab requests coalesce. Distinct slab loads share a gate
capped by `ASTRO_FORECAST_CONCURRENCY`; a new optional request fails open
without starting when that gate is full.

The provider returns its own run/base time, grid, validity times, and
freshness. There is no test that can make a GEOS-CF frame “the same run” as an
ICON frame; only the requested validity time is aligned. A provider error,
stale frame beyond its configured limit, incomplete provenance, invalid AOD,
or invalid ozone fails open: it removes the Reference-V ring for that term and
marks its data partial/unavailable, but does not change generic Overall or
block the seven ICON charts. This avoids both silent run mixing and silent
substitution of climatology.

### 6.6. Implementation decisions

1. `iconeu.Sync` downloads only selected fields and levels, decompresses, validates, and merges messages per forecast step.
2. `iconglobal.Sync` follows the same atomic lifecycle while retaining full native-grid bundles; its point store applies official DWD grid geometry through CDO.
3. The coverage router selects ICON-EU inside its domain and ICON Global elsewhere; both normalize their outputs to the same forecast types before rendering.
4. `iconruwis` is not implemented; the WIS spike remains a candidate for a future isolated shadow adapter and cannot affect a user response.
5. Raw `.bz2`, runs, cache, verification, and temporary files live only in `/opt/docker/bot-astrosferum/data` and are excluded from Git and the Docker build context.
6. A complete 72-hour run is never downloaded locally and is not started on the server without a free-space check.


### 6.7. Remaining checks

- capture one real ICON-Ru WIS notification and record object URL, name, size, and redelivery semantics;
- perform observational calibration against DIMM/MASS/SCIDAR or high-quality logs; server-side checks validate the calculation, not forecast accuracy.
