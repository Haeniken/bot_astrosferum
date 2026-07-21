# bot_astrosferum: server-side data-source spike results

Status: original Stage 0 spike updated with the current data contract;
`surface-hourly-v17`/`cloud-hourly-v4` is published in production
Initial measurement date: 2026-07-19; updated 2026-07-21
Host: the production host
Runtime directory: `/opt/docker/bot_astrosferum/data/verification`

## 1. Conclusion

The initial production policy is technically viable:

- use ICON-EU directly for Saint Petersburg, Moscow, and other points inside its actual product domain;
- retain ICON Global as the Russia-wide fallback, with mandatory preprocessing from `unstructured_grid` to a regular grid;
- keep ICON-Ru WIS 2.0 in shadow verification: its public field set cannot drive the complete seeing pipeline and delivery is notification-based MQTT;
- no model file exists on the local workstation. Every downloaded artifact remains on the production host below the project directory.

This spike proves availability and technical processing, not comparative forecast accuracy. Accuracy is established separately against observations.

## 2. ICON-EU

Run `2026071906`, forecast step `+3 h`, was tested with 14 surface fields, six dynamic fields at five representative model levels, and all 75 `HHL` half levels.

### Actual grid

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

### Confirmed GRIB keys

| DWD field | `shortName` | Units | Use |
|---|---|---|---|
| `T_2M`, `TD_2M`, `RELHUM_2M` | `2t`, `2d`, `2r` | K, K, % | weather and dew |
| `U_10M`, `V_10M`, `VMAX_10M` | `10u`, `10v`, `VMAX_10M` | m/s | wind and gust |
| `PMSL`, `PS` | `prmsl`, `sp` | Pa | pressure |
| `TOT_PREC` | `tp` | kg/m² | accumulated precipitation |
| `CLCL`, `CLCM`, `CLCH`, `CLCT` | same | % | cloud layers and total cloud |
| `T_G` | `T_G` | K | ground temperature |
| `U`, `V`, `T`, `P`, `QV` | `u`, `v`, `t`, `pres`, `q` | m/s, K, Pa, kg/kg | full model-level profile |
| `TKE` | `tke` | J/kg = m²/s² | prognostic turbulence on half levels |
| `HHL` | `HHL` | m | half-level heights |
| `MH` | `mld` | m | hourly mixed-layer depth |

For the sampled `+3 h` message, ecCodes reports `step=180`, `stepUnits=m`, and `stepRange=180`, not a three-hour integer. Accumulations use ranges such as `0-180`, while gust uses `120-180`. Code must parse `stepUnits` and `stepRange` instead of assuming that `step` is in hours.

### Initial spike vertical subset

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

### Measured volume

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

### Refinement after the first complete live sync

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
`P_Pa/(287.05·T)·abs(HHL[k]−HHL[k+1])`. Sparse upper-level sampling therefore
does not assign the mass of skipped layers to a retained cell.

### PBL and cloud refinement after the control-case audit

An anonymized historical control case showed that pressure levels do
not resolve the first few hundred metres above the model surface: HMNSP99 gave
about `0.70…0.77″` without the PBL. The next versioned bundle therefore uses
sparse cloud levels `25,30,35,40,45,48,50,52,54,56` plus consecutive levels
`58…74`. All 27 full levels require `CLC/P/T/QC/QI`; the lower chain also
requires `U/V`, while half-level `TKE/HHL` supplies turbulence and geometry.
The two bounding TKE values map to each full layer. The `cloud-hourly-v4`
contract contains exactly 187 messages at every `f000…f078` plus separate
time-invariant `HHL` geometry.

Server-side verification for only `f042/f048` confirmed availability and units
of those DWD fields. The first fixed-2-km calculation produced `2.221″` and
`3.644″`, respectively; the ground layer contained 88.36% and 94.43% of total
`J`. The final design takes single-level `MH` every hour and uses
`h_PBL=clamp(MH,500,2000) m AGL`, applying HMNSP99 only above the same
boundary. `MH` was `396 m` at both control leads: the unbounded cutoff gave
`1.9075″/2.5158″`, while the 500 m minimum gave `1.9145″/2.5478″`. This is a
server-only regression without observational calibration. Full hourly
publication of run `2026072106` and post-deploy control validation are complete.

The cloud audit found a second optimistic bias: diagnostic `CLC` can be large
while grid-scale `QC/QI` is almost zero. Condensate obstruction
`B_cond=C·(1−exp(−tau/C))` therefore receives a tier-aware guard: `0.45·C` for
low, `0.2475·C` for middle, and `0.081·C` for high cloud. The latter values are
`55%/18%` of the low-cloud base. They represent conservative engineering
uncertainty, not physical opacity; stronger `B_cond` always wins through
`max`.

The **Effective ICON cloud obstruction** heatmap applies the shared
optical-depth kernel and guard policy at each native level. Overall instead
uses total-column `TQC/TQI` and `CLCT/CLCL/CLCM/CLCH`, combining tiers with
maximum-random overlap. A heatmap cell and Overall therefore need not have the
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

### Production surface extension

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
the previous directory removed. The enriched point cache uses
`point-v6-native-cloud-mass-mh`, preventing old `gob.gz` entries without
native thickness or `MH` from being interpreted as current data.

`TOT_PREC` accumulates from model initialization, so user-facing `mm/h` values are non-negative differences between adjacent hourly forecast times. The response uses a rolling window of up to 72 future hours without interpolating or inventing unavailable times. The `T−Td` spread is only used to advise about possible dew and equipment protection; dew does not penalize seeing or practical time ranking.

## 3. ICON Global

Run `2026071906`, field `T_2M`, forecast step `+3 h`, was tested.

The source file has:

- `gridType=unstructured_grid`;
- 2,949,120 points;
- `step=180`, `stepUnits=m`;
- compressed size 2,990,636 bytes.

ecCodes 2.28 does not implement nearest-neighbour lookup for this grid type, so ICON Global cannot reuse ICON-EU's direct `grib_get -l` path.

Validated KISS path:

```text
DWD ICON Global GRIB (unstructured)
  -> CDO remap with official DWD WORLD 0.125° weights
  -> regular-grid GRIB2
  -> ecCodes validation and nearest-point extraction
```

The official DWD `ICON_GLOBAL2WORLD_0125_EASY.tar.bz2` archive is 50,677,442 bytes. Output:

| Property | Value |
|---|---:|
| `gridType` | `regular_ll` |
| `Ni × Nj` | `2879 × 1441` |
| Domain | `-180° … 179.75°`, `-90° … 90°` |
| Increment | `0.125° × 0.125°` |
| Points | 4,148,639 |
| Test `T_2M` size | 8,297,464 bytes |
| SHA-256 | `c0845b267e7b4c0a8ab3a4552d7cf32a6eb246d72a2db4daad35856eeb58aca8` |

After remapping, ecCodes successfully extracted `T_2M` for both public control cities. CDO/ecCodes 2.28 emitted a `section2Padding` warning while returning exit code 0; the output independently passed `grib_count`, `grib_ls`, `cdo sinfo`, and nearest-point extraction. Production must still treat stderr as diagnostic input and publish only after independent output validation.

CDO is needed only on ICON Global's preprocessing path. It is not installed on the host; the spike ran inside `bot_astrosferum-tools:spike`.

## 4. ICON-Ru WIS 2.0

On 2026-07-19, discovery metadata are available from the DWD Global Discovery Catalogue and directly over HTTP from `wis2box.mecom.ru`. They confirm:

- public `ICON-Ru13/6N29` output at `0.25° × 0.25°`;
- `00/12 UTC`, three-hourly through `+72 h`, with more frequent precipitation through `+48 h`;
- origin topic:
  `origin/a/wis2/ru-roshydromet/data/core/weather/prediction/forecast/short-range/deterministic/limited-area`;
- origin broker `mqtt://everyone:everyone@wis2box.mecom.ru:1883`;
- Global Broker cache topics use the same suffix below `cache/a/wis2/ru-roshydromet/...`.

Observed limitation: HTTPS on `wis2box.mecom.ru:443` timed out from the production host after eight seconds, while the HTTP endpoint returned `200`. The advertised origin metadata and MQTT URI are not TLS-protected. The shadow adapter should therefore prefer a TLS WIS 2.0 Global Broker from the discovery catalogue instead of the origin endpoint; the public URI credentials are not project secrets.

Metadata files remain server-only under `/opt/docker/bot_astrosferum/data/verification/icon-ru-wis-spike/`. Object naming, message sizes, and redelivery semantics still require capturing a real broker notification after a run is published.

## 5. Implementation decisions

1. `iconeu.Sync` downloads only selected fields and levels, decompresses, validates, and merges messages per forecast step.
2. `iconglobal.Sync` follows the same lifecycle but remaps each field with official DWD 0.125° weights before merging.
3. Both providers publish the same normalized manifest and then share the ecCodes point extractor.
4. `iconruwis` remains isolated to control points and verification; its failure cannot affect a user response.
5. Raw `.bz2`, runs, cache, verification, and temporary files live only in `/opt/docker/bot_astrosferum/data` and are excluded from Git and the Docker build context.
6. A complete 72-hour run is never downloaded locally and is not started on the server without a free-space check.

## 6. Remaining data-source checks

- complete the controlled publication of `surface-hourly-v17` and
  `cloud-hourly-v4`, measure size/time, and run a post-deploy control request;
- verify hourly `MH`, hybrid seeing, native-layer cloud mass, and tier guards
  on the published run against the server-only `f042/f048` controls;
- capture one real ICON-Ru WIS notification and record object
  URL/name/size/redelivery;
- perform observational calibration against DIMM/MASS/SCIDAR or high-quality
  logs; current server-only checks validate the calculation, not forecast
  accuracy.
