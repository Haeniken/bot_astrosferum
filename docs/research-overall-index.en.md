# Overall Astronomy Index re-evaluation

Status: calculation note, 21 July 2026. This document records sources, units,
control calculations, and configurable engineering decisions. It does not
claim that a universal scientific `1…10` sky-quality scale exists. Overall
Astronomy Index is an auditable mapping of several physical and practical
factors to a convenient range.

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
Cn²   = 2.8 * L0^(4/3) * M²
```

`P` is in hPa, `T` in K, `z` in m, and `S` in s⁻¹. Coefficients and units
follow the published
[HMNSP99 assessment](https://academic.oup.com/mnras/article/503/4/5692/6225358).
Vector shear `hypot(du,dv)` already includes changes in both wind speed and
direction.

HMNSP99 is useful aloft but does not replace a PBL model. The profiler
comparison in [Curé et al. (2024)](https://academic.oup.com/mnras/article/529/3/2208/7617711)
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
neighbours. Equation 12 of Curé et al. / Masciadri is then applied:

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
and verified through the live Telegram CLI path for the same saved point.

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
are given by [Curé et al. (2024)](https://academic.oup.com/mnras/article/529/3/2208/7617711).

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
and `CLCL/CLCM/CLCH`; its three tier guards are combined with maximum-random
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
and HMNSP99 have systematic error. Even after local calibration, Curé et al.
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
