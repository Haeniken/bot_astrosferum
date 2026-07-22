# Scientific method and Overall Astronomy Index

Status: research-software method and calculation note, revised 21 July 2026.
This document is the canonical description of sources, units, formulas,
control calculations, validation, uncertainty, and configurable engineering
decisions in `bot_astrosferum`.

## Scope and scientific status

`bot_astrosferum` is research-oriented scientific software. It acquires
numerical weather-prediction data, applies documented physical and engineering
models, and produces reproducible diagnostics for observational astronomy and
astrophotography.

It is not a peer-reviewed scientific result, a calibrated observatory
instrument, or a substitute for local measurements. Seeing and Overall
Astronomy Index are model-derived estimates. Absolute accuracy requires
validation against DIMM, MASS, SCIDAR, site SQM, all-sky cameras, and
structured observing logs. A universal scientific `1…10` sky-quality scale
does not exist; Overall is an auditable mapping of physical and practical
factors to a convenient range.

## Research question and outputs

The practical question is: how suitable is a coordinate and hour for
astronomical observing, given modeled turbulence, wind, cloud obstruction,
fog, daylight, and astronomical events?

The answer remains decomposed into auditable outputs:

- meteorological conditions and tiered cloud cover;
- effective cloud obstruction by native model level;
- wind speed, vector shear, and direction-change diagnostics;
- wind-derived and combined astronomy-condition indices;
- Sun, Moon, Jupiter, and Saturn planning events;
- point light-pollution context, reported separately from the hourly index.

## Data provenance

Every forecast image identifies the provider, product, model run, grid, time
zone, algorithm version, and renderer version. The primary weather source is
DWD ICON-EU open data. Light-pollution context uses an operator-pinned annual
Light Pollution Atlas and a separate World Atlas 2015 comparison.

Downloaded runs and atlases are absent from Git because they are large,
replaceable upstream artifacts. Runtime manifests and versioned cache keys
prevent products generated from incompatible schemas from being mixed.
Architecture and runtime status are recorded in the
[KISS architecture](architecture.en.md) and
[implementation status](implementation-status.en.md). Data-source selection,
measured contracts, and acquisition verification are consolidated in
[section 12](#12-data-source-selection-and-server-verification) below.

## Formula ledger and research provenance

This is the complete calculation chain used by the current implementation.
Equations marked **published** are transcribed from the linked research;
equations marked **project rule** are explicit, configurable engineering
choices made by `bot_astrosferum`. The latter must not be presented as
peer-reviewed physical laws.

### A. Physical optical-turbulence model

For every model layer, potential temperature and vector wind shear are

```text
[F1] theta = T * (1000/P)^(R/cp),       R/cp = 0.286
     S     = sqrt((du/dz)^2 + (dv/dz)^2)
```

Above the planetary boundary layer, HMNSP99 is evaluated exactly as

```text
[F2] M = -7.9e-5 * (P/T^2) * d(theta)/dz

     L0^(4/3) = 0.1^(4/3) * 10^Y

     Y = 0.362 + 16.728*S - 192.347*dT/dz   (troposphere)
       = 0.757 + 13.819*S -  57.784*dT/dz   (stratosphere)

     Cn2_FA = 2.8 * L0^(4/3) * M^2
```

`P` is hPa, `T` and `theta` are K, `z` is m, `S` is s^-1, and `Cn2` is
`m^(-2/3)`. The stratospheric branch starts above the WMO thermal tropopause
diagnosed from the temperature profile; `P < 200 hPa` is only a fallback when
that diagnosis is unavailable. **Published basis:** the Tatarskii relation and
the HMNSP99 coefficients are reproduced as equations (1), (2), and (7) in
[Wu et al. (2021)](https://academic.oup.com/mnras/article/503/4/5692/6225358),
which attributes HMNSP99 to Ruggiero & DeBenedictis (2002). The tropopause
selection is a **project rule** implementing the WMO lapse-rate definition.

Within the boundary layer, native ICON TKE is used in the Masciadri relation:

```text
[F3] Cn2_GL = 3.35e-6
              * P^[2*(1 - 2*R/cp)]
              * theta^(-10/3)
              * abs(d(theta)/dz)^(4/3)
              * TKE^(2/3)
```

**Published basis:** equation (12) of
[Cuevas et al. (2024)](https://academic.oup.com/mnras/article/529/3/2208/7617711),
following the PBL parametrization of Masciadri & Jabouille (2001). Using
ICON's prognostic TKE in this published relation is the model adaptation made
here; `ground_Cn2_scale=1.0` applies no empirical fit by default.

The two non-overlapping regions are integrated and converted to seeing:

```text
[F4] h_PBL  = clamp(ICON_MH, 500 m, 2000 m) AGL
     J_GL   = integral(surface .. h_PBL, Cn2_GL dz)
     J_FA   = integral(h_PBL .. model_top, Cn2_FA dz)
     J      = J_GL + J_FA

     r0     = [0.423 * (2*pi/lambda)^2 * J]^(-3/5)
     epsilon_rad = 0.98 * lambda/r0
                 = 5.25 * lambda^(-1/5) * J^(3/5)
     epsilon_arcsec = 206264.806247 * epsilon_rad
```

`lambda=500e-9 m`. The `r0` and seeing equations are equations (13) and (14)
of [Cuevas et al. (2024)](https://academic.oup.com/mnras/article/529/3/2208/7617711);
the underlying Fried parameter originates in
[Fried (1965)](https://opg.optica.org/abstract.cfm?uri=josa-55-11-1427).
The `500..2000 m` clamp is a configurable **project rule**, not a published
universal PBL boundary.

Wind affects the physical result twice, but through two different moments:
vector shear is already in HMNSP99 `Cn2`, while absolute wind speed determines
the atmospheric coherence time:

```text
[F5] J_V  = integral(Cn2(z) * |V(z)|^(5/3) dz)
     tau0 = 0.058 * lambda^(6/5) * J_V^(-3/5)
```

**Published basis:** equation (11) of
[Zhang et al. (2021)](https://academic.oup.com/mnras/article/505/1/582/6273151)
and the equivalent effective-wind definition discussed by
[Osborn et al. (2020)](https://academic.oup.com/mnras/article/496/4/4822/5863963).
No separate direction-change penalty is multiplied into Overall, because it
would count the vector-shear contribution again.

### B. Cloud obstruction

For liquid and ice separately, the condensate mass path and optical depth are

```text
[F6] rho_air   = P_Pa/(Rd*T),                  Rd = 287.05 J/(kg*K)
     CWP_phase = q_phase * rho_air * dz_native
     tau_phase = 3*Qext_phase*CWP_phase/(4*rho_phase*r_eff_phase)
     tau       = tau_liquid + tau_ice
```

Defaults are `Qext_liquid=2.0`, `Qext_ice=2.1`,
`rho_liquid=1000 kg/m3`, `rho_ice=916.7 kg/m3`,
`r_eff_liquid=10 um`, and `r_eff_ice=25 um`. The optical-depth relation
reduces to `tau=3*LWP/(2*rho_water*r_eff)` for `Qext=2`, matching equation
(19) of
[Gryspeerdt et al. (2019)](https://www.nature.com/articles/s41467-019-12982-0).
The fixed effective radii and ice extinction efficiency are **project
assumptions**, required because public one-moment ICON fields contain mass but
not particle number or effective radius.

ICON condensate is a grid-box mean. For cloudy fraction `C`, the code treats
`tau/C` as in-cloud optical depth and applies Beer-Lambert extinction:

```text
[F7] T_cloudy = exp(-tau/C)
     q_cloud  = T_all_sky = (1-C) + C*T_cloudy
     B_cond   = 1 - q_cloud = C*(1-exp(-tau/C))
```

This all-sky mixture is a **project derivation** from the published optical
depth relation, not an equation claimed by Gryspeerdt et al. A diagnosed-cloud
uncertainty guard is then applied when public `QC/QI` or `TQC/TQI` do not
represent diagnostic `CLC`:

```text
[F8] g_low=0.45;  g_middle=0.55*g_low;  g_high=0.18*g_low
     B_tier = g_tier*C_tier
     B_guard = min(C_cap, 1-(1-B_low)*(1-B_middle)*(1-B_high))
     q_cloud = min(q_cloud, 1-B_guard)
```

The product is the random-overlap expression for the three already aggregated
tiers; random and maximum overlap limits are set out explicitly by
[Pincus et al. (2005)](https://agupubs.onlinelibrary.wiley.com/doi/10.1029/2004JD005100).
The tier factors and choosing random overlap here are conservative **project
rules**; they are not fitted cloud optical properties. The formula is not
called maximum-random overlap because adjacent native layers are not being
grouped into maximum-overlap blocks in this calculation.

### C. Engineering mapping to Overall `1..10`

The physical outputs are mapped without rounding:

```text
[F9] q_seeing = clamp(ln(epsilon_bad/epsilon) /
                      ln(epsilon_bad/epsilon_best), 0, 1)

     q_tau = clamp(ln(tau0/tau_bad) /
                   ln(tau_best/tau_bad), 0, 1)
     q_coherence = 1 - w_tau*(1-q_tau)

[F10] smoothstep(x;a,b): t=clamp((x-a)/(b-a),0,1);  f=t^2*(3-2*t)
      r_surface=max(smoothstep(V10;8.5,15), smoothstep(gust;12,22))
      q_surface=1-0.20*r_surface

[F11] q_fog = 0.10  if visibility<1 km, RH>=95%, T-Td<=1.5 C
      q_fog = 0.75  if visibility<5 km, RH>=90%, T-Td<=2.5 C
      q_fog = 1.00  otherwise

[F12] Q = q_seeing^w_seeing * q_coherence
          * q_cloud^w_cloud * q_surface * q_fog
      Overall = 1 + 9*clamp(Q,0,1)
```

Defaults are `epsilon_best=0.5 arcsec`, `epsilon_bad=2.0 arcsec`,
`tau_best=5.2 ms`, `tau_bad=1.6 ms`, `w_tau=0.25`, `w_seeing=1`, and
`w_cloud=2`.
[ESO observing-condition categories](https://www.eso.org/sci/observing/phase2/ObsConditions.CRIRES.html)
inform the [F9] reference ranges. [F10] is intentionally mild because the
site measurements of
[Catala et al. (2013)](https://academic.oup.com/mnras/article/436/1/590/975197)
found only a weak surface-wind/seeing relation except at high wind, while
[ESO operations](https://www.eso.org/sci/facilities/paranal/sciops/At_Telescope.html)
provide practical wind limits. The
[WMO International Cloud Atlas](https://cloudatlas.wmo.int/fog-compared-with-mist.html)
defines fog by horizontal visibility below 1 km and supports the high-risk
visibility boundary in [F11]. However, equations [F9]-[F12], all thresholds,
weights, fog factors, smoothstep, and the final `1..10` transform are
**project rules**. The added humidity and dew-point conditions prevent
precipitation or dry haze from being mislabeled as fog.
Dew risk, daylight, Bortle class, and planetary events do **not** enter [F12].
Day/night is only visual shading on the hourly Overall chart.

## 1. Why the old index was too optimistic

The saved user control point is:

```text
Historical diagnostic case; exact user-provided location removed from public artifacts.
```

On production run `ICON-EU 2026072100`, the old pressure-level HMNSP99 model
produced an almost flat series around `0.70…0.77 arcsec` without a resolved
planetary boundary layer (PBL). Its full audited-window range was
`0.682…0.796 arcsec`, and 13 hours rounded to `10.0` on the Overall chart.

There were three causes.

1. The checked profile placed `1000 hPa` at `67.12 m MSL`, below the model
   surface `HHL75 = 219.75 m MSL`; `950 hPa` was already at `500.34 m MSL`.
   Roughly the first 280 m above ground was unresolved. In `f036`, the part
   below ~1.5 km contributed only about 0.6% of the `Cn²` integral.
2. Seeing used a smoothstep between `good=0.7″` and `bad=2.5″`; every
   `seeing <= 0.7″` immediately became the ideal `q_seeing=1`.
3. Diagnostic `CLC/CLCT` cover sometimes coexisted with almost zero
   grid-scale `QC/QI` or `TQC/TQI`. The condensate-only formula then treated
   a cloudy hour as almost perfectly transparent.

Example of the third cause for the historical control case, `2026-07-22 21:00 MSK`:

```text
CLCT = 65.21% (low 23.05%, middle 52.33%)
TQC  = 0
TQI  = 4.41e-17 kg/m²
tau  ~= 0
old computed transmission ~= 100%
old Overall = 10.0
```

This does not prove cloud transparency. In ICON, `CLC` is a diagnostic grid
fraction that includes sub-grid variability, whereas `QC/QI` are prognostic
grid-scale mixing ratios. They describe different aspects of the cloud field
and need not close numerically. DWD explicitly describes this split in the
[ICON tutorial's Cloud cover section](https://www.dwd.de/EN/ourservices/nwp_icon_tutorial/pdf_volume/icon_tutorial2020_en.pdf?__blob=publicationFile&v=9),
while field semantics and vertical staggering are documented in the official
[ICON database description](https://isabel.dwd.de/SharedDocs/downloads/DE/modelldokumentationen/nwv/icon/icon_dbbeschr_aktuell.pdf?nn=16102&view=nasPublication).

The model has diagnostic `QC_DIA/QI_DIA` fields that match the diagnostic
scheme, but they are absent from the actual public
[ICON-EU Open Data directory](https://opendata.dwd.de/weather/nwp/icon-eu/grib/00/).
The available `CLCT_MOD` is not a solution either: DWD defines it as a
visualization field and notes that it ignores cirrus when only high cloud is
present. That is unsuitable for observational astronomy, where thin cirrus
matters.

## 2. Free atmosphere: HMNSP99

HMNSP99 remains the free-atmosphere parametrization above the PBL. Per layer:

```text
theta = T * (1000 / P)^0.286
M     = -79e-6 * P / T² * d(theta)/dz
S     = hypot(du, dv) / dz
Y     = 0.362 + 16.728*S - 192.347*dT/dz  # troposphere
      = 0.757 + 13.819*S -  57.784*dT/dz  # stratosphere
L0^(4/3) = 0.1^(4/3) * 10^Y
Cn²   = 2.8 * L0^(4/3) * M²
```

`P` is in hPa, `T` in K, `z` in m, and `S` in s⁻¹. Coefficients and units
follow the published
[HMNSP99 assessment](https://academic.oup.com/mnras/article/503/4/5692/6225358).
Vector shear `hypot(du,dv)` already includes changes in both wind speed and
direction.

HMNSP99 is useful aloft but does not replace a PBL model. The profiler
comparison in [Cuevas et al. (2024)](https://academic.oup.com/mnras/article/529/3/2208/7617711)
shows that free-atmosphere models perform better than ground-layer models and
that the best tested approach combines a TKE-based PBL parametrization with a
separate model above it.

## 3. Ground layer: native ICON TKE, hourly `MH`, and the Masciadri equation

The ground-layer boundary is no longer fixed at `2 km AGL`. Every hourly
forecast time uses the ICON single-level `MH` field (ecCodes `mld`, mixed-layer
depth in metres), bounded as follows:

```text
h_PBL = clamp(MH, 500 m, 2000 m) AGL
```

The 500 m minimum prevents a very shallow or unstable `MH` from excluding the
first few hundred metres again; the 2000 m maximum bounds the domain of the
PBL parametrization. Both limits are configurable through `.env`, and each
result records the `h_PBL` actually used.

The interval from the model surface to `h_PBL` uses consecutive native
ICON-EU full model levels `58…74`:

- `T`, `P`, `U`, and `V` on full levels;
- `HHL` as the actual geometric boundaries;
- `TKE` on half levels, mapped to a full level from its two bounding values.

DWD publishes `TKE` in `J/kg`, dimensionally equal to `m²/s²`; official GRIB
packages are available in the
[ICON-EU TKE directory](https://opendata.dwd.de/weather/nwp/icon-eu/grib/00/tke/).

At every full level, `|dθ/dz|` is obtained by a central difference over its
neighbours. Equation 12 of Cuevas et al. / Masciadri is then applied:

```text
Cn² = 3.35e-6
      * P^[2*(1 - 2R/cp)]
      * theta^(-10/3)
      * abs(d(theta)/dz)^(4/3)
      * TKE^(2/3)

R/cp = 0.286
```

Here `P` is in hPa, `theta` in K, `z` in m, and `TKE` in `m²/s²`. Nodes are
integrated trapezoidally from the model surface to the hourly `h_PBL`; the
first full level is extended through the small slab down to the surface.
HMNSP99 is integrated only above the same boundary, so there is no overlap:

```text
h_PBL   = clamp(ICON_MH, 500 m, 2000 m)
J_GL    = integral[0..h_PBL AGL](Cn² dz)
J_FA    = integral[above h_PBL AGL](Cn² dz)
J_total = J_GL + J_FA
```

The first server-side control calculation at the historical control case used
the former fixed 2 km boundary and demonstrates the scale of the missing
contribution:

| Lead | Hybrid seeing | Ground-layer share of `J` |
|---|---:|---:|
| `f042` | `2.221″` | `88.36%` |
| `f048` | `3.644″` | `94.43%` |

Both use `ground Cn² scale = 1.0`, with no fit to the outcome. These are two
diagnostic leads, not observational validation, but they directly explain why
the pressure-level `0.70…0.77″` series and many old `10` values were
implausibly optimistic.

After adding `MH`, the field was `396 m` at both control leads. An unbounded
396 m cutoff produced `1.9075″` at `f042` and `2.5158″` at `f048`; the
production rule `max(MH,500 m)` produced `1.9145″` and `2.5478″`,
respectively. These server-only values are a regression check of the dynamic
boundary. Full hourly production run `2026072106` was subsequently published
and verified through the platform-neutral live CLI path for the same saved point.

For context, long-term measurements at the Russian Shatdzhatmaz site found
median total seeing around `0.96″` and free-atmosphere seeing around `0.43″`,
so the ground layer often dominates the integral:
[Kornilov et al.](https://arxiv.org/abs/1403.6820). Those values cannot be
used as a site-specific calibration, but the PBL cannot be ignored either.

## 4. Seeing and coherence time

Both products are evaluated at `lambda = 500 nm` and at zenith. From the full
integral:

```text
r0      = [0.423 * (2*pi/lambda)² * J_total]^(-3/5)
seeing  = 0.98 * lambda / r0
```

The equivalent `seeing = 5.25*lambda^(-1/5)*J_total^(3/5)` expression returns
radians before conversion to arcseconds. The equations and additive integral
are given by [Cuevas et al. (2024)](https://academic.oup.com/mnras/article/529/3/2208/7617711).

Wind enters through atmospheric coherence time:

```text
M_wind = integral(Cn² * |V|^(5/3) dz)
tau0   = 0.058 * lambda^(6/5) * M_wind^(-3/5)
```

This is the standard wind-weighted turbulence integral; see
[Osborn et al. (2015)](https://academic.oup.com/mnras/article/451/3/3299/2907963).
It penalizes strong wind specifically where optical turbulence is present.

No separate full `direction delta` penalty is added. The identity

```text
|Delta V|² = V1² + V2² - 2*V1*V2*cos(Delta direction)
```

shows that HMNSP vector shear already contains wind rotation. Multiplying by
direction change again would double-count it. On the diagnostic chart,
direction delta remains `0°`, rather than missing, when
`min(speedA,speedB) < 2 m/s`: near-calm direction is unstable and has little
practical effect.

## 5. An auditable `1…10` mapping

This is an engineering mapping, not a new physical scale. The reference
seeing and `tau0` values come from official
[ESO observing-condition categories](https://www.eso.org/sci/observing/phase2/ObsConditions.CRIRES.html).
To avoid a broad plateau, seeing quality varies logarithmically:

```text
q_seeing = clamp(
  ln(seeing_bad / seeing) / ln(seeing_bad / seeing_best),
  0, 1)

seeing_best = 0.5 arcsec
seeing_bad  = 2.0 arcsec
```

Coherence time, where larger is better, uses the analogous mapping:

```text
q_tau = clamp(
  ln(tau0 / tau_bad) / ln(tau_best / tau_bad),
  0, 1)

tau_best = 5.2 ms
tau_bad  = 1.6 ms
q_coherence = 1 - 0.25 * (1 - q_tau)
```

`tau weight = 0.25` makes wind relevant while capping the additional
coherence-time penalty at `0…25%`. The factor may only lower seeing quality;
it cannot improve it or replace the main `Cn²` integral.

## 6. Effective ICON cloud obstruction

For cloud fraction `C` and a total-column or layer condensate path, visible
optical depth is estimated separately for liquid and ice:

```text
tau_phase = 3 * Qext * CWP / (4 * rho * r_eff)
B_cond    = C * (1 - exp(-tau/C))
```

The cloud-water-path to optical-depth form follows
[Gryspeerdt et al.](https://www.nature.com/articles/s41467-019-12982-0).
`B_cond` is the fraction of sky blocked by model-resolved condensate, not mere
cloud cover.

On native model levels, mixing ratio becomes condensate path using the actual
full-layer thickness between adjacent `HHL` boundaries, rather than a pressure
difference inferred between sparsely retained levels:

```text
dz_native = abs(HHL[k] - HHL[k+1])
m_air     = P_Pa / (Rd*T) * dz_native
CWP_phase = q_phase * m_air
Rd        = 287.05 J/(kg*K)
```

Consequently, `T` is downloaded at all 27 heatmap levels. Sparse sampling
aloft never assigns the mass of skipped native layers to a retained level.

To prevent diagnostic `CLC` with almost zero grid-scale `QC/QI` from becoming
“perfect transparency”, a tier-aware conservative guard is applied:

```text
g_low    = base       = 0.45
g_middle = 0.55*base = 0.2475
g_high   = 0.18*base = 0.081

B_effective,layer = max(B_cond, g_tier*C)
```

For the heatmap, tier follows the layer's actual AGL midpoint: low below 2 km,
middle from 2 to 7 km, and high from 7 km. The column calculation instead uses
the ready-made ICON `CLCL/CLCM/CLCH` tiers and overlaps their guards as:

```text
B_guard,column = min(C_cap,
                     1 - (1-B_low)*(1-B_middle)*(1-B_high))
C_cap = max(CLCT, CLCL, CLCM, CLCH)
```

The base `45%` and the `55%`/`18%` tier multipliers are conservative
engineering uncertainty factors, not physical opacity assigned to low,
middle, or high cloud. Resolved `QC/QI` physics always wins when stronger,
because the operation is `max(B_cond, guard)`.

The hourly **Effective ICON cloud obstruction** heatmap applies this optical
formula and tier-aware guard independently at each native model level using
`CLC/QC/QI/T/HHL`. Overall instead uses total-column `TQC/TQI`, total `CLCT`,
and `CLCL/CLCM/CLCH`; its three tier guards are combined with random
overlap and capped by diagnosed cover. Heatmap cells and Overall `q_cloud`
therefore **need not be numerically identical**. They share the optical-depth
kernel and unresolved-CLC guard policy, while the chart represents vertical
structure and Overall represents total-column transmission.

## 7. Fog, dew, and surface wind

Dew is excluded from Overall. It remains an operational prompt to prepare a
heater or dew shield and does not by itself worsen atmospheric seeing.

Fog physically blocks observations, so the multiplier follows the existing
`FogRisk`:

```text
q_fog = 1.00  # no risk
q_fog = 0.75  # possible fog
q_fog = 0.10  # high fog risk
```

Surface wind is a separate and deliberately mild practical factor because
screens, a low setup, and site choice can partly mitigate it. SAAO observations
show a weak relation between surface wind and seeing except in the highest
range near `>=8.6 m/s`: [Catala et al.](https://academic.oup.com/mnras/article/436/1/590/975197).
ESO's operational limits begin at `12 m/s`, with dome closure at `18 m/s`:
[ESO Paranal operations](https://www.eso.org/sci/facilities/paranal/sciops/At_Telescope.html).

Calibration uses smoothstep risks for mean wind over `8.5…15 m/s` and gusts
over `12…22 m/s`, takes the worse risk, and caps the penalty at 20%:

```text
r_surface = max(smoothstep(V10; 8.5, 15), smoothstep(gust; 12, 22))
q_surface = 1 - 0.20 * r_surface
```

## 8. Final composition and parameters

With the default `w_seeing=1` and `w_cloud=2`:

```text
normalized = q_seeing^w_seeing
             * q_coherence
             * q_cloud^w_cloud
             * q_surface
             * q_fog

Overall Astronomy Index = 1 + 9 * clamp(normalized, 0, 1)
```

The calculation does not round to an integer internally. An exact `10.0`
requires the best seeing boundary, best `tau0` boundary, no effective cloud
obstruction, no fog, and no surface-wind penalty at the same time.

Parameters live in `.env`, enter the algorithm/render-cache version, and must
change together with regression examples:

| Parameter | Default |
|---|---:|
| best / bad seeing | `0.5″ / 2.0″` |
| best / bad `tau0` | `5.2 / 1.6 ms` |
| `tau0` weight | `0.25` |
| ground `Cn²` scale | `1.0` |
| `MH` clamp AGL | `500…2000 m` |
| unresolved `CLC` guard: low / middle / high | `0.45 / 0.2475 / 0.081` |
| possible / high fog factor | `0.75 / 0.10` |
| surface-wind maximum penalty | `0.20` |
| mean-wind thresholds | `8.5 / 15 m/s` |
| gust thresholds | `12 / 22 m/s` |

## 9. Accuracy limits and future validation

The hybrid ICON result remains a model estimate. Its grid does not resolve
local terrain, vegetation, site heating, or dome seeing; both Masciadri/TKE
and HMNSP99 have systematic error. Even after local calibration, Cuevas et al.
found error around `0.30″` against Stereo-SCIDAR and a tendency to
underestimate seeing.

Therefore the old fixed-2-km values `2.221″/3.644″` and the new server-only
`MH=396 m`, clamp-500 values `1.9145″/2.5478″` demonstrate sensitivity to the
missing PBL but are not ground truth. Production run `2026072106` with
`surface-hourly-v17` and `cloud-hourly-v4` is synchronized and passed
post-deploy verification: 69 hours gave Overall `1.00…4.58`, no exact `10`,
and seeing `0.72…2.84″`. The next scientific
step is an hourly comparison against DIMM/MASS/SCIDAR or high-quality observing
logs around Saint Petersburg and Moscow. Until then, the
UI must say “model estimate” and must not call Overall a measured seeing value
or a Pickering scale.

## 10. Reproducibility

Record the following for every result used in analysis or publication:

1. source revision or release tag;
2. `config.yaml` without secrets and every `ASTRO_OVERALL_*` override;
3. ICON run ID and manifest schema versions;
4. coordinates, resolved IANA time zone, and forecast interval;
5. algorithm and renderer versions printed on the images.

Acquire the same upstream run, execute `sync-icon-eu`, and use `render-point`.
The numerical pipeline is deterministic for identical inputs and
configuration; file-generation timestamps and upstream availability are
operational metadata. Raw model data stay outside Git, but the run ID,
manifests, configuration, and source revision make the calculation traceable.

## 11. Validation protocol and responsible interpretation

- Unit tests cover parsing, units, grid selection, optical-depth kernels,
  turbulence integration, cache identity, and retention.
- Ephemeris regressions use public Saint Petersburg and Moscow examples; the
  Moscow Sun/Moon reference comes from the
  [USNO one-day service](https://aa.usno.navy.mil/data/api.html).
- Synthetic fixtures test rendering without entering the user-serving path.
- Production smoke tests require a complete manifest and all seven PNG
  products.
- Future observational datasets must record timestamp, instrument or method,
  uncertainty, site altitude, and weather context. Calibration and validation
  sites must be separated to avoid site-specific overfitting.

ICON-EU has finite grid spacing and is not a point measurement. Sub-grid
cloud, terrain, local heat sources, aerosol, dome seeing, and telescope setup
can dominate actual conditions. Planetary rise/set calculations are planning
approximations; demanding work should use a high-precision ephemeris. Bortle
output is a modeled zenith-brightness reference, not a visual site
classification. The `1…10` indices rank modeled conditions; they are not the
Pickering scale and must not be reported as observed seeing.

Public examples contain only Saint Petersburg and Moscow. User identifiers,
saved locations, tokens, and production credentials are runtime data and must
never enter research artifacts or issue reports.

## 12. Data-source selection and server verification

Status: original Stage 0 spike updated with the current data contract;
`surface-hourly-v17`/`cloud-hourly-v4` is published in production
Initial measurement date: 2026-07-19; updated 2026-07-22
Host: the production host
Runtime directory: `/opt/docker/bot_astrosferum/data/verification`

### 12.1. Conclusion

The initial production policy is technically viable:

- use ICON-EU directly for Saint Petersburg, Moscow, and other points inside its actual product domain;
- retain ICON Global as the worldwide fallback, with mandatory native-grid geometry for one-point extraction from `unstructured_grid`;
- keep ICON-Ru WIS 2.0 only as a candidate for a future shadow-verification adapter: none is implemented, its public field set cannot drive the complete seeing pipeline, and delivery is notification-based MQTT;
- no model file exists on the local workstation. Every downloaded artifact remains on the production host below the project directory.

This spike proves availability and technical processing, not comparative forecast accuracy. Accuracy is established separately against observations.

### 12.2. ICON-EU

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
| `PMSL`, `PS` | `prmsl`, `sp` | Pa | pressure |
| `TOT_PREC` | `tp` | kg/m² | accumulated precipitation |
| `CLCL`, `CLCM`, `CLCH`, `CLCT` | same | % | cloud layers and total cloud |
| `T_G` | `T_G` | K | ground temperature |
| `U`, `V`, `T`, `P`, `QV` | `u`, `v`, `t`, `pres`, `q` | m/s, K, Pa, kg/kg | full model-level profile |
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
`P_Pa/(287.05·T)·abs(HHL[k]−HHL[k+1])`. Sparse upper-level sampling therefore
does not assign the mass of skipped layers to a retained cell.

#### PBL and cloud refinement after the control-case audit

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
the previous directory removed. The enriched point cache uses
`point-v6-native-cloud-mass-mh`, preventing old `gob.gz` entries without
native thickness or `MH` from being interpreted as current data.

`TOT_PREC` accumulates from model initialization, so user-facing `mm/h` values are non-negative differences between adjacent hourly forecast times. The response uses a rolling window of up to 72 future hours without interpolating or inventing unavailable times. The `T−Td` spread is only used to advise about possible dew and equipment protection; dew does not penalize seeing or practical time ranking.

### 12.3. ICON Global

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
consecutive lower levels `104…120` with `U/V`, half levels `104…121` with
`TKE`, and the bounding `HHL`. These Global indices are the physical-height
counterparts of ICON-EU levels `25,30,35,40,45,48,50,52,54,56,58…74`; a live
HHL comparison over the shared domain found the mapping to be the constant
index offset `+46` (for example, both HHL 58/104 are about 2.41 km and HHL
74/120 about 30 m at the checked node). The shared extractor and equations
therefore produce the same cloud-obstruction heatmap and hybrid TKE/HMNSP99
Overall method on either provider. Official DWD publishes the required
[CLC](https://opendata.dwd.de/weather/nwp/icon/grib/00/clc/),
[QC](https://opendata.dwd.de/weather/nwp/icon/grib/00/qc/),
[QI](https://opendata.dwd.de/weather/nwp/icon/grib/00/qi/),
[TKE](https://opendata.dwd.de/weather/nwp/icon/grib/00/tke/), and
[HHL](https://opendata.dwd.de/weather/nwp/icon/grib/00/hhl/) native products.
DWD Global TKE ends at `+48 h`, although the other selected model-level fields
remain hourly through `+78 h`. The first 49 bundles therefore contain 187
messages and the last 30 contain 169. Cloud obstruction remains complete; the
hybrid Overall sequence stops at `+48 h` rather than extrapolating TKE or
silently reverting to the known-optimistic free-atmosphere estimate. Global
still has no public `VIS` in this feed, so fog is not inferred from a
missing value and the transparency proxy is marked unavailable. CDO is
installed only in the application image; the host requires no meteorological
packages.

### 12.4. ICON-Ru WIS 2.0

On 2026-07-19, discovery metadata are available from the DWD Global Discovery Catalogue and directly over HTTP from `wis2box.mecom.ru`. They confirm:

- public `ICON-Ru13/6N29` output at `0.25° × 0.25°`;
- `00/12 UTC`, three-hourly through `+72 h`, with more frequent precipitation through `+48 h`;
- origin topic:
  `origin/a/wis2/ru-roshydromet/data/core/weather/prediction/forecast/short-range/deterministic/limited-area`;
- origin broker `mqtt://<public-access>@wis2box.mecom.ru:1883`;
- Global Broker cache topics use the same suffix below `cache/a/wis2/ru-roshydromet/...`.

Observed limitation: HTTPS on `wis2box.mecom.ru:443` timed out from the production host after eight seconds, while the HTTP endpoint returned `200`. The advertised origin metadata and MQTT URI are not TLS-protected. The shadow adapter should therefore prefer a TLS WIS 2.0 Global Broker from the discovery catalogue instead of the origin endpoint; the public URI credentials are not project secrets.

Metadata files remain server-only under `/opt/docker/bot_astrosferum/data/verification/icon-ru-wis-spike/`. Object naming, message sizes, and redelivery semantics still require capturing a real broker notification after a run is published.

### 12.5. Implementation decisions

1. `iconeu.Sync` downloads only selected fields and levels, decompresses, validates, and merges messages per forecast step.
2. `iconglobal.Sync` follows the same atomic lifecycle while retaining full native-grid bundles; its point store applies official DWD grid geometry through CDO.
3. The coverage router selects ICON-EU inside its domain and ICON Global elsewhere; both normalize their outputs to the same forecast types before rendering.
4. `iconruwis` is not implemented; the WIS spike remains a candidate for a future isolated shadow adapter and cannot affect a user response.
5. Raw `.bz2`, runs, cache, verification, and temporary files live only in `/opt/docker/bot_astrosferum/data` and are excluded from Git and the Docker build context.
6. A complete 72-hour run is never downloaded locally and is not started on the server without a free-space check.


### 12.6. Remaining checks

- capture one real ICON-Ru WIS notification and record object URL, name, size, and redelivery semantics;
- perform observational calibration against DIMM/MASS/SCIDAR or high-quality logs; server-side checks validate the calculation, not forecast accuracy.
