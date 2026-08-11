# Scientific method and Overall Astronomy Index

**Author:** Sergey Borzenkov\
**ORCID:** [0009-0005-5804-5011](https://orcid.org/0009-0005-5804-5011)\
**Project:** Astrosferum\
**Document type:** Research-software methodology and calculation note\
**Version:** 2.0\
**Revision date:** 9 August 2026

Status: research-software method and calculation note, revised 9 August 2026.
This document is the canonical description of sources, units, formulas,
control calculations, validation, uncertainty, and configurable engineering
decisions in `bot-astrosferum`.

The directional Horizon method described in section 7 is an implemented
user-facing capability. It is enabled by configuration, is deliberately
limited to ICON-EU, and follows the same reproducibility and current-run
requirements as the ordinary forecast.

The directional atmospheric Astrodome method in section 8 is implemented for
ICON-EU in this repository. Its presentation and saved-visualization catalogue
are deployed from the independent private `site-astrosferum` repository.
Production-v2/v29/v23
is the current writer; the complete v28/v22 numerical measurement recorded below
is an explicitly historical baseline;
multi-cycle resource, release, and observational validation remain separate
gates and do not change the formula contract.

## Document map

- Sections 1–2 define scientific status, outputs, and provenance.
- Section 3 is the canonical formula ledger for the ordinary point forecast;
  sections 7 and 8 add the numbered directional equations with their research
  or project provenance.
- Section 4 connects the canonical equations to implementation details,
  calibration parameters, and control calculations.
- Section 5 defines reproducibility, validation, and responsible use.
- Section 6 records the measured data-source contracts.
- Section 7 specifies the optional directional Horizon method.
- Section 8 specifies the directional atmospheric Astrodome geometry, primitive
  reconstruction, refraction, line-of-sight physics, numerical method, and
  limitations.

## 1. Scope and scientific status

`bot-astrosferum` is research-oriented scientific software. It acquires
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

## 2. Outputs and data provenance

### 2.1. Research question and outputs

The practical question is: how suitable is a coordinate and hour for
astronomical observing, given modeled turbulence, wind, cloud obstruction,
precipitation, fog, daylight, atmospheric composition, and astronomical
events?

The answer remains decomposed into auditable outputs:

- meteorological conditions and tiered cloud cover;
- effective cloud obstruction by native model level;
- wind speed, vector shear, and direction-change diagnostics;
- wind-derived and combined astronomy-condition indices;
- the retained `Cn2` profile diagnostics: `theta0`, effective turbulence
  height and wind, fixed-height `FracGL`, and free-atmosphere seeing;
- a separate Johnson-V zenith efficiency reference for a declared
  background-limited, seeing-limited point-source observation;
- optional ICON-EU directional conditions at the 10-degree Horizon reference;
- Sun, Moon, Jupiter, and Saturn planning events;
- point light-pollution context, reported separately from the hourly index.

### 2.2. Data provenance

Every forecast image identifies the provider, product, model run, grid, time
zone, algorithm version, and renderer version. The primary weather source is
DWD ICON-EU open data, with ICON Global for coordinates outside the EU
domain. The independent Reference-V diagnostic additionally records its NASA
GEOS-CF composition run because it must never be implied to share the ICON run
identity. Light-pollution context uses an operator-pinned annual Light
Pollution Atlas and a separate World Atlas 2015 comparison.

Downloaded runs and atlases are absent from Git because they are large,
replaceable upstream artifacts. Runtime manifests and versioned cache keys
prevent products generated from incompatible schemas from being mixed.
Architecture and runtime status are recorded in the
[KISS architecture](architecture.en.md) and
[implementation status](implementation-status.en.md). Data-source selection,
measured contracts, and acquisition verification are consolidated in
[section 6](#6-data-source-contracts-and-server-verification) below.

## 3. Canonical formula ledger and research provenance

This is the complete calculation chain used by the current implementation.
Equations marked **published** are transcribed from the linked research;
equations marked **project rule** are explicit, configurable engineering
choices made by `bot-astrosferum`. The latter must not be presented as
peer-reviewed physical laws.

### 3.1. Physical optical-turbulence model

For every model layer, potential temperature and vector wind shear are

```math
\begin{aligned}
\theta &= T\left(\frac{1000}{P}\right)^{R/c_p},
&\qquad \frac{R}{c_p}=0.286,\\
S &= \sqrt{\left(\frac{du}{dz}\right)^2+\left(\frac{dv}{dz}\right)^2}.
\end{aligned}\tag{F1}
```

Above the planetary boundary layer, HMNSP99 is evaluated exactly as

```math
\begin{aligned}
M &= -7.9\times10^{-5}\frac{P}{T^2}\frac{d\theta}{dz},\\
L_0^{4/3} &= 0.1^{4/3}\,10^Y,\\
Y &=
\begin{cases}
0.362+16.728S-192.347\,\dfrac{dT}{dz}, & \text{troposphere},\\
0.757+13.819S-57.784\,\dfrac{dT}{dz}, & \text{stratosphere},
\end{cases}\\
C_{n,\mathrm{FA}}^2 &= 2.8\,L_0^{4/3}M^2.
\end{aligned}\tag{F2}
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

```math
C_{n,\mathrm{GL}}^2
=3.35\times10^{-6}
P^{\,2(1-2R/c_p)}
\theta^{-10/3}
\left|\frac{d\theta}{dz}\right|^{4/3}
\mathrm{TKE}^{2/3}.
\tag{F3}
```

**Published basis:** equation (12) of
[Cuevas et al. (2024)](https://academic.oup.com/mnras/article/529/3/2208/7617711),
following the PBL parametrization of Masciadri & Jabouille (2001). Using
ICON's prognostic TKE in this published relation is the model adaptation made
here. The paper prints `(d theta/dz)^(4/3)`; taking the absolute derivative is
an explicit numerical adaptation that keeps the fractional power real when a
model layer has a negative gradient. `ground_Cn2_scale=1.0` applies no
empirical fit by default.

The two non-overlapping regions are integrated and converted to seeing:

```math
\begin{aligned}
h_{\mathrm{PBL}}
&=\mathrm{clamp}(\mathrm{ICON\_MH},500\,\mathrm{m},2000\,\mathrm{m})\ \mathrm{AGL},\\
J_{\mathrm{GL}}&=\int_{\mathrm{surface}}^{h_{\mathrm{PBL}}}C_{n,\mathrm{GL}}^2\,dz,\\
J_{\mathrm{FA}}&=\int_{h_{\mathrm{PBL}}}^{\mathrm{model\ top}}C_{n,\mathrm{FA}}^2\,dz,\\
J&=J_{\mathrm{GL}}+J_{\mathrm{FA}},\\[2pt]
r_0&=\left[0.423\left(\frac{2\pi}{\lambda}\right)^2J\right]^{-3/5},\\
\varepsilon_{\mathrm{rad}}
&=0.98\frac{\lambda}{r_0}
=C_\varepsilon\lambda^{-1/5}J^{3/5},\\
C_\varepsilon
&=0.98\left[0.423(2\pi)^2\right]^{3/5}
=5.306963958\ldots,\\
\varepsilon_{\mathrm{arcsec}}
&=206264.806247\,\varepsilon_{\mathrm{rad}}.
\end{aligned}\tag{F4}
```

`lambda=500e-9 m`. The `r0` and `0.98 lambda/r0` equations are equations
(13) and (14)
of [Cuevas et al. (2024)](https://academic.oup.com/mnras/article/529/3/2208/7617711);
the underlying Fried parameter is defined in D. L. Fried, “Optical Resolution
Through a Randomly Inhomogeneous Medium for Very Long and Very Short
Exposures,” *JOSA* **56**(10), 1372–1379 (1966),
[DOI 10.1364/JOSA.56.001372](https://doi.org/10.1364/JOSA.56.001372).
The implementation evaluates these two base definitions directly. Their
algebraically consistent coefficient is `5.306963958…`.
The `500..2000 m` clamp is a configurable **project rule**, not a published
universal PBL boundary.

The ordinary Overall calculation uses the ICON cell's HHL surface elevation as
the zero of AGL height. It therefore affects native cloud height, the placement
of `h_PBL`, and the ground/free-atmosphere split in [F4]. There is no separate
elevation multiplier: adding one would count the same model geometry twice.
HHL is a coarse model-cell surface, not a local DEM or an obstacle/skyline
survey.

Wind affects the physical result twice, but through two different moments:
vector shear is already in HMNSP99 `Cn2`, while absolute wind speed determines
the atmospheric coherence time:

```math
\begin{aligned}
J_V&=\int C_n^2(z)\,\lvert V(z)\rvert^{5/3}\,dz,\\
k&=\frac{2\pi}{\lambda},\\
D_\phi(t)&=2.910\,k^2J_Vt^{5/3},\\
D_\phi(\tau_0)&=1,\\
\tau_0&=\left(2.910\,k^2J_V\right)^{-3/5}
=C_\tau\lambda^{6/5}J_V^{-3/5},\\
C_\tau&=\left[2.910(2\pi)^2\right]^{-3/5}
=0.058056167701097\ldots.
\end{aligned}\tag{F5}
```

**Published basis:** equations (4)–(5) of
[Kellerer & Tokovinin (2007)](https://www.aanda.org/articles/aa/pdf/2007/02/aa5788-06.pdf)
give the temporal phase structure function and effective-wind definition.
[Qian et al. (2021)](https://academic.oup.com/mnras/article/505/1/582/6273151)
publish the equivalent expanded convention rounded to three decimal places,
while
[Aristidi et al. (2020)](https://academic.oup.com/mnras/article/496/4/4822/5863963)
use the effective-wind form. The implementation evaluates the phase-structure
definition directly; `C_tau` above is shown only as its algebraic control value.
Here `2.910` is the source-published base coefficient; the implementation does
not introduce another rounding step. In particular, it does not substitute the
three-decimal shorthand `0.058`: relative to the same base coefficient, its
algebraically expanded value is `0.058056167701097...`. Consequently, using the
shorthand would make `tau0` about `0.0968%` smaller. The expanded value is kept
only as a regression check, while production code evaluates the first form in
[F5]. This choice does not claim numerical precision beyond the
source-reported `2.910`.
No separate direction-change penalty is multiplied into Overall, because it
would count the vector-shear contribution again.

For each retained interval the wind-weighted moment is integrated by applying
the `5/3` power at the endpoints before the trapezoid:

```math
J_{V,i}\approx\frac{\Delta z_i}{2}
\left[C_{n,i}^2\lvert V_i\rvert^{5/3}
+C_{n,i+1}^2\lvert V_{i+1}\rvert^{5/3}\right].\tag{F5e}
```

It is not evaluated as `Cn2` times the `5/3` power of an interval-mean wind.
Since `x^(5/3)` is convex for non-negative wind speed, applying the nonlinear
power after averaging can bias the wind moment low by Jensen's inequality,
especially across a strong speed gradient. Equation [F5e] also makes the
discretization consistent with the retained endpoint integrand used by
`tau0` and `V_eff`; it is a trapezoidal numerical rule, not a claim that the
unresolved within-layer wind is linear in its `5/3` power.

#### One retained profile and the height-sensitive diagnostics

The implementation does not reconstruct a second, differently sampled
turbulence column for adaptive-optics diagnostics. The native TKE boundary
layer and the HMNSP99 free atmosphere are joined once into an ordered,
non-overlapping, piecewise-linear profile. For a retained interval
`[z_i,z_(i+1)]`, the interpolant and its ordinary integral are

```math
\begin{aligned}
C_n^2(z)
&=C_{n,i}^2+
\frac{z-z_i}{z_{i+1}-z_i}
\left(C_{n,i+1}^2-C_{n,i}^2\right),\\
\int_{z_i}^{z_{i+1}}C_n^2(z)\,dz
&=\frac{C_{n,i}^2+C_{n,i+1}^2}{2}
\left(z_{i+1}-z_i\right).
\end{aligned}\tag{F5a}
```

Every reported moment below is evaluated from that same retained profile;
therefore seeing, `tau0`, `theta0`, effective height/wind, and ground-layer
fractions cannot silently disagree because of different vertical masks:

```math
\begin{aligned}
J&=\int_0^{z_{\mathrm{top}}}C_n^2(h)\,dh,\\
J_V&=\int_0^{z_{\mathrm{top}}}C_n^2(h)\lvert V(h)\rvert^{5/3}\,dh,\\
J_h&=\int_0^{z_{\mathrm{top}}}C_n^2(h)h^{5/3}\,dh,\\
V_{\mathrm{eff}}&=\left(\frac{J_V}{J}\right)^{3/5},
&h_{\mathrm{eff}}&=\left(\frac{J_h}{J}\right)^{3/5},\\
\theta_{0,\mathrm{rad}}
&=\left[2.914\left(\frac{2\pi}{\lambda}\right)^2J_h\right]^{-3/5},\\
\mathrm{FracGL}(H)
&=\frac{\int_0^H C_n^2(h)\,dh}{J},
&H&\in\{250,500,1000\}\ \mathrm{m},\\
J_{\mathrm{FA},500}
&=\int_{500\,\mathrm{m}}^{z_{\mathrm{top}}}C_n^2(h)\,dh,\\
\varepsilon_{\mathrm{FA},500}
&=0.98\frac{\lambda}{
\left[0.423(2\pi/\lambda)^2J_{\mathrm{FA},500}\right]^{-3/5}}.
\end{aligned}\tag{F5b}
```

Here `h` is geometric height above the ICON model-cell surface, `theta0` is
converted from radians to arcseconds for output, and all wavelength-dependent
diagnostics in [F5b] use `lambda=500 nm` at zenith. The `2.914` anisoplanatic
phase-structure coefficient is deliberately not replaced by the temporal
coefficient `2.910` in [F5]. The standard height- and wind-weighted moments are
described by
[Roddier (1981)](https://doi.org/10.1016/S0079-6638(08)70204-X) and the
operational meaning of `theta0`, `tau0`, and ground/free-atmosphere seeing is
documented for combined MASS-DIMM instruments by
[Kornilov et al. (2007)](https://doi.org/10.1111/j.1365-2966.2007.12467.x).
The fixed-height fractions are model analogues of these monitoring products;
they are not claimed to be measurements by MASS.

`BoundaryLayerFraction` uses the hourly dynamic `h_PBL`, whereas the exported
`FracGL250`, `FracGL500`, and `FracGL1000` use the fixed cutoffs in [F5b]. The
legacy field named `GroundLayerFraction` is only a compatibility alias for the
dynamic boundary-layer fraction and must not be cited as MASS `FracGL`.

Profile completeness is structural, not probabilistic forecast confidence:

```math
\begin{aligned}
C_z&=\frac{\sum_{i\in\mathrm{valid}}\Delta z_i}{z_{\mathrm{top}}},\\
C_h&=\frac{\sum_{i\in\mathrm{valid}}
\int_{z_i}^{z_{i+1}}h^{5/3}\,dh}
{\int_0^{z_{\mathrm{top}}}h^{5/3}\,dh},\\
\mathrm{profile\ quality}=\mathrm{complete}
&\iff C_z\ge0.99\ \land\ C_h\ge0.99\ \land\\
z_{\mathrm{top}}\ge18\ \mathrm{km\ AGL},\\
\mathrm{Overall\ profile\ gate}=\mathrm{pass}
&\iff C_z\ge0.90\ \land\ C_h\ge0.90\ \land\\
z_{\mathrm{top}}\ge15\ \mathrm{km\ AGL}.
\end{aligned}\tag{F5c}
```

Otherwise a valid integrated profile is marked `limited`; an invalid profile
is `unavailable`. `C_h` exposes the stronger effect of upper-level gaps on
`theta0`. Overall rejects an hour below either `90%` structural threshold or
with a model/profile top below `15 km AGL`.
An hour that passes that safety gate but whose `ProfileQuality` is anything
other than `complete` retains its physical result and is marked partial with
`!`; a fully covered profile reaching `15…18 km AGL` is therefore usable only
as partial. Neither the
quality label nor `!` is a probability that the forecast will verify.

#### Conditioning zenith seeing on an actual target

The stored seeing is the atmospheric long-exposure FWHM at zenith and
`500 nm`. When a target elevation `a` and observing wavelength `lambda_obs`
are explicitly supplied, the atmospheric component is transformed as

```math
\begin{aligned}
X(a)&=\left[
\sin a+0.50572\left(a+6.07995^\circ\right)^{-1.6364}
\right]^{-1},\\
\varepsilon_{\mathrm{atm}}(a,\lambda_{\mathrm{obs}})
&=\varepsilon_{500,\mathrm{zen}}
X(a)^{3/5}
\left(\frac{\lambda_{\mathrm{obs}}}{500\,\mathrm{nm}}\right)^{-1/5},\\
\mathrm{FWHM}_{\mathrm{delivered}}
&=\sqrt{\varepsilon_{\mathrm{atm}}^2+
\mathrm{FWHM}_{\mathrm{non-atm}}^2}.
\end{aligned}\tag{F5d}
```

The relative optical-air-mass approximation is from
[Kasten & Young (1989)](https://doi.org/10.1364/AO.28.004735); the
`X^(3/5) lambda^(-1/5)` atmospheric-FWHM scaling is the same convention used
by the
[ESO exposure-time calculators](https://www.eso.org/observing/etc/doc/helpsphere.html).
The provider-neutral implementation exposes these operations as
`KastenYoungRelativeOpticalAirmass`, `ScaleZenithSeeingFWHMArcsec`, and
`DeliveredImageQualityFWHMArcsec`. The last line is exact only for convolution
of independent circular Gaussian PSFs. It must not collapse an adaptive-optics
core/halo PSF, an Airy pattern, asymmetric tracking, or field-dependent
aberration into one Gaussian width.

This transformation is a target-conditioned diagnostic. It is not inserted
into the coordinate-only Overall score, because that score has no target
altitude, passband, telescope diameter, outer scale, or instrument transfer
function. Equation [F5d] is therefore atmospheric FWHM, not a claim of full
delivered detector image quality unless a defensible non-atmospheric Gaussian
term is explicitly supplied; other regimes require the actual PSF or an
encircled-energy model.

### 3.2. Cloud obstruction

For liquid and ice separately, the condensate mass path and optical depth are

```math
\begin{aligned}
\rho_{\mathrm{air}}&=\frac{P_{\mathrm{Pa}}}{R_dT},
&R_d&=287.05\ \mathrm{J\,kg^{-1}\,K^{-1}},\\
\mathrm{CWP}_{\mathrm{phase}}
&=q_{\mathrm{phase}}\rho_{\mathrm{air}}\,\Delta z_{\mathrm{native}},\\
\tau_{\mathrm{phase}}
&=\frac{3Q_{\mathrm{ext,phase}}\mathrm{CWP}_{\mathrm{phase}}}
{4\rho_{\mathrm{phase}}r_{\mathrm{eff,phase}}},\\
\tau&=\tau_{\mathrm{liquid}}+\tau_{\mathrm{ice}}.
\end{aligned}\tag{F6}
```

Defaults are `Qext_liquid=2.0`, `Qext_ice=2.1`,
`rho_liquid=1000 kg/m3`, `rho_ice=916.7 kg/m3`,
`r_eff_liquid=10 um`, and `r_eff_ice=25 um`. For `Qext=2`, the
optical-depth relation reduces to

```math
\tau=\frac{3\,\mathrm{LWP}}{2\rho_{\mathrm{water}}r_{\mathrm{eff}}}.
```

This matches equation (19) of
[Lowe et al. (2019)](https://www.nature.com/articles/s41467-019-12982-0).
That source supports the liquid-water form only. The ice term, fixed
effective radii, and ice extinction efficiency are **project
assumptions**, required because public one-moment ICON fields contain mass but
not particle number or effective radius.
The density `P/(Rd*T)` is the dry-air approximation; without model `DEN` or
specific humidity on the retained cloud levels, moist-air density is not
reconstructed. This small approximation is disclosed rather than hidden.

ICON condensate is a grid-box mean. For diagnosed cloudy fraction `C`, the
code first defines the nonsingular effective fraction `C_hat` below, then
treats `tau/C_hat` as in-cloud optical depth and applies Beer-Lambert
extinction:

```math
\begin{aligned}
\widehat C&=
\begin{cases}
C, & C\ge10^{-6},\\
0, & C<10^{-6}\ \land\ \tau<10^{-9},\\
\min\!\left[1,\max\!\left(0.01,1-e^{-\tau}\right)\right], & \text{otherwise},
\end{cases}\\
q_{\mathrm{cloud}}=T_{\mathrm{all\ sky}}
&=
\begin{cases}
1, & \widehat C=0,\\
(1-\widehat C)+\widehat C\exp\!\left(-\dfrac{\tau}{\widehat C}\right),
& \widehat C>0,
\end{cases}\\
B_{\mathrm{cond}}&=1-q_{\mathrm{cloud}}.
\end{aligned}\tag{F7}
```

The clear/cloudy mixture and the bounded inference used only when rounded
`C=0` coexists with condensate are **project derivations** from the published
optical-depth relation, not equations claimed by Lowe et al. A diagnosed-cloud
uncertainty guard is then applied when public `QC/QI` or `TQC/TQI` do not
represent diagnostic `CLC`:

```math
\begin{aligned}
g_{\mathrm{low}}&=0.45,
&g_{\mathrm{middle}}&=0.55\,g_{\mathrm{low}},
&g_{\mathrm{high}}&=0.18\,g_{\mathrm{low}},\\
B_{\mathrm{tier}}&=g_{\mathrm{tier}}C_{\mathrm{tier}},\\
C_{\mathrm{cap}}&=\max(\mathrm{CLCT},\mathrm{CLCL},\mathrm{CLCM},\mathrm{CLCH}),\\
B_{\mathrm{guard}}
&=\min\!\left(C_{\mathrm{cap}},
1-(1-B_{\mathrm{low}})(1-B_{\mathrm{middle}})(1-B_{\mathrm{high}})\right),\\
q_{\mathrm{cloud}}&=\min(q_{\mathrm{cloud}},1-B_{\mathrm{guard}}).
\end{aligned}\tag{F8}
```

The product is the random-overlap expression for the three already aggregated
tiers; random and maximum overlap limits are set out explicitly by
[Pincus et al. (2005)](https://agupubs.onlinelibrary.wiley.com/doi/10.1029/2004JD005100).
The tier factors and choosing random overlap here are conservative **project
rules**; they are not fitted cloud optical properties. The formula is not
called maximum-random overlap because adjacent native layers are not being
grouped into maximum-overlap blocks in this calculation.

### 3.3. Engineering mapping to Overall `1..10`

The physical outputs are mapped without rounding:

```math
\begin{aligned}
q_{\mathrm{seeing}}
&=\mathrm{clamp}\!\left(
\frac{\ln(\varepsilon_{\mathrm{bad}}/\varepsilon)}
{\ln(\varepsilon_{\mathrm{bad}}/\varepsilon_{\mathrm{best}})},0,1\right),\\
q_{\tau}
&=\mathrm{clamp}\!\left(
\frac{\ln(\tau_0/\tau_{\mathrm{bad}})}
{\ln(\tau_{\mathrm{best}}/\tau_{\mathrm{bad}})},0,1\right),\\
q_{\mathrm{coherence}}&=1-w_{\tau}(1-q_{\tau}),\\
q_{\mathrm{turbulence}}
&=q_{\mathrm{seeing}}^{w_{\mathrm{seeing}}}q_{\mathrm{coherence}},\\
f_{\mathrm{turbulence}}
&=(1-p_{\mathrm{turbulence}})
+p_{\mathrm{turbulence}}q_{\mathrm{turbulence}}.
\end{aligned}\tag{F9}
```

```math
\begin{aligned}
t&=\mathrm{clamp}\!\left(\frac{x-a}{b-a},0,1\right),\\
\mathrm{smoothstep}(x;a,b)&=t^2(3-2t),\\
r_{\mathrm{surface}}
&=\max\!\left[\mathrm{smoothstep}(V_{10};8.5,15),
\mathrm{smoothstep}(V_{\mathrm{gust}};12,22)\right],\\
q_{\mathrm{surface}}&=1-0.20\,r_{\mathrm{surface}}.
\end{aligned}\tag{F10}
```

```math
q_{\mathrm{fog}}=
\begin{cases}
0.10, & \mathrm{VIS}<1\,\mathrm{km},\ \mathrm{RH}\ge95\%,\ T-T_d\le1.5^\circ\mathrm{C},\\
0.75, & \mathrm{VIS}<5\,\mathrm{km},\ \mathrm{RH}\ge90\%,\ T-T_d\le2.5^\circ\mathrm{C},\\
1.00, & \text{otherwise}.
\end{cases}\tag{F11}
```

```math
q_{\mathrm{precip}}=[R_{1\mathrm{h}}<R_{\mathrm{detect}}],
\qquad R_{\mathrm{detect}}=0.05\ \mathrm{mm}.
```

```math
Q=f_{\mathrm{turbulence}}q_{\mathrm{cloud}}^{w_{\mathrm{cloud}}}
q_{\mathrm{surface}}q_{\mathrm{fog}}q_{\mathrm{precip}}.
```

```math
\mathrm{Overall}=1+9\,\mathrm{clamp}(Q,0,1).\tag{F12}
```

Here `[A]` is the Iverson bracket: it equals `1` when condition `A` is true
and `0` otherwise.

Defaults are `epsilon_best=0.5 arcsec`, `epsilon_bad=2.0 arcsec`,
`tau_best=5.2 ms`, `tau_bad=1.6 ms`, `w_tau=0.25`, `w_seeing=1`,
`w_cloud=2`, and `p_turbulence=0.25`.
[ESO observing-condition categories](https://www.eso.org/sci/observing/phase2/ObsConditions.CRIRES.html)
provide observational context for [F9], including the `0.5 arcsec` image-quality
and `5.2/1.6 ms` coherence-time values. The `2.0 arcsec` saturation endpoint and
their use in a continuous utility curve are project choices, not an ESO
good/bad classification. [F10] is intentionally mild because the
site measurements of
[Catala et al. (2013)](https://academic.oup.com/mnras/article/436/1/590/975197)
found only a weak surface-wind/seeing relation except at high wind, while
[CRIRES visitor instructions](https://www.eso.org/sci/facilities/paranal/instruments/crires/visitor.html)
and the official
[VLT environmental specifications](https://www.eso.org/sci/facilities/paranal/telescopes/ut/envspecs.html)
provide operational context: pointing into wind is restricted above `12 m/s`,
while VLT full-performance limits distinguish wind orientation and gusts.
The exact `8.5/15` and `12/22 m/s` endpoints in [F10] remain project choices. The
[WMO International Cloud Atlas](https://cloudatlas.wmo.int/fog-compared-with-mist.html)
defines fog by horizontal visibility below 1 km and supports the high-risk
visibility boundary in [F11]. However, equations [F9]-[F12], all thresholds,
weights, fog factors, smoothstep, and the final `1..10` transform are
**project rules**. The added humidity and dew-point conditions prevent
precipitation or dry haze from being mislabeled as fog.

`R_1h` is the interval precipitation attributed to the hourly frame after the
model accumulation has been differenced. The `0.05 mm` boundary is a
configurable **detection threshold**, not a calibrated dose-response curve.
At or above it, precipitation is an operational veto: exposed optics and
electronics should not be operated, regardless of whether the amount is
light or heavy. The physical seeing, cloud, and wind diagnostics remain
reported, but Overall is exactly `1.0`. This rule intentionally avoids
inventing a smooth precipitation utility before an ensemble probability and
equipment-specific protection model exist.

Dew risk, daylight, Bortle class, and planetary events do **not** enter [F12].
Day/night is only visual shading on the hourly Overall chart.

The bounded `f_turbulence` is a convex mixture between target detectability
and fine-resolution performance. Seeing is the FWHM of the atmospheric
point-spread function: it redistributes
point-source light and changes angular resolution and the signal-to-noise
reference area, but it is not extinction. ESO accordingly treats image
quality/turbulence and sky transparency as separate observing constraints and
evaluates them against each science programme rather than declaring one
universal threshold
([ESO ETC definitions](https://www.eso.org/observing/etc/doc/helpuves.html),
[ESO QC0 constraints](https://www.eso.org/sci/facilities/paranal/quality-control/qc0-ob-grading.html)).
Equation [F9] interprets `p_turbulence` as the fraction of a
target-agnostic utility assigned to fine-resolution performance; the remaining
fraction represents observing modes in which the target remains detectable
although fine detail is degraded.

No published universal value of `p_turbulence` exists. Its default `0.25` is a
versioned, configurable **project prior**, chosen by the explicit ordering
requirement that cloud obstruction dominate a general-purpose score. With the
defaults, the exact consequences are

```math
\begin{aligned}
0.75&\le f_{\mathrm{turbulence}}\le1,\\
1-\sqrt{0.75}&=0.133975\ldots,\\
(1-0.20)^2&=0.64,\\
7.75&\le\mathrm{Overall}_{\mathrm{turbulence\ only}}\le10.
\end{aligned}
```

Thus turbulence alone removes at most 25% of normalized utility, effective
obstruction above about 13.4% has a stronger effect, and 20% obstruction causes
a 36% loss. Cloud can still drive the score to its minimum. This numerical
choice must eventually be
calibrated against target-class-labelled observing logs; physical `epsilon`
and `tau0` are neither clipped nor changed by it.

#### Exact attribution of the displayed loss

The enlarged Overall chart does not infer a penalty from rounded labels. It
decomposes the exact multiplicative loss among the five factors in [F12]:
optical turbulence, cloud obstruction, surface wind, fog, and precipitation.
For factor set `N` and factors `f_i` in `[0,1]`, the displayed contribution is
the Shapley value of the multiplicative-loss game:

```math
\begin{aligned}
v(S)&=1-\prod_{j\in S}f_j,\\
\phi_i
&=\sum_{S\subseteq N\setminus\{i\}}
\frac{\lvert S\rvert!\,(n-\lvert S\rvert-1)!}{n!}
\left[v(S\cup\{i\})-v(S)\right]\\
&=(1-f_i)
\sum_{S\subseteq N\setminus\{i\}}
\frac{\lvert S\rvert!\,(n-\lvert S\rvert-1)!}{n!}
\prod_{j\in S}f_j,\\
L&=1-\prod_{i\in N}f_i=\sum_{i\in N}\phi_i,\\
\mathrm{Overall}+\sum_{i\in N}9\phi_i&=10.
\end{aligned}\tag{F12a}
```

The serialized authoritative `penalty_loss_fraction` is evaluated as
`1-product(f_i)`, not by re-summing its floating-point Shapley attribution.
The latter closes to the same mathematical value within roundoff but, at an
exact veto, its binary64 sum can lie a few ulps above one. The payload
therefore preserves both the bounded physical loss in `[0,1]` and the
unmodified attribution values; their closure is checked with the documented
serialization-consistency tolerances: `2e-6` for factor/product/Shapley loss
and `2e-5` for the reconstructed `1…10` Overall value. These project validation
limits are neither observational uncertainties nor permission to publish a
loss outside `[0,1]`.
This writer correction retains dataset schema 1 and the same scientific
meaning, but `astrodome-dataset-writer-v2` participates in the calculation
cache key so a payload produced by the defective writer cannot be reused.
An archived payload outside the declared unit interval is rejected rather than
silently clamped or reinterpreted.

Thus the lower part of every column is retained suitability and the colored
segments above it are an order-independent allocation of exactly
`10-Overall`; the stack closes at `10` within floating-point roundoff. In an
interacting multiplicative model there is no unique naive “remove one factor”
attribution. Shapley allocation shares interactions over all insertion orders
instead of assigning them according to renderer order. When precipitation
vetoes an already degraded hour, its red segment need not occupy all nine
points: the interaction loss is shared with factors already below one. The
veto remains unambiguous because the retained index is exactly `1` and the bar
carries the precipitation-veto mark.

The allocation axioms and permutation-average value are from
[Shapley (1953), “A Value for n-Person Games”](https://doi.org/10.1515/9781400881970-018).
Its use for these engineering factors is a project attribution rule, not a
claim that Overall is a cooperative game observed in nature.

#### Missing input and completeness contract

Completeness is neither multiplied into Overall nor presented as forecast
probability. Inputs needed to form a physically coherent ICON hour are strict:
invalid precipitation, cloud cover, surface wind, or the hybrid turbulence
profile rejects the frame. Optional diagnostics fail explicitly:

- unavailable direct visibility disables the fog assessment, leaves
  `q_fog=1`, and marks Overall `partial` rather than interpreting missing
  visibility as clear air;
- unavailable condensate retains the documented cloud-cover fallback and
  marks the hour `partial`; missing coherence time likewise marks it `partial`;
- a turbulence profile below either `90%` structural-coverage gate in [F5c],
  or whose top is below `15 km AGL`, rejects the Overall frame; a profile that
  passes the gate but is not
  `complete` retains supported physical diagnostics and marks Overall
  `partial` with `!`;
- missing seeing, invalid native-level station-pressure reconstruction, or
  absent/stale independent composition makes Reference V `partial` and
  suppresses its ring; it never inserts a neutral PSF, sea-level pressure,
  clean-aerosol, or standard-ozone value.

The `!` marker on chart 2 means incomplete inputs, not low confidence in an
otherwise complete probabilistic forecast.

### 3.4. Reference Johnson-V zenith efficiency

Overall remains deliberately target-agnostic. The versioned contracts are
`reference-v-band-zenith-efficiency-v2` and
`reference-v-band-spectrl2-ks91-v3`, normalized by
`reference-v-band-benchmark-v1`. A second diagnostic, drawn as a ring on
chart 2 only during astronomical night (`Sun altitude < -18 deg`), answers a
narrower reproducible question: relative observing efficiency for a faint
point source along the **grid-cell-mean geometric zenith** column, in Generic
Johnson-V, in the long-exposure, background-limited, seeing-limited, non-AO
regime. It is not an instantaneous point-column measurement or a tracked
target direction. For source photon rate `S`, sky photon radiance `B`, and the
noise-equivalent PSF solid angle `Omega_NEA`,

```math
\begin{aligned}
\Omega_{\mathrm{NEA}}
&=\left[\int P(\boldsymbol{\omega})^2\,d\boldsymbol{\omega}\right]^{-1},
&\int P(\boldsymbol{\omega})\,d\boldsymbol{\omega}&=1,\\
G&=\frac{S^2}{B\,\Omega_{\mathrm{NEA}}},\\
g&=\frac{G}{G_{\mathrm{benchmark}}},
&t_{\mathrm{relative}}&=\frac{G_{\mathrm{benchmark}}}{G},\\
I_V&=1+9\,\mathrm{clamp}(g,0,1).
\end{aligned}\tag{F12b}
```

In this regime `SNR^2/t` is proportional to `G`; aperture area and common
detector throughput cancel in the ratio. `G_benchmark` is computed through the
identical code path for pressure `1013.25 hPa`, PWV `5 mm`, total ozone
`300 DU`, aerosol optical depth `AOD_550=0.05`, no cloud attenuation,
`1.0 arcsec` zenith seeing at `500 nm`, natural V sky
`21.7 mag arcsec^-2`, and no Moon contribution. The index is capped at 10,
whereas `g` and `t_relative` retain improvements beyond the benchmark.

The response curve is the
[SVO Filter Profile Service `Generic/Johnson.V`](https://svo2.cab.inta-csic.es/theory/fps3/fps.php?ID=Generic%2FJohnson.V),
pinned by identifier, retrieval revision, and a canonical curve SHA-256. A
flat-`f_nu`, AB=0 reference spectrum (`3631 Jy`) fixes the source
normalization. For wavelength nodes in the passband, the clear-air direct
transmission follows the Bird-Riordan SPECTRL2 parameterization:

```math
\begin{aligned}
P_{\mathrm{surface}}
&=P_l\exp\!\left[
\frac{g_0\left(z_l-z_s\right)}{R_dT_l}
\right],\\
m_p&=\frac{P_{\mathrm{surface}}}{1013\ \mathrm{hPa}},
&u&=\frac{\mathrm{PWV}_{\mathrm{mm}}}{10},
&o&=\frac{\mathrm{O_3}_{\mathrm{DU}}}{1000},\\
T_R(\lambda)
&=\exp\!\left[
-\frac{m_p}{\lambda_{\mu\mathrm{m}}^4
\left(115.6406-1.3366/\lambda_{\mu\mathrm{m}}^2\right)}
\right],\\
T_a(\lambda)
&=\exp\!\left[-\mathrm{AOD}_{550}
\left(\frac{\lambda}{550\ \mathrm{nm}}\right)^{-1.14}\right],\\
T_w(\lambda)
&=\exp\!\left[-\frac{0.2385\,a_w(\lambda)u}
{\left(1+20.07\,a_w(\lambda)u\right)^{0.45}}\right],\\
T_o(\lambda)&=\exp[-a_o(\lambda)o],\\
T_g(\lambda)
&=\exp\!\left[-\frac{1.41\,a_g(\lambda)m_p}
{\left(1+118.3\,a_g(\lambda)m_p\right)^{0.45}}\right],\\
T_{\mathrm{clear}}(\lambda)
&=T_R(\lambda)T_a(\lambda)T_w(\lambda)T_o(\lambda)T_g(\lambda),\\
S&=\frac{f_{\nu,0}}{h}
\int\frac{R_V(\lambda)T_{\mathrm{clear}}(\lambda)
T_{\mathrm{cloud}}}{\lambda}\,d\lambda.
\end{aligned}\tag{F12c}
```

`a_w`, `a_o`, and `a_g` are the tabulated SPECTRL2 water-vapour, ozone, and
mixed-gas absorption coefficients, linearly interpolated only within the
Johnson-V support. The primary method is
[Bird & Riordan (1986)](https://doi.org/10.1175/1520-0450(1986)025%3C0087:SSSMFD%3E2.0.CO;2),
with the original technical report available from
[NREL/SERI](https://www.nrel.gov/docs/legosti/old/2436.pdf).
Here `P_l` and `T_l` are the pressure and temperature of the lowest available
native ICON full model level, `z_l` is the full-level height formed from its
adjacent `HHL` boundaries, `z_s` is the bottom model-surface `HHL`,
`g_0=9.80665 m s^-2`, and
`R_d=287.05 J kg^-1 K^-1`. The first line of [F12c] dry-hydrostatically
continues that lowest level through the short model gap to the actual model
surface. Its recorded provenance is
`icon-lowest-model-level-p-hydrostatic-to-hhl-surface-v1`.

This is station/model-surface pressure, not pressure reduced to mean sea
level. `PMSL` remains a weather-display field and is never supplied to
SPECTRL2. There is deliberately no `PMSL` or standard-atmosphere fallback:
missing or invalid same-hour `P_l/T_l/z_l/z_s`, or a lowest-level-to-surface
gap greater than the guarded `1 km`, makes Reference V partial and
suppresses its ring. The reconstruction assumes dry hydrostatic balance and
constant `T_l` across the thin lowest-level-to-surface interval; it does not
resolve moisture, sub-grid terrain, or temperature curvature inside that
gap. ICON supplies these pressure inputs, `TQV` as PWV, and the already
documented cloud transmission. NASA GEOS-CF supplies total `AOD550` and
total-column ozone;
the provider and independent run/base time/grid are retained with every
diagnostic. GEOS-CF's system and validation are described by
[Keller et al. (2021)](https://doi.org/10.1029/2020MS002413), and the live
product contract is documented by
[NASA GMAO](https://gmao.gsfc.nasa.gov/gmao-products/geos-cf/system-description_geos-cf/).
Because the feed exposes only `AOD550` for this use, the rural SPECTRL2
Angstrom exponent `1.14` is an explicit spectral-shape assumption rather than
information inferred from ICON.

The reconstructed pressure parameterizes the Rayleigh and mixed-gas terms of
this Reference-V calculation only; neither it nor `PMSL` is a new generic
Overall factor.

The PSF is not evaluated at one effective wavelength. Each spectral node uses
Kolmogorov scaling
`FWHM(lambda)=epsilon_500(lambda/500 nm)^(-1/5)`. With normalized photon
weights `w_i` and circular Gaussian standard deviations `sigma_i`, the square
of the photon-weighted mixture integrates analytically:

```math
\begin{aligned}
P(\boldsymbol{\omega})&=\sum_i w_iP_i(\boldsymbol{\omega}),
&\sum_i w_i&=1,\\
\int P^2\,d\boldsymbol{\omega}
&=\sum_i\sum_j
\frac{w_iw_j}{2\pi(\sigma_i^2+\sigma_j^2)},\\
\Omega_{\mathrm{NEA}}
&=\left[
\sum_i\sum_j\frac{w_iw_j}{2\pi(\sigma_i^2+\sigma_j^2)}
\right]^{-1}.
\end{aligned}\tag{F12d}
```

ICON's all-sky cloud transmission is a grey multiplier on the source in this
contract. The background is held at the modeled clear-air natural/lunar value
as a conservative floor because ICON does not provide the cloud-scattered
radiance required for a closed sky-background calculation. For fixed clear-air
composition and PSF,

```math
\begin{aligned}
S&=S_{\mathrm{clear}}T_{\mathrm{cloud}},\\
B&=B_{\mathrm{clear,natural+Moon}},\\
G_{\mathrm{cloud}}
&=\frac{(S_{\mathrm{clear}}T_{\mathrm{cloud}})^2}
{B_{\mathrm{clear,natural+Moon}}\Omega_{\mathrm{NEA}}}
=T_{\mathrm{cloud}}^2G_{\mathrm{clear}}.
\end{aligned}\tag{F12e}
```

Thus the implemented cloud response is approximately quadratic in
transmission. This is deliberately conservative but is also an explicit model
limitation: actual clouds may attenuate, redistribute, or increase the natural
and lunar background differently. The model must not be interpreted as a
cloud radiative-transfer solution.

The V-band zenith Moon contribution uses the empirical scattering and lunar
illuminance relations of
[Krisciunas & Schaefer (1991)](https://doi.org/10.1086/132921), topocentric
Moon altitude/zenith distance, phase angle, and Earth-Moon distance. It is a
declared KS91 fallback calibrated at Mauna Kea, not local radiative-transfer
truth. The baseline natural sky is fixed at `21.7 mag arcsec^-2`.

The reported extinction magnitude is
`A_V=-2.5 log10(T_atmospheric)`. At exactly opaque transmission the logarithm
has no finite value, so `ExtinctionMag` is omitted (or represented as `null` by
schemas that retain the field) and `OpaqueTransmission=true` is set. A high
fog-risk hour or a precipitation-veto hour makes Reference V operationally
unavailable and suppresses the chart marker even if the underlying diagnostic
components were computed. Missing seeing or atmospheric composition remains
explicitly `partial` and likewise cannot produce a marker.

Artificial light pollution is intentionally excluded from `B`: the atlas
value remains a separate site-context line because it is static, has a
different epoch/resolution, and does not supply an hourly spectral sky
radiance consistent with this observation contract. Reference V is likewise
**not silently multiplied into Overall**. The ring and the stacked Overall
bar answer different questions and may legitimately disagree.

PWV therefore has a real, wavelength-resolved V-band effect in [F12c], but no
invented universal Overall penalty. `theta0`, `FracGL`, and `tau0` are highly
relevant to adaptive optics; scintillation depends on telescope aperture,
exposure, target direction, and a different height/wind moment. None has one
scientifically defensible penalty for all visual, imaging, photometric, NIR,
MIR, and AO modes. They remain physical diagnostics until an observing mode
provides the missing contract. A GEOS-CF outage is fail-open for the seven
ICON charts: Reference V becomes partial/unavailable, while the generic
forecast continues and never substitutes climatological AOD or ozone.

## 4. Overall implementation and control calculations

### 4.1. Current model and regression controls

The current calculation deliberately separates physical quantities from the
user-facing utility. The physical layer computes the optical-turbulence
integrals, zenith seeing `epsilon`, coherence time `tau0`, and effective cloud
transmission `q_cloud` without clipping. The `1…10` layer only combines these
different quantities into a planning aid:

```math
\begin{aligned}
J&=\int C_n^2\,dz,\\
\varepsilon&=C_\varepsilon\lambda^{-1/5}J^{3/5},
&C_\varepsilon&=5.306963958\ldots,\\
\tau_0&=C_\tau\lambda^{6/5}
\left(\int C_n^2\lvert V\rvert^{5/3}\,dz\right)^{-3/5},\\[2pt]
q_{\mathrm{seeing}}
&=\mathrm{clamp}\!\left(
\frac{\ln(\varepsilon_{\mathrm{bad}}/\varepsilon)}
{\ln(\varepsilon_{\mathrm{bad}}/\varepsilon_{\mathrm{best}})},0,1\right),\\
q_{\tau}
&=\mathrm{clamp}\!\left(
\frac{\ln(\tau_0/\tau_{\mathrm{bad}})}
{\ln(\tau_{\mathrm{best}}/\tau_{\mathrm{bad}})},0,1\right),\\
q_{\mathrm{turbulence}}
&=q_{\mathrm{seeing}}^{w_{\mathrm{seeing}}}
\left[1-w_{\tau}(1-q_{\tau})\right],\\
f_{\mathrm{turbulence}}
&=(1-p_{\mathrm{turbulence}})
+p_{\mathrm{turbulence}}q_{\mathrm{turbulence}},\\
Q&=f_{\mathrm{turbulence}}q_{\mathrm{cloud}}^{w_{\mathrm{cloud}}}
q_{\mathrm{surface}}q_{\mathrm{fog}}q_{\mathrm{precip}},\\
\mathrm{Overall}&=1+9\,\mathrm{clamp}(Q,0,1).
\end{aligned}
```

With default `w_seeing=1`, `w_tau=0.25`, `w_cloud=2`, and
`p_turbulence=0.25`, physically poor seeing is not a veto on every observing
mode. The target can remain visible while fine detail and image quality
degrade. Effective cloud obstruction instead represents transmission loss and
can reduce the cloud term to zero. Detected precipitation is an independent
hard operational veto and sets `q_precip=0`. The relative bounds in a dry hour
are

```math
\begin{aligned}
0.75&\le f_{\mathrm{turbulence}}\le1,\\
B_{\mathrm{equal}}&=1-\sqrt{0.75}=0.133975\ldots,\\
q_{\mathrm{cloud}}(B=0.20)^{2}&=(1-0.20)^2=0.64.
\end{aligned}
```

Thus about `13.4%` effective obstruction already matches the greatest possible
turbulence loss, and `20%` obstruction has the stronger effect.

The `p_turbulence=0.25` coefficient is not presented as a physical constant.
It is a configurable prior for a general-purpose index: 75% of normalized
utility remains available to observing modes governed by target visibility
rather than limiting angular resolution. Fried theory and the `Cn²` moments
provide the physical basis; ESO's separate treatment of image
quality/turbulence and transparency provides the basis for keeping these
effects distinct. The numerical prior still requires target-class-labelled
observing logs and independent DIMM/MASS/SCIDAR calibration.

A regression calculation over a retained 66-term input set checks the current
semantics. Raw seeing quality is zero in 26 terms, including 15 with modeled
cloud transmission of at least 80%. The two control cases are

```math
\begin{aligned}
(\varepsilon,\tau_0,T_{\mathrm{cloud}})
&=(2.63\ \mathrm{arcsec},1.67\ \mathrm{ms},0.9451)
&&\Longrightarrow\quad \mathrm{Overall}=7.03,\\
T_{\mathrm{cloud}}&=0.0006
&&\Longrightarrow\quad \mathrm{Overall}\approx1.00.
\end{aligned}
```

This control demonstrates that zero raw seeing quality does not independently
force the exact minimum, whereas an effectively opaque cloud column can do so.

The mismatch between diagnostic `CLC` cover and grid-resolved `QC/QI`
condensate is handled by a separate bounded tier guard. DWD documents that
split in the
[ICON tutorial's cloud-cover section](https://www.dwd.de/EN/ourservices/nwp_icon_tutorial/pdf_volume/icon_tutorial2020_en.pdf?__blob=publicationFile&v=9)
and the official
[ICON database description](https://isabel.dwd.de/SharedDocs/downloads/DE/modelldokumentationen/nwv/icon/icon_dbbeschr_aktuell.pdf?nn=16102&view=nasPublication).
`QC_DIA/QI_DIA` are absent from the checked public ICON-EU set, while
`CLCT_MOD` is documented as a visualization field that ignores isolated
cirrus. It is therefore not used as a substitute for physical obstruction.

### 4.2. Free atmosphere: HMNSP99

HMNSP99 remains the free-atmosphere parametrization above the PBL. Per layer:

```math
\begin{aligned}
\theta&=T\left(\frac{1000}{P}\right)^{0.286},\\
M&=-79\times10^{-6}\frac{P}{T^2}\frac{d\theta}{dz},\\
S&=\frac{\mathrm{hypot}(du,dv)}{dz},\\
Y&=
\begin{cases}
0.362+16.728S-192.347\,\dfrac{dT}{dz}, & \text{troposphere},\\
0.757+13.819S-57.784\,\dfrac{dT}{dz}, & \text{stratosphere},
\end{cases}\\
L_0^{4/3}&=0.1^{4/3}10^Y,\\
C_n^2&=2.8\,L_0^{4/3}M^2.
\end{aligned}
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

### 4.3. Ground layer: native ICON TKE, hourly `MH`, and the Masciadri equation

The ground-layer boundary is no longer fixed at `2 km AGL`. Every hourly
forecast time uses the ICON single-level `MH` field (ecCodes `mld`, mixed-layer
depth in metres), bounded as follows:

```math
h_{\mathrm{PBL}}
=\mathrm{clamp}(\mathrm{MH},500\,\mathrm{m},2000\,\mathrm{m})\ \mathrm{AGL}.
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

```math
\begin{aligned}
C_n^2
&=3.35\times10^{-6}
P^{\,2(1-2R/c_p)}
\theta^{-10/3}
\left|\frac{d\theta}{dz}\right|^{4/3}
\mathrm{TKE}^{2/3},\\
\frac{R}{c_p}&=0.286.
\end{aligned}
```

Here `P` is in hPa, `theta` in K, `z` in m, and `TKE` in `m²/s²`. Nodes are
integrated trapezoidally from the model surface to the hourly `h_PBL`; the
first full level is extended through the small slab down to the surface.
HMNSP99 is integrated only above the same boundary, so there is no overlap:

```math
\begin{aligned}
h_{\mathrm{PBL}}
&=\mathrm{clamp}(\mathrm{ICON\_MH},500\,\mathrm{m},2000\,\mathrm{m}),\\
J_{\mathrm{GL}}&=\int_{0}^{h_{\mathrm{PBL}}\ \mathrm{AGL}}C_n^2\,dz,\\
J_{\mathrm{FA}}&=\int_{h_{\mathrm{PBL}}\ \mathrm{AGL}}^{\mathrm{model\ top}}C_n^2\,dz,\\
J_{\mathrm{total}}&=J_{\mathrm{GL}}+J_{\mathrm{FA}}.
\end{aligned}
```

A server-side sensitivity calculation used a fixed `2 km AGL` comparison
boundary to quantify the scale of the ground-layer contribution:

| Lead | Hybrid seeing | Ground-layer share of `J` |
|---|---:|---:|
| `f042` | `2.221″` | `88.36%` |
| `f048` | `3.644″` | `94.43%` |

Both use `ground Cn² scale = 1.0`, with no fit to the outcome. These are two
diagnostic leads, not observational validation. They show why a
pressure-level-only `0.70…0.77″` series can understate the contribution below
the free atmosphere.

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

### 4.4. Seeing, coherence time, `theta0`, and layer fractions

Both products are evaluated at `lambda = 500 nm` and at zenith. From the full
integral:

```math
\begin{aligned}
r_0&=\left[0.423\left(\frac{2\pi}{\lambda}\right)^2J_{\mathrm{total}}\right]^{-3/5},\\
\varepsilon&=0.98\frac{\lambda}{r_0}.
\end{aligned}
```

The equivalent expanded form is

```math
\varepsilon_{\mathrm{rad}}
=5.306963958\ldots\,\lambda^{-1/5}J_{\mathrm{total}}^{3/5}.
```

It returns radians before conversion to arcseconds. The code evaluates the two
base equations directly rather than storing the expanded decimal coefficient.

Wind enters through atmospheric coherence time:

```math
\begin{aligned}
M_{\mathrm{wind}}&=\int C_n^2\lvert V\rvert^{5/3}\,dz,\\
k&=\frac{2\pi}{\lambda},\\
\tau_0&=\left(2.910\,k^2M_{\mathrm{wind}}\right)^{-3/5}.
\end{aligned}
```

This is the standard wind-weighted turbulence integral and phase-structure
definition in [Kellerer & Tokovinin (2007)](https://www.aanda.org/articles/aa/pdf/2007/02/aa5788-06.pdf);
[Liu et al. (2015)](https://academic.oup.com/mnras/article/451/3/3299/2907963)
use its rounded expanded form.
It penalizes strong wind specifically where optical turbulence is present.

The same retained piecewise-linear profile also supplies `J_h`, `theta0`,
`h_eff`, `V_eff`, `FracGL250/500/1000`, and free-atmosphere seeing above
`500 m`, exactly as specified in [F5a]-[F5c]. There is no independent resample
or second `Cn2` estimate. These fields support AO/site interpretation but do
not add another multiplier to generic Overall: doing so would both double
count turbulence and pretend that one AO constraint applies to every visual,
photometric, and imaging programme.

No separate full `direction delta` penalty is added. The identity

```math
\lvert\Delta\mathbf{V}\rvert^2
=V_1^2+V_2^2-2V_1V_2\cos(\Delta\varphi).
```

shows that HMNSP vector shear already contains wind rotation. Multiplying by
direction change again would double-count it. On the diagnostic chart,
direction change follows the masking rule

```math
\min(V_A,V_B)<2\ \mathrm{m\,s^{-1}}
\quad\Longrightarrow\quad\Delta\varphi_{\mathrm{chart}}=0^\circ.
```

Near-calm direction is unstable and has little practical effect.

### 4.5. An auditable `1…10` mapping

This is an engineering mapping, not a new physical scale. The reference
seeing and `tau0` values come from official
[ESO observing-condition categories](https://www.eso.org/sci/observing/phase2/ObsConditions.CRIRES.html).
To avoid a broad plateau, seeing quality varies logarithmically:

```math
\begin{aligned}
q_{\mathrm{seeing}}
&=\mathrm{clamp}\!\left(
\frac{\ln(\varepsilon_{\mathrm{bad}}/\varepsilon)}
{\ln(\varepsilon_{\mathrm{bad}}/\varepsilon_{\mathrm{best}})},0,1\right),\\
\varepsilon_{\mathrm{best}}&=0.5\ \mathrm{arcsec},
&\varepsilon_{\mathrm{bad}}&=2.0\ \mathrm{arcsec}.
\end{aligned}
```

Coherence time, where larger is better, uses the analogous mapping:

```math
\begin{aligned}
q_{\tau}
&=\mathrm{clamp}\!\left(
\frac{\ln(\tau_0/\tau_{\mathrm{bad}})}
{\ln(\tau_{\mathrm{best}}/\tau_{\mathrm{bad}})},0,1\right),\\
\tau_{\mathrm{best}}&=5.2\ \mathrm{ms},
&\tau_{\mathrm{bad}}&=1.6\ \mathrm{ms},\\
q_{\mathrm{coherence}}&=1-0.25(1-q_{\tau}),\\
q_{\mathrm{turbulence}}
&=q_{\mathrm{seeing}}^{w_{\mathrm{seeing}}}q_{\mathrm{coherence}},\\
f_{\mathrm{turbulence}}&=0.75+0.25q_{\mathrm{turbulence}}.
\end{aligned}
```

`tau weight = 0.25` keeps wind relevant inside the high-resolution term. The
outer convex mixture caps the **combined** seeing and coherence-time influence
at 25%; it does not clip or replace either physical `Cn²` moment.

### 4.6. Effective ICON cloud obstruction

For cloud fraction `C` and a total-column or layer condensate path, visible
optical depth is estimated separately for liquid and ice:

```math
\begin{aligned}
\tau_{\mathrm{phase}}&=\frac{3Q_{\mathrm{ext}}\mathrm{CWP}}{4\rho r_{\mathrm{eff}}},\\
B_{\mathrm{cond}}&=C\left[1-\exp\!\left(-\frac{\tau}{C}\right)\right].
\end{aligned}
```

The cloud-water-path to optical-depth form follows
[Lowe et al.](https://www.nature.com/articles/s41467-019-12982-0).
`B_cond` is the fraction of sky blocked by model-resolved condensate, not mere
cloud cover.

On native model levels, mixing ratio becomes condensate path using the actual
full-layer thickness between adjacent `HHL` boundaries, rather than a pressure
difference inferred between sparsely retained levels:

```math
\begin{aligned}
\Delta z_{\mathrm{native}}&=\lvert\mathrm{HHL}_k-\mathrm{HHL}_{k+1}\rvert,\\
m_{\mathrm{air}}&=\frac{P_{\mathrm{Pa}}}{R_dT}\,\Delta z_{\mathrm{native}},\\
\mathrm{CWP}_{\mathrm{phase}}&=q_{\mathrm{phase}}m_{\mathrm{air}},\\
R_d&=287.05\ \mathrm{J\,kg^{-1}\,K^{-1}}.
\end{aligned}
```

Consequently, `T` is downloaded at all 27 heatmap levels. Sparse sampling
aloft never assigns the mass of skipped native layers to a retained level.

To prevent diagnostic `CLC` with almost zero grid-scale `QC/QI` from becoming
“perfect transparency”, a tier-aware conservative guard is applied:

```math
\begin{aligned}
g_{\mathrm{low}}&=g_{\mathrm{base}}=0.45,\\
g_{\mathrm{middle}}&=0.55g_{\mathrm{base}}=0.2475,\\
g_{\mathrm{high}}&=0.18g_{\mathrm{base}}=0.081,\\
B_{\mathrm{effective,layer}}
&=\max(B_{\mathrm{cond}},g_{\mathrm{tier}}C).
\end{aligned}
```

For the heatmap, tier follows the layer's actual AGL midpoint: low below 2 km,
middle from 2 to 7 km, and high from 7 km. The column calculation instead uses
the ready-made ICON `CLCL/CLCM/CLCH` tiers and overlaps their guards as:

```math
\begin{aligned}
B_{\mathrm{guard,column}}
&=\min\!\left[C_{\mathrm{cap}},
1-(1-B_{\mathrm{low}})(1-B_{\mathrm{middle}})(1-B_{\mathrm{high}})\right],\\
C_{\mathrm{cap}}&=\max(\mathrm{CLCT},\mathrm{CLCL},\mathrm{CLCM},\mathrm{CLCH}).
\end{aligned}
```

The base `45%` and the `55%`/`18%` tier multipliers are conservative
engineering uncertainty factors, not physical opacity assigned to low,
middle, or high cloud. Resolved `QC/QI` physics always wins when stronger,
because [F8] takes the larger of resolved condensate obstruction and the tier
guard.

The hourly **Effective ICON cloud obstruction** heatmap applies this optical
formula and tier-aware guard independently at each native model level using
`CLC/QC/QI/T/HHL`. Overall instead uses total-column `TQC/TQI`, total `CLCT`,
and `CLCL/CLCM/CLCH`; its three tier guards are combined with random
overlap and capped by diagnosed cover. Heatmap cells and Overall `q_cloud`
therefore **need not be numerically identical**. They share the optical-depth
kernel and unresolved-CLC guard policy, while the chart represents vertical
structure and Overall represents total-column transmission.

### 4.7. Precipitation, fog, dew, and surface wind

Precipitation is a binary operational constraint, not an atmospheric-seeing
term. At `R_1h >= 0.05 mm`, `q_precip=0` and Overall is `1.0`; below the
detection boundary `q_precip=1`. The threshold is configurable. No smooth
intensity penalty is inferred from a deterministic ICON member.

Dew is excluded from Overall. It remains an operational prompt to prepare a
heater or dew shield and does not by itself worsen atmospheric seeing.

Fog physically blocks observations, so the multiplier follows the existing
`FogRisk`:

```math
q_{\mathrm{fog}}=
\begin{cases}
1.00, & \text{no fog risk},\\
0.75, & \text{possible fog},\\
0.10, & \text{high fog risk}.
\end{cases}
```

Surface wind is a separate and deliberately mild practical factor because
screens, a low setup, and site choice can partly mitigate it. SAAO observations
show a weak relation between surface wind and seeing except in the highest
range near `>=8.6 m/s`: [Catala et al.](https://academic.oup.com/mnras/article/436/1/590/975197).
ESO provides operational context rather than the exact project thresholds:
[CRIRES visitor instructions](https://www.eso.org/sci/facilities/paranal/instruments/crires/visitor.html)
restrict pointing into wind above `12 m/s`, while
[VLT environmental specifications](https://www.eso.org/sci/facilities/paranal/telescopes/ut/envspecs.html)
state full-performance mean-wind limits of `14` or `18 m/s` depending on
orientation and a gust limit of `27 m/s`.

Calibration uses smoothstep risks for mean wind over `8.5…15 m/s` and gusts
over `12…22 m/s`, takes the worse risk, and caps the penalty at 20%:

```math
\begin{aligned}
r_{\mathrm{surface}}
&=\max\!\left[\mathrm{smoothstep}(V_{10};8.5,15),
\mathrm{smoothstep}(V_{\mathrm{gust}};12,22)\right],\\
q_{\mathrm{surface}}&=1-0.20r_{\mathrm{surface}}.
\end{aligned}
```

### 4.8. Final composition and parameters

With the default `w_seeing=1` and `w_cloud=2`:

```math
\begin{aligned}
Q_{\mathrm{normalized}}
&=f_{\mathrm{turbulence}}q_{\mathrm{cloud}}^{w_{\mathrm{cloud}}}
q_{\mathrm{surface}}q_{\mathrm{fog}}q_{\mathrm{precip}},\\
\mathrm{Overall\ Astronomy\ Index}
&=1+9\,\mathrm{clamp}(Q_{\mathrm{normalized}},0,1).
\end{aligned}
```

The calculation does not round to an integer internally. An exact `10.0`
requires the best seeing boundary, best `tau0` boundary, no effective cloud
obstruction, no fog, no detected precipitation, and no surface-wind penalty
at the same time. Detected precipitation instead returns exactly `1.0`.

Parameters live in `.env`, enter the algorithm/render-cache version, and must
change together with regression examples:

| Parameter | Default |
|---|---:|
| best / bad seeing | `0.5″ / 2.0″` |
| best / bad `tau0` | `5.2 / 1.6 ms` |
| `tau0` weight | `0.25` |
| combined optical-turbulence maximum penalty | `0.25` |
| ground `Cn²` scale | `1.0` |
| `MH` clamp AGL | `500…2000 m` |
| unresolved `CLC` guard: low / middle / high | `0.45 / 0.2475 / 0.081` |
| possible / high fog factor | `0.75 / 0.10` |
| hourly precipitation detection/veto threshold | `0.05 mm` |
| surface-wind maximum penalty | `0.20` |
| mean-wind thresholds | `8.5 / 15 m/s` |
| gust thresholds | `12 / 22 m/s` |

### 4.9. Accuracy limits and future validation

The hybrid ICON result remains a model estimate. Its grid does not resolve
local terrain, vegetation, site heating, or dome seeing; both Masciadri/TKE
and HMNSP99 have systematic error. Even after local calibration, Cuevas et al.
found error around `0.30″` against Stereo-SCIDAR and a tendency to
underestimate seeing.

The fixed-2-km comparison values `2.221″/3.644″` and the dynamic-boundary
values with `MH=396 m` and a 500 m lower clamp, `1.9145″/2.5478″`, demonstrate
sensitivity to PBL treatment but are not ground truth. Production run
`2026072106` with
`surface-hourly-v17` and `cloud-hourly-v4` is synchronized and passed
post-deploy verification: 69 hours gave Overall `1.00…4.58`, no exact `10`,
and seeing `0.72…2.84″`. The next scientific
step is an hourly comparison against DIMM/MASS/SCIDAR or high-quality observing
logs around Saint Petersburg and Moscow. Until then, the
UI must say “model estimate” and must not call Overall a measured seeing value
or a Pickering scale.

## 5. Reproducibility, validation, and interpretation

### 5.1. Reproducibility

Record the following for every result used in analysis or publication:

1. source revision or release tag;
2. `config.yaml` without secrets and every `ASTRO_OVERALL_*` override;
3. ICON run ID and manifest schema versions; when Reference V is used, also
   the independently recorded GEOS-CF provider, run/base time, and grid;
4. coordinates, resolved IANA time zone, and forecast interval;
5. algorithm and renderer versions printed on the images.

Acquire the same upstream run, execute `sync-icon-eu`, and use `render-point`.
The numerical pipeline is deterministic for identical inputs and
configuration; file-generation timestamps and upstream availability are
operational metadata. Raw model data stay outside Git, but the run ID,
manifests, configuration, and source revision make the calculation traceable.

### 5.2. Validation protocol and responsible interpretation

- Unit tests cover parsing, units, grid selection, optical-depth kernels, the
  unified turbulence profile and its moments, target-conditioned image-quality
  scaling, precipitation veto, exact Shapley closure, Reference-V spectral and
  Moon-background kernels, cache identity, and retention.
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

## 6. Data-source contracts and server verification

Status: original Stage 0 spike updated with the current data contract;
`surface-hourly-v17`/`cloud-hourly-v4` is published in production
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
`58…74`. All 27 full levels require `CLC/P/T/QC/QI`; the lower chain also
requires `U/V`, while half-level `TKE/HHL` supplies turbulence and geometry.
The two bounding TKE values map to each full layer. The `cloud-hourly-v4`
contract contains exactly 187 messages at every `f000…f078` plus separate
time-invariant `HHL` geometry.

Server-side verification for only `f042/f048` confirmed availability and units
of those DWD fields. The first fixed-2-km calculation produced `2.221″` and
`3.644″`, respectively; the ground layer contained 88.36% and 94.43% of total
`J`. The current design takes single-level `MH` every hour and uses

```math
h_{\mathrm{PBL}}=
\mathrm{clamp}(\mathrm{MH},500\ \mathrm{m},2000\ \mathrm{m})
\quad\mathrm{AGL},
```

applying HMNSP99 only above the same
boundary. `MH` was `396 m` at both control leads: the unbounded cutoff gave
`1.9075″/2.5158″`, while the 500 m minimum gave `1.9145″/2.5478″`. This is a
server-only regression without observational calibration. Full hourly
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
the previous directory removed. The enriched point cache uses
`point-v6-native-cloud-mass-mh`, preventing old `gob.gz` entries without
native thickness or `MH` from being interpreted as current data.

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

## 7. Directional Horizon analysis

### 7.1. Scope, time, and provider

The optional Horizon product answers a directional question for every term of
the immutable ICON-EU interval `f000..f072` (73 hourly terms spanning 72
hours): in which of eight compass directions are conditions
least obstructed for an object referenced at `10 deg` geometric elevation? The
directions and azimuths are
`N=0`, `NE=45`, `E=90`, `SE=135`, `S=180`, `SW=225`, `W=270`, and `NW=315 deg`.
The scientific contract is versioned as
`horizon-spherical-los-tke-hmnsp99-v7`.

All eight directions use that same `f000..f072` time axis. Native hourly
surface, cloud, TKE, MH, and visibility terms are used directly. Pressure-level
`U/V/T/Z` are native every three hours; the raw variables are linearly
interpolated between same-run bracketing terms, and the HMNSP/TKE profile,
`Cn2`, seeing, `tau0`, and index are then recomputed for the requested hour.
`Cn2`, seeing, `tau0`, or an index are never interpolated. No field is
extrapolated beyond `f072` or across a run boundary. Solar altitude classifies
the continuous time axis as day (`>=0 deg`), bright twilight (`0…-12 deg`),
astronomical twilight (`-12…-18 deg`), or night (`<-18 deg`) for visual shading
only. Each crossing is bracketed numerically to within five minutes before
rounding to the chart pixel, rather than to the nearest model hour. It is not a
score multiplier; a polar-day forecast is shown as a fully daylight-marked
period rather than collapsed to one hour. Captions identify the run, its
freshness, and the covered valid-time interval.

Current-run identity and the freshness label are separate safeguards. The
workflow confirms the same current run before cache reuse or heavy work, the
source confirms it before and after acquisition, and delivery confirms it
immediately before every send, including cache hits. A mismatch rejects the
result. For an unchanged run, the caption calculates age at delivery time with
the configured ICON-EU `max_stale_age`; that threshold labels freshness but does
not alter [F13]-[F21].

The method is **ICON-EU-only**. The observer and every actual midpoint lookup
must be inside the open ICON-EU output domain, and pressure, surface, cloud, and
HHL values must belong to the same immutable run and compatible time grid. ICON Global has
no Horizon button and no runnable Horizon calculation. This is a product and
data-quality boundary, not a statement that spherical geometry ceases outside
Europe.

### 7.2. Spherical line-of-sight geometry

The current geometry is a spherical-Earth straight chord. With
`R=6,371,008.8 m`, observer HHL elevation `h0`, reference elevation
`e=10 deg`, and line-of-sight distance `s`, the ray radius, altitude, and
sub-ray central angle are

```math
\begin{aligned}
r_0&=R+h_0,\\
r(s)&=\sqrt{r_0^2+s^2+2r_0s\sin e},\\
h(s)&=r(s)-R,\\
\alpha(s)&=\mathrm{atan2}(s\cos e,\ r_0+s\sin e),\\
x(s)&=R\alpha(s).
\end{aligned}\tag{F13}
```

The forward intersection with the fixed absolute top `H=22,300 m MSL` is

```math
s_{\mathrm{top}}
=-r_0\sin e+\sqrt{(R+H)^2-r_0^2\cos^2 e}.
\tag{F14}
```

Lengths in [F13]-[F14] are metres; trigonometric calculations use radians.

At sea level this is `s_top=121.927 km` and `x_top=119.662 km`. Ground-distance
boundaries are at no more than `0.5 km`; the midpoint of each resulting chord
segment supplies one model lookup and the exact difference in `s` supplies its
quadrature length `ds`. At 10 degrees a full ground step changes ray altitude
by about 87 m, avoiding coarse PBL/cloud aliasing.
The production constants produce 240 samples per direction at sea level.
Nearest ICON-EU cells are deduplicated after all actual geodesic destinations
have passed the coverage check.

For origin latitude `phi1`, longitude `lambda1`, azimuth `A`, and central angle
`alpha`, every spherical destination is

```math
\begin{aligned}
\phi_2
&=\arcsin\!\left(\sin\phi_1\cos\alpha
+\cos\phi_1\sin\alpha\cos A\right),\\
\lambda_2
&=\lambda_1+\mathrm{atan2}\!\left(
\sin A\sin\alpha\cos\phi_1,\;
\cos\alpha-\sin\phi_1\sin\phi_2\right).
\end{aligned}\tag{F15}
```

Longitudes are normalized to `[-180,180)`. Exact geographic poles are rejected
because compass azimuth is degenerate; near-polar and dateline-crossing paths
remain well-defined.

`10 deg` is a project product choice. At `5 deg` the footprint and sensitivity
to refraction and unresolved terrain become much larger, whereas `20 deg` is no
longer a useful near-horizon diagnostic. The constant is a geometric reference:
the current code traces a straight `10 deg` chord and does not bend the ray or
apply a pressure/temperature-dependent refraction correction. Equation 42 of the
[NREL Solar Position Algorithm](https://www.nrel.gov/docs/fy08osti/34302.pdf)
is the reference approximation used to quantify the omitted correction. This
unsupported effect and the spherical rather than ellipsoidal Earth are part of
the stated uncertainty.

### 7.3. Directional optical turbulence and wind

At each segment midpoint and forecast hour, the ray altitude selects exactly one
local turbulence kernel already used by ordinary Overall: Masciadri/ICON TKE below
`h_surface + clamp(MH,500,2000) m`, and HMNSP99 above it. Model values are
interpolated vertically to the ray altitude. The line-of-sight moments are

```math
\begin{aligned}
\mathbf U_0&=(\cos\phi_0\cos\lambda_0,\ \cos\phi_0\sin\lambda_0,\ \sin\phi_0),\\
\mathbf E_0&=(-\sin\lambda_0,\ \cos\lambda_0,\ 0),\\
\mathbf N_0&=(-\sin\phi_0\cos\lambda_0,\ -\sin\phi_0\sin\lambda_0,\ \cos\phi_0),\\[2pt]
\mathbf D&=\cos e(\sin A\,\mathbf E_0+\cos A\,\mathbf N_0)+\sin e\,\mathbf U_0,\\[2pt]
\mathbf E_j&=(-\sin\lambda_j,\ \cos\lambda_j,\ 0),\\
\mathbf N_j&=(-\sin\phi_j\cos\lambda_j,\ -\sin\phi_j\sin\lambda_j,\ \cos\phi_j),\\
\mathbf U_j&=(\cos\phi_j\cos\lambda_j,\ \cos\phi_j\sin\lambda_j,\ \sin\phi_j),\\[2pt]
d_{E,j}&=\mathbf D\!\cdot\!\mathbf E_j,
&d_{N,j}&=\mathbf D\!\cdot\!\mathbf N_j,
&d_{U,j}&=\mathbf D\!\cdot\!\mathbf U_j,\\
V_{\mathrm{los},j}&=u_jd_{E,j}+v_jd_{N,j},\\
V_{\perp,j}&=\sqrt{\max\!\left(0,u_j^2+v_j^2-V_{\mathrm{los},j}^2\right)},\\[2pt]
J_H(t,A)&=\sum_j C_n^2(t,j)\,\Delta s_j,\\
J_{V,H}(t,A)&=\sum_j C_n^2(t,j)V_\perp(t,j)^{5/3}\,\Delta s_j,\\
\varepsilon_H(t,A)&=C_\varepsilon\lambda^{-1/5}J_H(t,A)^{3/5},\\
\tau_{0,H}(t,A)&=\left[2.910\left(\frac{2\pi}{\lambda}\right)^2
J_{V,H}(t,A)\right]^{-3/5}.
\end{aligned}\tag{F16}
```

`lambda=500 nm`; `epsilon_H` is converted from radians to arcseconds and
`tau0_H` to milliseconds. Only horizontal ICON `u/v` are available here, so
vertical wind is assumed zero. `D` is one fixed unit ray in ECEF coordinates;
projection into each midpoint's local ENU basis accounts for great-circle
bearing convergence and the changing local elevation of that straight chord.
`Vperp` is therefore the horizontal-wind component transverse to the 3-D ray.
This makes coherence time directional without adding an empirical wind rotation
penalty. Terrain geometry may be reused across hours, but all meteorological
values and moments in [F16] are recomputed for every hour. The
long/short-exposure resolution basis follows
[Fried (1966)](https://doi.org/10.1364/JOSA.56.001372). The wind-weighted
coherence-time moment follows
[Kellerer & Tokovinin (2007)](https://www.aanda.org/articles/aa/pdf/2007/02/aa5788-06.pdf).
The PBL and HMNSP99 coefficients and their research provenance remain those
documented in [F1]-[F5].

In [F16], `Cn2` is in `m^(-2/3)`, `ds` in m, and wind in `m/s`; therefore
`J_H` is in `m^(1/3)`. The numerical seeing and coherence coefficients assume
those SI inputs.

Public pressure profiles commonly end near 50 hPa, slightly below 22.3 km. The
last valid HMNSP99 layer and top-level wind are extended only to the fixed top
with quality weight `0.35`; nothing above 22.3 km is integrated. This bounded
closure keeps the product usable but can understate turbulence above the chosen
top and explicitly lowers confidence.

For the homogeneous-`C_n^2` reference used by the quality mapping, the exact
path ratio is taken from the same spherical straight-ray geometry as the
physical line-of-sight integration:

```math
\begin{aligned}
L_H(h_0,e)&=\sum_j\Delta s_j=s_{\mathrm{top}}(h_0,e),\\
X_{\mathrm{geo}}(h_0,e)
&=\frac{L_H(h_0,e)}{H-h_0},\\
s_{\mathrm{geo}}(h_0,e)
&=X_{\mathrm{geo}}(h_0,e)^{3/5},\\[2pt]
X_{\mathrm{geo}}(0,10^\circ)
&=\frac{121926.58412394}{22300}
=5.467559826\ldots,\\
s_{\mathrm{geo}}(0,10^\circ)&=2.771252681\ldots.
\end{aligned}\tag{F17}
```

The reported `epsilon_H` and `tau0_H` are absolute slant estimates. The
spherical line-of-sight integrals contain the actual sampled path. [F17] is
used only in the later *quality mapping* [F20] to put the good/bad seeing
reference anchors at the same fixed elevation; it never changes the physical
LOS integral or reported arcseconds. This normalization is exact for constant
`C_n^2` and constant transverse wind within the same bounded spherical
geometry. It is a transparent project reference, not a substitution of
molecular optical air mass for turbulence air mass. Atmospheric refraction
and real vertical inhomogeneity remain represented by the stated limitations
and by the actual sampled `C_n^2` profile, respectively.

### 7.4. Cloud, fog, and terrain closure

At each ray midpoint the retained native-level `P/T/QC/QI/CLC` profile is
sampled at ray altitude. A containing HHL layer is preferred. Across a gap,
`P`, `T`, liquid/ice mixing ratio, and cover are linearly interpolated between
bracketing retained levels. Because `QC/QI` are grid-box means, samples are
first grouped by unique horizontal ICON cell and low/middle/high tier. For each
such block `b`, the all-sky condensate closure is

```math
\begin{aligned}
\rho_j&=\frac{P_j}{R_dT_j},\\
\mathrm{CWP}_{\mathrm{liquid},b}
&=\sum_{j\in b}Q_{C,j}\rho_j\,\Delta s_j,\\
\mathrm{CWP}_{\mathrm{ice},b}
&=\sum_{j\in b}Q_{I,j}\rho_j\,\Delta s_j,\\[2pt]
\tau_b
&=\frac{3(2.0)\,\mathrm{CWP}_{\mathrm{liquid},b}}
{4(1000)\,r_{\mathrm{liquid}}}
+\frac{3(2.1)\,\mathrm{CWP}_{\mathrm{ice},b}}
{4(916.7)\,r_{\mathrm{ice}}},\\[2pt]
C_b&=\mathrm{clamp}\!\left(\max_{j\in b}C_{\mathrm{LC},j},0,1\right),\\
\widehat C_b&=
\begin{cases}
0, & C_b<10^{-6}\ \land\ \tau_b<10^{-9},\\
\min\!\left(1,\max\!\left(0.01,1-e^{-\tau_b}\right)\right),
& C_b<10^{-6}\ \land\ \tau_b\ge10^{-9},\\
C_b, & C_b\ge10^{-6},
\end{cases}\\
T_b&=
\begin{cases}
1, & \widehat C_b=0,\\
(1-\widehat C_b)+\widehat C_b
\exp\!\left(-\dfrac{\tau_b}{\widehat C_b}\right),
& \widehat C_b>0,
\end{cases}\\
T_{\mathrm{condensate}}&=\prod_bT_b,\\
\tau_{\mathrm{grid\ mean}}&=\sum_b\tau_b.
\end{aligned}\tag{F18}
```

In [F18], `P` is in Pa, `Rd=287.05 J/(kg*K)`, `T` in K, `QC/QI` in kg/kg,
`ds` and effective radii in m, and each `CWP` in `kg/m^2`; `tau_b`,
`tau_grid mean`, and
transmission are dimensionless.

The piecewise definition makes both zero-cover cases explicit. For the rare
inconsistent block with rounded `C_b=0` but nonzero condensate, it applies the
same bounded inference as ordinary Overall. `tau_grid mean` is the sum of
grid-box-mean condensate optical depths; because cover is mixed separately, it
is not generally equal to the negative natural logarithm of `T_condensate`. The density is the dry-air
approximation already disclosed after [F6]. The constants and provenance are
the same as [F6]-[F8]. Grouping prevents the 0.5 km quadrature from multiplying
the same model-cell cover once per substep.

Separately, the maximum cover encountered once in each AGL tier (`<2 km`,
`2..7 km`, `>=7 km`) receives the existing low/middle/high guard `0.45`,
`0.2475`, `0.081`; the three tier obstructions use random overlap. The final
transmission is

```math
T_H=\min(T_{\mathrm{condensate}},T_{\mathrm{guard}}).
```

This is an effective all-sky directional
obstruction proxy from grid-box means, not a literal or retrieved transmission
along one infinitesimal beam. The distinction is part of the scientific limit,
even though the result field retains the short name `CloudTransmission`.

Sparse cloud profiles remain calculable but visibly reduce data quality. A
native containing layer has weight `1`; interpolation uses

```math
q_{\mathrm{interp}}=
\mathrm{clamp}\!\left(
\frac{\Delta z_{\mathrm{lower}}+\Delta z_{\mathrm{upper}}}
{2\,\Delta z_{\mathrm{bracket}}},0.35,0.85\right).
```

A bounded value below
the first retained level has weight `0.50`; and the final level clamped only to
the fixed model top has weight `0.25`. These weights are engineering disclosure,
not probabilities.

Fog is the ordinary observer-cell ICON-EU `VIS/RH/T-Td` classification [F11]
and is therefore identical for all directions. Roughly 7 km output cannot
support a credible directional near-site fog wall. Dew remains an equipment
advisory and does not enter the score.

For each midpoint, coarse terrain is blocked when

```math
\mathrm{HHL}_{\mathrm{surface}}(\mathrm{midpoint})
\ge h_{\mathrm{ray}}(\mathrm{midpoint}).
\tag{F19}
```

Both sides of [F19] are absolute metres MSL.

A block is an explicit index-1 veto. Ordinary Overall already uses observer-cell
HHL as its AGL origin; Horizon adds directional HHL intersection but no separate
altitude bonus or penalty. HHL is neither a local DEM nor an optical skyline
model and does not resolve local terrain or obstructions. The displayed
terrain conclusion must therefore be labelled coarse model terrain. DWD
describes ICON output and orography as grid-cell means in the
[ICON model description](https://www.dwd.de/EN/research/weatherforecasting/num_modelling/01_num_weather_prediction_modells/icon_description.html), and
[the ICON tutorial](https://www.dwd.de/DE/leistungen/nwv_icon_tutorial/pdf_einzelbaende/icon_tutorial2025.pdf)
defines HHL as vertical half-level height; neither is a local survey.

### 7.5. Directional index and limiting factor

The physical `epsilon_H` remains the slant-path seeing calculated from the LOS
`Cn2` integral. Directly applying zenith reference anchors to that value is not
a useful quality mapping at the fixed `10 deg` elevation: even a homogeneous,
otherwise good atmosphere acquires the Fried factor in [F17], which
would saturate the mapping at its lower boundary. The reference anchors
are therefore scaled to the same elevation, while the physical seeing value is
not altered:

```math
\begin{aligned}
X_{\mathrm{geo}}&=\frac{s_{\mathrm{top}}(h_0,10^\circ)}{H-h_0},
&s_{\mathrm{geo}}&=X_{\mathrm{geo}}^{3/5},\\[2pt]
q_{\mathrm{seeing},H}
&=\mathrm{clamp}\!\left(
\frac{\ln[(\varepsilon_{\mathrm{bad}}s_{\mathrm{geo}})/\varepsilon_H]}
{\ln[(\varepsilon_{\mathrm{bad}}s_{\mathrm{geo}})/(\varepsilon_{\mathrm{best}}s_{\mathrm{geo}})]},0,1\right),\\
\tau_{\mathrm{best},H}&=\frac{\tau_{\mathrm{best}}}{s_{\mathrm{geo}}},
&\tau_{\mathrm{bad},H}&=\frac{\tau_{\mathrm{bad}}}{s_{\mathrm{geo}}},\\
q_{\tau,H}
&=\mathrm{clamp}\!\left(
\frac{\ln(\tau_{0,H}/\tau_{\mathrm{bad},H})}
{\ln(\tau_{\mathrm{best},H}/\tau_{\mathrm{bad},H})},0,1\right),\\
q_{\mathrm{turbulence},H}
&=q_{\mathrm{seeing},H}^{w_{\mathrm{seeing}}}
\left[1-w_{\tau}(1-q_{\tau,H})\right],\\
f_{\mathrm{turbulence},H}
&=(1-p_{\mathrm{turbulence}})
+p_{\mathrm{turbulence}}q_{\mathrm{turbulence},H},\\
Q_H&=f_{\mathrm{turbulence},H}T_H^{w_{\mathrm{cloud}}}
q_{\mathrm{surface}}q_{\mathrm{fog}},\\
\mathrm{HorizonIndex}&=1+9\,\mathrm{clamp}(Q_H,0,1).
\end{aligned}\tag{F20}
```

The same default `p_turbulence=0.25`, `w_seeing=1`, `w_cloud=2`, and
`w_tau=0.25` as zenith Overall is used, plus the ordinary fog and mild
surface-wind coefficients. The combined seeing/`tau0` factor can blur detail
but cannot make an otherwise clear direction mathematically unusable.
Effective cloud obstruction remains stronger and can reach zero:

```math
0.75\le f_{\mathrm{turbulence},H}\le1,
\qquad (1-0.20)^2=0.64<0.75.
```

The two elevation scalings follow directly from the physical moments:

```math
\begin{aligned}
J_H&=X_{\mathrm{geo}}J,
&\frac{\varepsilon_H}{\varepsilon_z}&=X_{\mathrm{geo}}^{3/5},\\
J_{V,H}&=X_{\mathrm{geo}}J_V,
&\frac{\tau_{0,H}}{\tau_{0,z}}&=X_{\mathrm{geo}}^{-3/5}.
\end{aligned}
```

Scaling
both reference pairs therefore preserves relative atmospheric quality at the
fixed 10-degree comparison elevation while the displayed physical values stay
slant-path values. The exponents are the Fried-moment result; the reference
ratio is the exact homogeneous path ratio of [F13]-[F14], rather than a
molecular-air-mass approximation. The common `0.25` utility cap remains the
explicit, uncalibrated project prior justified after [F12], because the
importance of fine resolution depends on target scale, focal length, sampling,
and observing technique.

A terrain block forces index 1. An incomplete direction is marked unavailable
and also carries sentinel index 1; the UI must distinguish it from a valid poor
direction. The main limiting factor is the smallest contributing factor, with
stable ordering only to break equal values. The presentation names that factor
explicitly: `optical seeing` means the model-derived turbulence integral,
while `effective cloud obstruction` means the phase-resolved
cloud-transmission term. It does not use the generic word “weather” for either
case. A period summary reports the most frequent primary factor, not a separate
score component.

These defaults and every other `ASTRO_OVERALL_*` calibration value are shared
with the ordinary Overall Astronomy Index rather than copied into a separate
Horizon profile. The cloud effective-radius inputs are shared as well. The
complete serialized calibration is part of Horizon cache identity, so a change
cannot reuse a PNG calculated under the preceding values.

The elevation-scaled anchors make this a comparison of forecast conditions at
the fixed 10-degree reference, not a claim that a 10-degree target is as sharp
as one at zenith. The PNG keeps the explicit elevation, and `SeeingArcsec`
retains the unnormalized slant value. Atmospheric dispersion, extinction, and
target/equipment-specific resolution remain outside the score.

Daylight, Moon/planet position, dew, Bortle class, aerosols, molecular Rayleigh
extinction, precipitation, and user equipment are not separate terms in [F20].
Daylight and twilight are only visual bands on the hourly heatmap. Rayleigh
optical depth is a
real low-elevation attenuation described by
[Bodhaine et al. (1999)](https://doi.org/10.1175/1520-0426(1999)016%3C1854:ORODC%3E2.0.CO;2),
but it is not penalized because every sector is evaluated at the same elevation
and the current product has no complete aerosol/extinction closure. Its omission
must not be interpreted as a transparency prediction.

### 7.6. Confidence is data quality, not probability

Let `R` be the set of fully resolved meteorological segments, let `L` be the
whole geometric path, and let `q_turb,j` and `q_cloud,j` be the bounded
vertical-closure weights described above. The reported quality components are

```math
\begin{aligned}
L&=\sum_j\Delta s_j,
&L_R&=\sum_{j\in\mathcal R}\Delta s_j,\\
P_{\mathrm{turb}}&=\frac{\sum_{j\in\mathcal R}q_{\mathrm{turb},j}\Delta s_j}{L},
&P_{\mathrm{cloud}}&=\frac{\sum_{j\in\mathcal R}q_{\mathrm{cloud},j}\Delta s_j}{L},\\
P_{\mathrm{path}}&=\min(P_{\mathrm{turb}},P_{\mathrm{cloud}}),\\[2pt]
C_{\mathrm{lead}}
&=\begin{cases}
\dfrac{\sum_{j\in\mathcal R}c_{\mathrm{ICON},j}\Delta s_j}{L_R},&L_R>0,\\
0,&L_R=0,
\end{cases}\\
R_{\mathrm{dir}}&=\frac{N_{\mathrm{available\ directions}}}{8},\\
\mathrm{Confidence}
&=\min\!\left(0.85,\;
0.50P_{\mathrm{path}}+0.30C_{\mathrm{lead}}+0.20R_{\mathrm{dir}}\right).
\end{aligned}\tag{F21}
```

If the direction is unavailable, reported `Confidence=0` regardless of the
remaining shared inputs; an unavailable row must never look highly confident.
If `L_R=0`, the direction is unavailable; no division result is exposed.
For a direction already vetoed by [F19], `P_path` instead reports the resolved
HHL-terrain share. In that branch `R` means segments with valid terrain HHL,
and a missing vertical confidence contributes zero to the lead-time numerator.
Cloud/turbulence completeness cannot change the veto and is therefore not
presented as if it had been evaluated beyond the obstruction.

The ICON lead-time input follows the project curve

```math
c_{\mathrm{ICON}}=\max\!\left(0.65,
0.96-0.26\frac{h_{\mathrm{forecast}}}{72}\right).
```

The bottom-strip category is driven primarily by the decline in
`C_{\mathrm{lead}}` with forecast lead time. Data are labelled good when
`C_{\mathrm{lead}}\ge0.85`, usable when
`0.75\le C_{\mathrm{lead}}<0.85`, and limited when
`C_{\mathrm{lead}}<0.75`. Composite `Confidence` is a conservative lower
bound: a value below `0.60` always downgrades the category to limited
regardless of lead time. An unavailable path is labelled missing. A complete
72-hour run therefore normally progresses from good early-run data through
usable to limited late-run data, while incomplete profile closure can only
downgrade the category.

The hard `0.85` ceiling on composite `Confidence` represents the lack of a
local DEM/obstacle model. These numbers express deterministic input
completeness, forecast lead time, and model resolution. They are **not** a
calibrated probability that the observation will succeed.

### 7.7. Validation and remaining limitations

Unit regressions cover spherical intersections and midpoints, exact surface
distances, dateline normalization, near-polar coordinates, exact-pole rejection,
full-footprint coverage, ECEF-to-local-ENU transverse-wind projection,
physical slant seeing/coherence scaling, elevation-adjusted reference mapping,
the bounded turbulence utility, step-size-stable cloud closure,
sparse-profile confidence penalties, common observer fog, terrain veto,
missing data, the `f000..f072` hourly series, raw-field three-hour interpolation
followed by metric recomputation, and ordered time-by-eight-direction output.

Every release validation runs one real calculation from a current ICON-EU run,
an explicit ICON Global case with neither button nor job, localized readable
rendering, callback/queue/cache tests, full Go
lint/race/build checks, and production latency comparison while an ordinary
forecast runs concurrently. Absolute skill still requires independent
observations such as DIMM/MASS/SCIDAR, all-sky cameras, visibility observations,
and observer logs. Until then the product is a reproducible model diagnostic,
not a measured horizon profile or an observatory safety decision.

The meteorological resolution, domain, and model top are documented on the
[DWD NWP forecast-data page](https://www.dwd.de/EN/ourservices/nwp_forecast_data/nwp_forecast_data.html)
and in the
[DWD ICON description](https://www.dwd.de/EN/research/weatherforecasting/num_modelling/01_num_weather_prediction_modells/icon_description.html).
The implementation stops at `22.3 km`, just below the documented ICON-EU top,
rather than extrapolating beyond the selected data contract. The observer-local
visibility input is a DWD diagnostic, not a direct optical-transmission
measurement; see the
[DWD visibility-method change note](https://www.dwd.de/DE/fachnutzer/forschung_lehre/numerische_wettervorhersage/nwv_aenderungen/_functions/DownloadBox_modellaenderungen/icon_d2/pdf_2024/pdf_icon_d2_23_04_2024.pdf?__blob=publicationFile&v=3).

## 8. Directional atmospheric Astrodome

### 8.1. Scope, sampling, and the non-interpolation rule

Astrodome extends the directional model diagnostic over the sky above an explicit
`10°` calculation boundary. It is available only from one complete, immutable
ICON-EU run and contains up to 72 consecutive native whole-hour frames. A fresh
run normally provides all 72; an ageing run stops before the first native
hourly cadence gap. Elevations
below 10° are absent. Zenith is calculated once per frame with `azimuth=null`;
assigning arbitrary bearings to the same physical direction is forbidden.

The three-dimensional browser may draw a contextual shell outside that data
domain: a labelled geometric horizon at `0°`, a twilight-coloured sky band
between `0°` and the `10°` calculation boundary, and a decorative terrain
panorama below `0°`. These elements contain no forecast nodes, are never
pickable as data, and do not interpolate or alter seeing, `tau0`, cloud
transmission, water vapour, data quality, or Overall. The terrain image is a
presentation asset, not ICON topography or an obstruction profile.

Dense storage selects `production-v2`: eight rings at
`10/20/30/40/50/60/70/80°`, each with 16 azimuths. The versioned storage
fallback `sparse-storage-v1` has 16 azimuths at
`10/20/30/45/60/75°`. Each adds one common zenith:

```math
\begin{aligned}
N_{\mathrm{production\text{-}v2,hour}}&=8\cdot16+1=129,
&N_{\mathrm{production\text{-}v2,total}}&=72\cdot129=9288,\\
N_{\mathrm{sparse,hour}}&=6\cdot16+1=97,
&N_{\mathrm{sparse,total}}&=72\cdot97=6984.
\end{aligned}
\tag{A1}
```

The profile is fixed before calculation and carried with
`astrosferum-grid-geometry-v1`, `spherical-voronoi-v1`, and a SHA-256 digest of
the canonical geometry descriptor. The former `dense-v1` 353-node geometry is
retained only for historical archives and calibration; it is not admitted for
a current production calculation.

For every nonlinear derived quantity `F`, the implementation computes

```math
F\!\left(\mathcal I_{\mathrm{raw}}
[P,T,q_v,q_l,q_i,\mathrm{CLC},U,V,W,\mathrm{TKE},\mathrm{HHL}]\right)
\quad\text{and never}\quad
\mathcal I\!\left[F(\cdot)\right].
\tag{A2}
```

Seeing, `tau0`, cloud transmission, Horizon/Sky/Overall indices, Shapley
contributions, and deterministic quality are therefore never spatially or
temporally interpolated.

### 8.2. ICON reference-sphere geometry

The calculation uses DWD ICON's reference sphere
`R_{\mathrm{ICON}}=6371229 m`. For elevation `e` and geographic azimuth `A`
clockwise from north, the local East–North–Up direction is

```math
\boldsymbol t_{\mathrm{ENU}}=
\begin{bmatrix}
\cos e\,\sin A\\
\cos e\,\cos A\\
\sin e
\end{bmatrix}.
\tag{A3}
```

After transformation to ECEF, the separately versioned straight compatibility
mode `astrodome-icon-sphere-straight-ray-v2` is

```math
\boldsymbol r(s)=\boldsymbol r_0+s\boldsymbol t_0,\qquad
h(s)=\lVert\boldsymbol r(s)\rVert-R_{\mathrm{ICON}}.
\tag{A4}
```

For a concentric target height `H`, let

```math
b=\boldsymbol r_0\mathbin{\boldsymbol\cdot}\boldsymbol t_0,\qquad
c=\lVert\boldsymbol r_0\rVert^2-(R_{\mathrm{ICON}}+H)^2.
```

The forward intersection is evaluated without subtracting two nearly
Earth-radius terms:

```math
s_H=\frac{-c}{b+\sqrt{b^2-c}}.
\tag{A5}
```

No plane-parallel `1/\sin e`, optical-airmass table, or multiplication of the
10° result is used to obtain another elevation.

The angular coordinate of the refraction product is explicitly
`apparent_at_aperture`: elevation and azimuth are the local launch direction
seen by an instrument at `H_SURF+2 m` on the ICON reference sphere, at the
declared wavelength `500 nm`. They are neither a WGS84-geodetic local frame nor
a vacuum/geometric celestial direction. The payload therefore records
`direction_coordinate=apparent_at_aperture`,
`direction_reference_surface=icon_sphere_hsurf_plus_2m`,
`direction_reference_wavelength_m=5e-7`, and
`vacuum_direction_available=false`. The cell inspector labels this coordinate
as **apparent altitude at aperture, 500 nm (ICON sphere)** rather than as a
generic geometric altitude. The separately versioned straight
compatibility path uses the same numerical launch angles without refraction;
neither mode performs inverse shooting to a requested direction above model
top.

### 8.3. Moist-air refraction

The full geometry version is
`astrodome-icon-sphere-refraction-full-ciddor-dopri54-v3`. The phase index at
500 nm is evaluated from native `P,T,q_v` using Ciddor's published moist-air
density formulation and BIPM 1981/91 compressibility. Specific humidity is
converted algebraically to water-vapour mole fraction; relative humidity is
not introduced. The fixed, versioned carbon-dioxide assumption is 425 ppm.

Let $M_w=0.018015\ \mathrm{kg\,mol^{-1}}$, let $x_c$ be the fixed carbon-dioxide
content in ppm, and let
$M_a=10^{-3}[28.9635+12.011\times10^{-6}(x_c-400)]\ \mathrm{kg\,mol^{-1}}$.
The exact mass-fraction-to-mole-fraction conversion used by the implementation
is

```math
\begin{aligned}
x_w
&=\frac{q_v/M_w}{q_v/M_w+(1-q_v)/M_a}
=\frac{q_vM_a}{M_w+q_v(M_a-M_w)}.
\end{aligned}
\tag{A5a}
```

For $t_C=T-273.15\ \mathrm K$, pressure $P$ in Pa and temperature $T$ in K,
the BIPM 1981/91 compressibility evaluated in code is

```math
\begin{aligned}
Z(P,T,x_w)={}&1-\frac PT\Bigl[
1.58123\times10^{-6}-2.9331\times10^{-8}t_C
+1.1043\times10^{-10}t_C^2\\
&+(5.707\times10^{-6}-2.051\times10^{-8}t_C)x_w
+(1.9898\times10^{-4}-2.376\times10^{-6}t_C)x_w^2\Bigr]\\
&+\left(\frac PT\right)^2
\left(1.83\times10^{-11}-0.765\times10^{-8}x_w^2\right).
\end{aligned}
\tag{A5b}
```

Writing $\lambda_\mu=\lambda/(1\ \mu\mathrm m)$ and
$\sigma=1/\lambda_\mu$, the Ciddor density-index closure is

```math
\begin{aligned}
r_{as}&=10^{-8}\left[
\frac{5792105}{238.0185-\sigma^2}
+\frac{167917}{57.362-\sigma^2}\right],\\
r_{axs}&=r_{as}\left[1+0.534\times10^{-6}(x_c-450)\right],\\
r_{ws}&=1.022\times10^{-8}
\left(295.235+2.6422\sigma^2-0.032380\sigma^4
+0.004028\sigma^6\right),\\
\rho_a&=\frac{PM_a(1-x_w)}{ZRT},&
\rho_w&=\frac{PM_wx_w}{ZRT},\\
Z_{as}&=Z(101325,288.15,0),&
\rho_{axs}&=\frac{101325M_a}{Z_{as}R(288.15)},\\
Z_{ws}&=Z(1333,293.15,1),&
\rho_{ws}&=\frac{1333M_w}{Z_{ws}R(293.15)},\\
n&=1+\frac{\rho_a}{\rho_{axs}}r_{axs}
+\frac{\rho_w}{\rho_{ws}}r_{ws},
\qquad R=8.314510\ \mathrm{J\,mol^{-1}\,K^{-1}}.
\end{aligned}
\tag{A5c}
```

The implemented dual-number derivatives are analytic first derivatives of
this same expression with respect to $P,T,q_v$; they do not differentiate an
independent approximation. The declared implementation range is
$230\le\lambda\le1690\ \mathrm{nm}$, while the present product fixes
$\lambda=500\ \mathrm{nm}$.

The aperture is anchored at `H_{\mathrm{SURF}}+2 m`. Native surface pressure
is transported to this anchor by the declared two-metre hydrostatic closure

```math
\begin{aligned}
R_{\mathrm{mix}}&=(1-q_{v,2})R_d+q_{v,2}R_v,\\
P_2&=P_s\exp\!\left[-\frac{g_0(2\ \mathrm m)}
{R_{\mathrm{mix}}T_2}\right],
\qquad g_0=9.80665\ \mathrm{m\,s^{-2}}.
\end{aligned}
\tag{A6}
```

This boundary is versioned as
`icon-ps-t2m-qv2m-hydrostatic-2m-v1`. `PMSL` and dew point are not substitutes
for `PS` and `QV_2M`.

With geometric path length `s`, unit tangent `\boldsymbol t`, refractive index
`n(\boldsymbol r)`, and optical path `\mathcal L`, the implemented coupled ray
equations are

```math
\begin{aligned}
\frac{d\boldsymbol r}{ds}&=\boldsymbol t,\\
\frac{d\boldsymbol t}{ds}
&=\frac{\nabla n-\boldsymbol t
(\boldsymbol t\mathbin{\boldsymbol\cdot}\nabla n)}{n},\\
\frac{d\mathcal L}{ds}&=n.
\end{aligned}
\tag{A7}
```

Analytic derivatives of the Ciddor expression are combined with analytic ECEF
gradients of the reconstructed `P,T,q_v` fields. Adaptive Dormand–Prince 5(4)
with continuous extension locates model-top, terrain, and interpolation
partition events. The calibration declares relative tolerance `10^{-9}`;
`10^{-4} m` absolute position/optical-path tolerances; `10^{-11} rad`
direction tolerance; `10^{-2} m` event tolerance; an initial `25 m` step
within `10^{-4}…1000 m`; `500 km` maximum path; and at most `10^6` steps. A
convergence controller starts with tolerance scales `1` and `1/2`. Adjacent forward
endpoints must agree within $0.02\ \mathrm{m}$,
$10^{-8}\ \mathrm{rad}$, and $0.02\ \mathrm{m}$ of optical path. If the
forward pair fails, the local RK and event-localisation tolerances are both
halved through the bounded sequence `1/4`, `1/8`, `1/16`, `1/32`, `1/64`,
`1/128`; the
acceptance bounds themselves are never relaxed. Thus the event tolerance
reaches $7.8125\times10^{-5}\ \mathrm{m}$ at the finest pass. Event roots are
located on the accepted Shampine dense extension independently of the RK
minimum step; the latter is instead required to clear the finest internal
partition-transition scale $25/128=0.1953125\ \mathrm{m}$. Production
refraction v3 publishes
only after two neighbouring forward passes converge. Reference/strict
verification additionally launches a reverse pass from the finer endpoint and
requires return within $0.05\ \mathrm{m}$ and
$5\times10^{-9}\ \mathrm{rad}$. Reverse closure is therefore a release and
regression diagnostic, not work repeated for every production ray. Failure of
the checks required by the selected mode rejects the ray as numerically
non-converged.

The returned direction is explicitly `direction_at_icon_top`. ICON model top
is not vacuum, so this version does not claim a complete astrometric
apparent-to-vacuum correction above it.

Primary sources: Ciddor,
[DOI 10.1364/AO.35.001566](https://doi.org/10.1364/AO.35.001566);
Auer and Standish,
[DOI 10.1086/301325](https://doi.org/10.1086/301325); van der Werf,
[DOI 10.1364/AO.42.000354](https://doi.org/10.1364/AO.42.000354).
The embedded Runge--Kutta pair follows Dormand and Prince,
[DOI 10.1016/0771-050X(80)90013-3](https://doi.org/10.1016/0771-050X(80)90013-3),
and its quartic continuous extension follows Shampine,
[DOI 10.1090/S0025-5718-1986-0815836-3](https://doi.org/10.1090/S0025-5718-1986-0815836-3).

### 8.4. Reconstruction of native primitives

The reconstruction order is part of the scientific contract:
**time interpolation of raw native levels -> bilinear interpolation of the
corresponding HHL geometry and raw variables -> vertical interpolation in the
resulting local terrain-following column -> complete nonlinear recomputation**.
Changing that order would reconstruct four different absolute-height columns
before mixing them and would be physically wrong where model levels follow
sloping terrain.

For a raw primitive `x` at support column `i` and native level `k`, native
times `t_0<t_1` separated by no more than three hours are interpolated first:

```math
\begin{aligned}
\alpha&=\frac{t-t_0}{t_1-t_0},\\
\widetilde{x}_{i,k}(t)
&=(1-\alpha)x_{i,k}(t_0)+\alpha x_{i,k}(t_1).
\end{aligned}
\tag{A8}
```

At an exact native time the stored value is used directly. There is no time
extrapolation, nearest-time substitution, or cross-run bracket. Let
`u,v\in[0,1]` be the eastward and northward coordinates in the regular-grid
cell and let `\mathcal{S}={\mathrm{SW},\mathrm{SE},\mathrm{NW},\mathrm{NE}}`.
The four bilinear weights and the corresponding-level reconstruction are

```math
\begin{aligned}
w_{\mathrm{SW}}&=(1-u)(1-v),&
w_{\mathrm{SE}}&=u(1-v),\\
w_{\mathrm{NW}}&=(1-u)v,&
w_{\mathrm{NE}}&=uv,\\
Z_k(u,v)&=\sum_{i\in\mathcal{S}}w_i(u,v)Z_{i,k},&
x_k(u,v,t)&=\sum_{i\in\mathcal{S}}w_i(u,v)
\widetilde{x}_{i,k}(t),\\
\partial_a Z_k&=\sum_{i\in\mathcal{S}}(\partial_a w_i)Z_{i,k},&
\partial_a x_k&=\sum_{i\in\mathcal{S}}(\partial_a w_i)
\widetilde{x}_{i,k}(t),\qquad a\in\{u,v\}.
\end{aligned}
\tag{A9}
```

CDO `gennn` is not the scientific interpolation operator in (A9). Before any
extraction, the implementation reads the HHL source-grid metadata from the
GRIB and requires the exact regular ICON-EU dimensions, endpoints, increments,
and scan flags recorded by the immutable manifest. Every GRIB message in a
source file is inspected; mixed geometry or scanning order is rejected. Every
extraction target is then constructed from an integer native
`(latitude_index, longitude_index)`; no ray coordinate is passed to CDO.
After `gennn`, the generated SCRIP file must contain exactly one link per
target, the expected canonical one-based source address, the target's
one-based destination address, and a weight whose binary64 value is exactly
`1`. Every meteorological GRIB subsequently remapped with those weights must
reproduce the same proven grid descriptor as the HHL source. Thus
`gennn -> remap` collects only the exact union of native support columns
required by all four-point stencils. The sorted native-index/coordinate plan
is validated for uniqueness and exact binary64 agreement with the manifest
grid and is persisted as `source_column_plan_digest=sha256:...`. An opt-in
manufactured-grid regression runs the installed production CDO, encodes the
native source index in each field value, and verifies the same address and
unit-weight contract. The project-owned bilinear evaluation in (A9) is the
sole horizontal interpolation used by the physical model.

Here `Z_{i,k}` is the HHL geometric height for a half-level field. For a
full-level field, the two bounding HHL surfaces are each reconstructed
horizontally before their local arithmetic midpoint is formed:

```math
Z_k^{\mathrm{full}}(u,v)
=\frac12\left[
\sum_{i\in\mathcal S}w_iZ_{i,k-1/2}
+\sum_{i\in\mathcal S}w_iZ_{i,k+1/2}
\right].
\tag{A9a}
```

The implementation deliberately does not precompute four corner midpoints and
then mix them: although the two expressions are algebraically equal over the
reals, their binary64 operation order can differ at a WMO sign boundary. The
path planner and the science kernel use the order in (A9a). HHL is immutable
within the run; only raw meteorological variables undergo (A8). Horizontal
wind at each support is rotated into the common ECEF frame before it is
combined.

At fixed absolute geometric height `z`, the local column supplies lower and
upper anchors `Z_{\mathrm{l}}<z<Z_{\mathrm{u}}`. With
`\Delta Z=Z_{\mathrm{u}}-Z_{\mathrm{l}}`, the exact moving-level interpolant and
its derivatives are

```math
\begin{aligned}
\beta&=\frac{z-Z_{\mathrm{l}}}{\Delta Z},&
\partial_a\beta
&=-\frac{(1-\beta)\partial_a Z_{\mathrm{l}}
+\beta\partial_a Z_{\mathrm{u}}}{\Delta Z},\\
x&=(1-\beta)x_{\mathrm{l}}+\beta x_{\mathrm{u}},&
\partial_a x
&=(1-\beta)\partial_a x_{\mathrm{l}}
+\beta\partial_a x_{\mathrm{u}}
+(x_{\mathrm{u}}-x_{\mathrm{l}})\partial_a\beta,\\
\partial_z\beta&=\frac{1}{\Delta Z},&
\partial_z x&=\frac{x_{\mathrm{u}}-x_{\mathrm{l}}}{\Delta Z},
\qquad a\in\{u,v\}.
\end{aligned}
\tag{A10}
```

Thus the horizontal derivatives used by refraction are taken at fixed
absolute height, not at fixed model-level index. Omitting the
`\partial_a\beta` terms would discard the slope of both terrain-following
anchors. The analytic geographic derivatives are subsequently transformed to
ECEF gradients.

Pressure alone is vertically log-linear. Writing
`\ell_P=\ln P` gives the separately implemented pressure equations

```math
\begin{aligned}
\ell_P&=(1-\beta)\ln P_{\mathrm{l}}
+\beta\ln P_{\mathrm{u}},&
P&=\exp(\ell_P),\\
\partial_a\ell_P
&=(1-\beta)\frac{\partial_aP_{\mathrm{l}}}{P_{\mathrm{l}}}
+\beta\frac{\partial_aP_{\mathrm{u}}}{P_{\mathrm{u}}}
+(\ln P_{\mathrm{u}}-\ln P_{\mathrm{l}})\partial_a\beta,&
\partial_aP&=P\,\partial_a\ell_P,\\
\partial_z\ell_P&=
\frac{\ln P_{\mathrm{u}}-\ln P_{\mathrm{l}}}{\Delta Z},&
\partial_zP&=P\,\partial_z\ell_P,
\qquad a\in\{u,v\}.
\end{aligned}
\tag{A11}
```

`T,q_v,q_l,q_i,C,U,V,W,\mathrm{TKE}` use (A10); `U,V` use full levels and
`W,TKE` use half levels. The outer HHL half-cell retains the nearest native
representative value instead of inventing a vertical gradient.

The physical coarse-model surface is reconstructed from the same stencil,
not selected as the highest support corner:

```math
H_s(u,v)=\sum_{i\in\mathcal{S}}w_i(u,v)\,\mathrm{HSURF}_i,
\qquad H_2=H_s+2\ \mathrm m.
```

The bottom HHL must agree with `H_s`. `PS` is anchored at `H_s`; `T_2` and
`q_{v,2}` define the surface-to-2 m closure, and (A6) transports pressure to
`H_2`. Inside that two-metre interval, `T` and `q_v` remain at their 2 m
values and pressure follows the same hydrostatic exponential. From `H_2` to
the lowest native full level, the local-column interpolation above is applied.

Only `P,T,q_v` have narrow continuous numerical extensions immediately below
bilinear `H_s` and immediately above model-top HHL. They exist solely so the
ODE event locator can bracket terrain and model-top roots: pressure follows
the declared hydrostatic closure while `T` and `q_v` retain the boundary
value. No cloud, wind, turbulence, transmission, seeing, `tau0`, or Overall is
extrapolated there. Scientific quadrature never integrates below the bilinear
terrain or above model top; a terrain crossing is returned as a blocked path,
not as atmosphere.

After the local raw state is reconstructed, density, refractive index,
turbulence, cloud extinction and overlap, seeing, `tau0`, and Overall are all
recomputed from that state. None of these nonlinear products is interpolated.
Every reconstructed evaluation is then rejected unless `P>0`, `T>0`,
`0<=q_v<1`, `q_l>=0`, `q_i>=0`, `q_v+q_l+q_i<1`, `0<=CLC<=1`, `TKE>=0`, and
all wind components, vertical derivatives, refractive state, and spatial
gradients are finite. Before reconstruction, native full-level pressure must
strictly increase in stored top-to-surface order and native surface pressure
`PS` must be strictly greater than the lowest full-level pressure. After
log-linear reconstruction, upward geometric height must satisfy `dP/dz < 0`
in `Pa/m`; zero or positive values are rejected. These are provider-data and
physical-coordinate invariants, not score calibration. They complement the
post-decode and post-interpolation fail-closed checks.

The immutable source identity contains provider, product, grid, run base time,
run ID, manifest digest, and `astrodome-icon-primitives-v2`. Any identity
change during reconstruction invalidates the node.

The separate calculation-request schema is v3. It carries the fixed science
and path versions, the apparent-direction contract, and
`science_calibration_sha256`: SHA-256 of the canonical JSON serialization of
the complete validated `AstrodomeScienceCalibration`. The same configured
calibration is injected into the bot and isolated worker and the digest is
stored in every current dataset. A supported coefficient change therefore
changes the cache identity; a bot/worker digest mismatch is unavailable rather
than silently using either side's defaults.

The vertical-coordinate basis is the official
[DWD ICON Database Reference Manual](https://www.dwd.de/DWD/forschung/nwv/fepub/icon_database_main.pdf),
which documents HHL geometric model half levels and their relation to model
topography, together with the
[DWD ICON tutorial](https://www.dwd.de/EN/ourservices/nwp_icon_tutorial/pdf_volume/icon_tutorial2020_en.pdf?__blob=publicationFile&v=9).

### 8.5. Joint line-of-sight physics

For reconstructed mass fractions `q_v,q_l,q_i`,

```math
\begin{aligned}
T_v&=T\left[1+\left(\frac{R_v}{R_d}-1\right)q_v-q_l-q_i\right],\\
\rho&=\frac{P}{R_dT_v},
\qquad R_d=287.05,\quad R_v=461.5\\
\mathrm{J\,kg^{-1}\,K^{-1}}.
\end{aligned}
\tag{A12}
```

The dynamic boundary-layer top is

```math
h_{\mathrm{PBL}}=H_{\mathrm{SURF}}+
\mathrm{clamp}(MH,500\ \mathrm m,2000\ \mathrm m).
\tag{A13}
```

Below it, the Masciadri TKE kernel is evaluated pointwise; above it, HMNSP99's
tropospheric/stratospheric branch uses the local reconstructed primitives and
the thermal-tropopause diagnosis. The formulae and provenance are exactly
Sections 3.1 and 4.2–4.4; Astrodome changes the integration path, not these
kernels.

For local ECEF wind `\boldsymbol V`,

```math
\boldsymbol V_\perp=
\boldsymbol V-(\boldsymbol V\mathbin{\boldsymbol\cdot}\boldsymbol t)
\boldsymbol t.
\tag{A14}
```

Five components are integrated jointly along the actual straight or refracted
path:

```math
\begin{aligned}
J&=\int C_n^2\,ds,\\
J_V&=\int C_n^2\lVert\boldsymbol V_\perp\rVert^{5/3}\,ds,\\
W_{\mathrm{slant}}&=\int\rho q_v\,ds,\\
\tau_l&=\int\frac{3Q_{\mathrm{ext},l}\rho q_l}
{4\rho_l r_{\mathrm{eff},l}}\,ds,\\
\tau_i&=\int\frac{3Q_{\mathrm{ext},i}\rho q_i}
{4\rho_i r_{\mathrm{eff},i}}\,ds.
\end{aligned}
\tag{A15}
```

The cloud constants are

```math
\begin{aligned}
Q_{\mathrm{ext},l}&=2.0,&\rho_l&=1000\ \mathrm{kg\,m^{-3}},
&r_{\mathrm{eff},l}&=10\ \mu\mathrm m,\\
Q_{\mathrm{ext},i}&=2.1,&\rho_i&=916.7\ \mathrm{kg\,m^{-3}},
&r_{\mathrm{eff},i}&=25\ \mu\mathrm m.
\end{aligned}
\tag{A16}
```

`W_{\mathrm{slant}}` is in `kg m^{-2}`, numerically equal to millimetres of
liquid-water equivalent. It is exposed as a physical diagnostic but does not
enter generic Overall. Its relevance depends on passband, molecular
absorption, source spectrum, and instrument; a universal PWV penalty would be
less scientific than the actual slant column. Target/passband radiative
transfer remains a separate future product and is not silently approximated in
the generic score.

At `\lambda=500 nm`,

```math
\begin{aligned}
k&=\frac{2\pi}{\lambda},&
r_0&=\left(0.423\,k^2J\right)^{-3/5},\\
\varepsilon_{\mathrm{arcsec}}
&=206264.80624709636\,\frac{0.98\lambda}{r_0},&
\tau_0&=\left(2.910\,k^2J_V\right)^{-3/5}.
\end{aligned}
\tag{A17}
```

The unexpanded published `2.910` temporal phase-structure coefficient avoids a
second rounding through the common `0.058` shorthand. If `J_V` is
indistinguishable from zero at the numerical scale, `tau0` is reported as
unbounded above with an explicit state rather than JSON infinity.

Primary sources: Fried,
[DOI 10.1364/JOSA.56.001372](https://doi.org/10.1364/JOSA.56.001372);
Greenwood,
[DOI 10.1364/JOSA.67.000390](https://doi.org/10.1364/JOSA.67.000390);
Kellerer and Tokovinin,
[DOI 10.1051/0004-6361:20065788](https://doi.org/10.1051/0004-6361:20065788);
Masciadri et al.,
[DOI 10.1051/aas:1999474](https://doi.org/10.1051/aas:1999474); Wu et al.,
[DOI 10.1093/mnras/stab515](https://doi.org/10.1093/mnras/stab515); Cuevas et
al., [DOI 10.1093/mnras/stae630](https://doi.org/10.1093/mnras/stae630).

### 8.6. Directional cloud closure

A cloud block is the unique pair `(native horizontal cell, AGL tier)`, with
tier boundaries at 2 and 7 km AGL. Its path domain $\Gamma_b$ is the union of
the atomic intervals carrying that exact block identity. Optical depth is
allocated normatively while the original extinction integrands are evaluated,
not apportioned from a finished line-of-sight total:

```math
\begin{aligned}
\tau_{l,b}^{(0)}
&=\sum_{p\subset\Gamma_b}\int_p
\frac{3Q_{\mathrm{ext},l}\rho q_l}
{4\rho_l r_{\mathrm{eff},l}}\,ds,\\
\tau_{i,b}^{(0)}
&=\sum_{p\subset\Gamma_b}\int_p
\frac{3Q_{\mathrm{ext},i}\rho q_i}
{4\rho_i r_{\mathrm{eff},i}}\,ds,\\
E_{\tau_l,b}&=\sum_{p\subset\Gamma_b}e_{\tau_l,p},&
E_{\tau_i,b}&=\sum_{p\subset\Gamma_b}e_{\tau_i,p},\\
\tau_b^{(0)}&=\tau_{l,b}^{(0)}+\tau_{i,b}^{(0)},&
\tau_b^{(+)}&=\max\!\left(0,
\tau_b^{(0)}+E_{\tau_l,b}+E_{\tau_i,b}\right).
\end{aligned}
\tag{A18}
```

Here $e_{\tau_l,p}$ and $e_{\tau_i,p}$ are the accepted embedded-rule
component estimators for that panel. They are numerical allowances, not
probabilistic uncertainty intervals.

Within each atomic interval $p$, let $S_p$ be its four native horizontal
stencil columns, $T_p$ the one or two distinct endpoints of its native
temporal bracket, and $K_p$ its exact active vertical CLC support: either one
adjacent bottom-to-top full-level pair or the single constant top/bottom
extension level. The nominal diagnostic cover and strict raw-support envelope
are distinct:

```math
\begin{aligned}
C_{p,\max}
&=\max_{i\in S_p,\,t\in T_p,\,k\in K_p}
\mathrm{CLC}^{\mathrm{raw}}_{i,k,t},\\
\eta_p
&=\max\!\left(0,\sum_{i\in S_p}\widehat w_{p,i}-1\right)+256u_q,
\qquad u_q=2^{-53},\\
\overline C_p
&=\begin{cases}
0,&C_{p,\max}=0,\\
\min\!\left(1,\mathrm{nextup}\!\left[C_{p,\max}(1+\eta_p)\right]\right),
&C_{p,\max}>0,
\end{cases}\\
C_b^{(0)}&=\max_{s\in\Xi_b}C_{\mathrm{reconstructed}}(s),&
C_b^{(+)}&=\max_{p\subseteq\Gamma_b}\overline C_p.
\end{aligned}
\tag{A18a}
```

Native CLC is validated in $[0,1]$. Temporal, bilinear-horizontal, and
within-support vertical reconstruction are convex; consequently
$\overline C_p$ bounds every reconstructed CLC value in that active support
even though the refracted trajectory is nonlinear in path length. The bound
is deliberately conservative across the four columns and temporal bracket,
but it uses only the active adjacent pair or extension level, not an entire
tier or column. The vertical-support identity is part of the partition
signature and envelope-cache key. Path v22 must certify every native
full-level support transition and every reachable raw WMO predicate before
quadrature; a missing certificate or sampled change fails closed rather than
triggering a heuristic kernel split.

$\Xi_b$ contains the actual higher/lower quadrature nodes and the one-sided
atomic-interval endpoint probes used by the accepted calculation. Thus
$C_b^{(0)}$ is a reproducible sampled nominal diagnostic, not a claimed bound
on an unsampled nonlinear ray segment. Conversely, $C_b^{(+)}$ is the
certified conservative raw-support envelope. The payload publishes both and
never labels the latter as nominal.

For either pair $(C,\tau)=(C_b^{(0)},\tau_b^{(0)})$ or
$(C_b^{(+)},\tau_b^{(+)})$, define the same exact effective-cover function

```math
\begin{aligned}
C_{\mathrm{cond}}(\tau)&=0&&\text{if }\tau<10^{-9},\\
C_{\mathrm{cond}}(\tau)&=
\min\!\left[1,\max\!\left(0.01,-\mathrm{expm1}(-\tau)\right)\right]
&&\text{if }\tau\ge10^{-9},\\
C_{\mathrm{eff}}(C,\tau)&=\max\!\left(C,C_{\mathrm{cond}}(\tau)\right),\\
T_b(C,\tau)&=1&&\text{if }C_{\mathrm{eff}}=0,\\
T_b(C,\tau)&=(1-C_{\mathrm{eff}})
+C_{\mathrm{eff}}\exp\!\left(-\frac{\tau}{C_{\mathrm{eff}}}\right)
&&\text{if }C_{\mathrm{eff}}>0.
\end{aligned}
\tag{A19}
```

The nominal and conservative block transmissions are
$T_b^{(0)}=T_b(C_b^{(0)},\tau_b^{(0)})$ and
$T_b^{(+)}=T_b(C_b^{(+)},\tau_b^{(+)})$. The explicit zero branch prevents
division by zero. Applying the condensate-derived lower bound through `max`
for every declared cover avoids a small-cover branch discontinuity. Since
$T_b=(1-C)+C\exp(-\tau/C)$ is non-increasing in effective cover and optical
depth for $C>0$, increasing either the CLC envelope or the optical-depth error
cannot improve conservative transmission or Overall. The `0.01` and `10^{-9}`
guards are versioned project closures for an inconsistent state with non-zero
condensate but vanishing CLC; they are not published cloud microphysics.

Independent-block random overlap is accumulated in log space:

```math
T_{\mathrm{cond}}^{(r)}=\prod_bT_b^{(r)}
=\exp\!\left(\sum_b\mathrm{log1p}(T_b^{(r)}-1)\right),
\qquad r\in\{0,+\}.
\tag{A20}
```

If any block has exactly zero transmission, the product is set to zero and the
closure state is `opaque`; its logarithm is never evaluated. Otherwise the
implemented accumulation uses `log1p(T_b-1)` as written in (A20), which avoids
loss of relative accuracy for nearly transparent blocks.

The unresolved-cover guard and final closure are

```math
\begin{aligned}
C_j^{(r)}&=\max_{b:\,\mathrm{tier}(b)=j} C_b^{(r)},
\qquad j\in\{\mathrm{low,mid,high}\},\\
T_{\mathrm{guard}}^{(r)}&=
\prod_{j\in\{\mathrm{low,mid,high}\}}\left(1-g_jC_j^{(r)}\right),\\
(g_{\mathrm{low}},g_{\mathrm{mid}},g_{\mathrm{high}})
&=(0.45,0.2475,0.081),\\
T_{\mathrm{cloud}}^{(r)}&=\min(T_{\mathrm{cond}}^{(r)},T_{\mathrm{guard}}^{(r)}),
\qquad r\in\{0,+\}.
\end{aligned}
\tag{A21}
```

For a tier with no blocks the implementation defines $C_j^{(r)}=0$.
`cloud_transmission_nominal` publishes $T_{\mathrm{cloud}}^{(0)}$;
`cloud_transmission_conservative` publishes $T_{\mathrm{cloud}}^{(+)}$.
The legacy internal/display alias `effective_cloud_transmission` is exactly the
conservative value, and directional Overall uses only that conservative value.

Guard coefficients and fixed effective radii are versioned project closures,
not universal cloud microphysics. Extinction basis: Stephens,
[DOI 10.1175/1520-0469(1978)035%3C2123:RPIEWC%3E2.0.CO;2](https://doi.org/10.1175/1520-0469(1978)035%3C2123:RPIEWC%3E2.0.CO;2).
Overlap basis: Hogan and Illingworth,
[DOI 10.1002/qj.49712656914](https://doi.org/10.1002/qj.49712656914).

### 8.7. Directional Overall and data quality

Let $I_{H,j}$ be the accumulated higher-order estimate of integral component
$j$ from the accepted production pass, and let $e_{\mathrm{emb},j}$ be the
accumulated componentwise selected-rule estimator. Depending on certified
child-panel length and endpoint ownership, it is the augmented G7/K15
difference or the independent GL5/GL3, GL3/GL2, or GL2/GL1 difference. A
physical panel below the GL2/GL1 sampling floor uses the separately labelled
limited midpoint rule in Section 8.8; no extrapolatory moment-fitted rule is
present. Production defines

```math
\begin{aligned}
E_j^{(\mathrm{prod})}&=e_{\mathrm{emb},j},\\
J_+&=\max(0,J_H+E_J^{(\mathrm{prod})}),&
J_{V,+}&=\max(0,J_{V,H}+E_{J_V}^{(\mathrm{prod})}),\\
\varepsilon_{\mathrm{score}}&=\varepsilon(J_+),&
\tau_{0,\mathrm{score}}&=\tau_0(J_{V,+}).
\end{aligned}
\tag{A21a}
```

Reference/calibration mode repeats the integration at half tolerance. With
coarse and fine results $I_{c,j},I_{f,j}$ and the fine selected-rule estimator
$e_{f,j}$, its diagnostic allowance is

```math
E_j^{(\mathrm{ref})}=e_{f,j}+\left|I_{f,j}-I_{c,j}\right|.
\tag{A21b}
```

The repeat is required for regression, calibration, and release verification,
but not for an ordinary production node. Each $E_j$ is an engineering
numerical allowance, not a rigorous confidence interval. Published nominal
production diagnostics use $J_H$ and $J_{V,H}$; their reported seeing range
uses $J_H\pm E_J^{(\mathrm{prod})}$, while Overall uses only the conservative
$\varepsilon_{\mathrm{score}}$ and $\tau_{0,\mathrm{score}}$. If
$J_{V,H}-E_{J_V}^{(\mathrm{prod})}$ is at the declared numerical floor,
nominal `tau0` is reported as calm/unbounded, but the score still uses $J_{V,+}$
whenever it is positive. The cloud-transmission bound is propagated per exact
cell/tier block from that block's accumulated liquid and ice errors; the
overall error bound is then evaluated from these componentwise bounds rather
than by applying one aggregate perturbation to the finished product.

The physical seeing and `tau0` enter the same bounded target-agnostic utility
as ordinary Overall:

```math
\begin{aligned}
q_\varepsilon&=
\mathrm{clamp}\!\left(
\frac{\ln(\varepsilon_{\mathrm{bad}}/\varepsilon_{\mathrm{score}})}
{\ln(\varepsilon_{\mathrm{bad}}/\varepsilon_{\mathrm{best}})},0,1\right),\\
q_\tau&=
\mathrm{clamp}\!\left(
\frac{\ln(\tau_{0,\mathrm{score}}/\tau_{\mathrm{bad}})}
{\ln(\tau_{\mathrm{best}}/\tau_{\mathrm{bad}})},0,1\right),\\
q_{\mathrm{raw}}&=
q_\varepsilon^{w_\varepsilon}
\left[1-w_\tau(1-q_\tau)\right],\\
f_{\mathrm{turb}}&=
1-p_{\mathrm{turb}}
\left[1-\mathrm{clamp}(q_{\mathrm{raw}},0,1)\right].
\end{aligned}
\tag{A22}
```

In (A22), the two wrapped line continuations are multiplicative in the
implementation:
`q_raw = pow(q_epsilon,w_epsilon) * [1-w_tau(1-q_tau)]` and
`f_turb = 1-p_turb*[1-clamp(q_raw,0,1)]`. This plain-text restatement is
normative because it avoids any renderer ambiguity. Defaults are
`w_epsilon=1`, `w_tau=0.25`, `p_turb=0.25`,
`epsilon_best=0.5 arcsec`, `epsilon_bad=2.0 arcsec`,
`tau_bad=1.6 ms`, and `tau_best=5.2 ms`.

With the surface-wind and fog closures from Section 3.3,

```math
\begin{aligned}
f_{\mathrm{cloud}}&=\left(T_{\mathrm{cloud}}^{(+)}\right)^{w_{\mathrm{cloud}}},\\
q_{\mathrm{precip}}&=[R_{1h}<0.05\ \mathrm{mm}],\\
Q&=f_{\mathrm{turb}}f_{\mathrm{cloud}}
q_{\mathrm{surface}}q_{\mathrm{fog}}q_{\mathrm{precip}},\\
\mathrm{Overall}&=1+9\,\mathrm{clamp}(Q,0,1).
\end{aligned}
\tag{A23}
```

Precipitation is an operational veto, not a fitted intensity curve. Dew,
light pollution, and generic PWV remain excluded. Shapley values decompose the
exact multiplicative loss; they add no second penalty.

Data quality is deterministic metadata, not a success probability. Forecast
lead contributes

```math
C_{\mathrm{lead}}=
0.96-0.26\,\mathrm{clamp}\!\left(\frac{h_{\mathrm{lead}}}{72},0,1\right).
\tag{A24}
```

An available, top-closed, numerically converged path with complete spatial
coverage and hourly temporal resolution is `good` for
`C_lead >= 0.85`, `usable` for `0.75 <= C_lead < 0.85`, and `limited` below
0.75. Any accepted short-path approximation forces `limited` and
`quadrature_converged=false`, independently of lead time. Its accumulated
length is serialized as `approximation_length_m` and its reason set contains
`short_path_approximation`. Missing mandatory geometry, turbulence, cloud,
temporal brackets, terrain, or top closure gives `unavailable`. A terrain
intersection is a separate physical state and is never published as Overall 1.

### 8.8. Numerical method, versions, and limitations

The vector in (A15) is integrated jointly by non-extrapolatory positive-weight
regimes: adaptive embedded Gauss–Kronrod G7/K15 on ordinary panels and
independent Gauss–Legendre GL5/GL3, GL3/GL2, or GL2/GL1 on successively shorter
physical panels. Physical-event and adaptive numerical endpoints are distinct
metadata. A physical interval at or below the GL2 sampling floor is evaluated
only by the positive midpoint fallback defined below: it is neither replaced
by zero nor inferred from nodes outside its certified safe domain.
The 1-cm floor applies only to adaptively created numerical panels. A rejected
smooth panel may reuse recursive tolerance subdivision only while both
represented children remain strictly above their applicable floors.
Intervals split at native horizontal/HHL, PBL, thermal-tropopause, cloud-tier,
terrain, and model-top events. Path contract v23 must isolate every reachable
raw WMO decision breakpoint before integration; any physical-partition
signature mismatch observed by quadrature fails closed. When the WMO
thermal diagnosis has no finite result, the `P<200 hPa` HMNSP99 fallback is
not left as a pointwise branch inside quadrature: its geometric boundary is
reconstructed by the same log-pressure interpolation as (A11), and that
boundary is added to the path partition. Let $I_{H,j}$ be the higher-order
estimate and let $e_{j,\mathrm{panel}}$ denote its G7/K15 or selected GL5/GL3,
GL3/GL2, or GL2/GL1
higher/lower difference. Component `j` is accepted only when

```math
e_{j,\mathrm{panel}}
\le a_j+r\lvert I_{H,j}\rvert,
\qquad r=10^{-3},
\tag{A25}
```

with

```math
(a_J,a_{J_V},a_W,a_{\tau_l},a_{\tau_i})
=(10^{-20},10^{-19},10^{-6},10^{-8},10^{-8}).
\tag{A26}
```

The accumulated embedded error over all accepted panels must separately
satisfy the same absolute-plus-relative budget; local acceptance is not
sufficient. This accepted pass is the complete production science integration.
Reference mode subsequently performs the independent half-tolerance repeat
described below.

The WMO diagnosis does not define one globally smooth tropopause-height
surface: the first qualifying native level can change discontinuously as the
raw profile varies horizontally. The path planner therefore partitions the
zeros of every raw bilinear HHL/T predicate used by the discrete decision:
the 5-km eligibility threshold, the local `2 K km^-1` lapse test, the 2-km
window selection, and the corresponding mean-lapse tests. A candidate is
accepted only when the native profile covers the complete following 2 km and
the mean lapse from the candidate to every native level through the first
level at or above 2 km is at most `2 K km^-1`; checking only the final level or
accepting the former 1.5-km edge case would be scientifically weaker. The
implemented comparison never divides two nearly cancelling differences. For
every upward ordered native pair it evaluates the canonical residual

```math
\begin{aligned}
R_{ij}
&=(z_j-z_i)
 +500\ \mathrm{m\,K^{-1}}(T_j-T_i),\\
R_{ij}&\ge 0
\quad\Longleftrightarrow\quad
-\frac{T_j-T_i}{z_j-z_i}\le 2\times10^{-3}\ \mathrm{K\,m^{-1}},
\qquad z_j>z_i.
\end{aligned}
\tag{A26a}
```

The height differences and both products in $R_{ij}$ are accumulated by one
fixed binary64 expansion using error-free `TwoSum` transforms and
`TwoProduct` residuals obtained with `FMA`. The path planner and the science
kernel call this same provider-neutral comparator after reconstructing the
same native HHL/T primitives in the same order. The 5-km eligibility and
2-km-span comparisons use the corresponding compensated signed difference.
This certifies the numerical decision for the decoded and reconstructed
binary64 model primitives; it is not a claim that ICON meteorological
uncertainty is micrometric.

For `upper=lower+1`, the first mean-lapse residual is algebraically identical
to the instantaneous `lower/next-level` residual in (A26a). The WMO decision
still evaluates both logical conditions, but the current path contract v23
continues the v12 rule of registering their shared physical zero set only
once. This avoids treating one surface as two distinct roots without
coalescing any genuinely different event identities.

The planner emits span and mean-lapse predicates in native-level order through the
first upper level whose four corner spans are all strictly greater than 2 km
by more than their unit-aware roundoff enclosure. Because bilinear
reconstruction is a convex combination of those corners, that upper level is
above 2 km everywhere in the cell: it can still be selected, but no later
level can be read by the WMO decision and later predicates are therefore not
physical events. If even one corner is at or inside the enclosure, enumeration
continues so a horizontally changing first upper level remains partitioned.
Two additional reachability proofs remove algebraic zeros that the WMO
decision cannot read. If an eligible lower candidate's instantaneous-lapse
residual is strictly negative at all four corners beyond its raw-operand
roundoff enclosure, it fails everywhere in the bilinear cell and none of its
span or mean-lapse predicates is emitted. During the upper-level scan, if a
mean-lapse residual is strictly negative throughout the cell, every point
that reaches that upper level rejects the candidate, while every point that
does not reach it has already stopped at an earlier first 2-km top; no later
upper-level predicate is reachable and that scan ends. All predicates
belonging to earlier lower-level candidates remain present. If
one lower candidate is strictly certified at all four corners for 5-km
eligibility, its instantaneous lapse, every mean lapse through the first
cell-wide 2-km top, and the existence of that top, convexity proves that this
candidate qualifies everywhere in the cell. Since WMO returns the first
qualifying candidate, every later WMO predicate and the 200-hPa fallback are
then unreachable and are omitted. Otherwise the 200-hPa predicates of every
native level are partitioned, so the fallback pressure bracket cannot change
inside an open decision interval.
Between those events the WMO candidate is fixed, and its native full-level
surface is already part of the physical partition. This follows the
thermal-tropopause definition
quoted by the [WMO/Copernicus algorithm documentation](https://dast.data.compute.cci2.ecmwf.int/documents/satellite-aerosol-properties/C3S2_312a_Lot2_FDDP-AER/C3S2_312a_Lot2_D-WP2-FDDP-AER_202311_ATBD_AER_Annex_K_AERGOM_v2.1_final3.pdf),
while retaining the project's explicitly documented native-level
approximation. Finished seeing, tau0, transmission, or Overall values are
never interpolated to find these events.

Every raw WMO predicate and side-sensitive physical-science scalar root
processed by the recursive isolator has a finite event identity and retained
root evidence `[s_left,s_right]`. Horizontal-cell crossings use the separate
ECEF predicates and compound-corner rule described below. Four independent
lengths define the current contract:

```math
\begin{aligned}
\delta_{\mathrm{root}}&=0.0002\ \mathrm{m},&
\delta_{\mathrm{merge}}&=0.0004\ \mathrm{m},\\
\delta_{\mathrm{side}}&=0.0005\ \mathrm{m},&
\delta_{\mathrm{proof}}&=0.0002\ \mathrm{m}.
\end{aligned}
\tag{A26b}
```

Thus a proved unique physical or horizontal root is represented by an interval
of radius at most 0.2 mm; non-identical roots inside the 0.4-mm proximity window
fail closed; all side-sensitive probes must clear 0.5 mm; and generic recursive absence proof
continues to 0.2 mm. These are numerical ownership scales for reconstructed
binary64 primitives, not atmospheric accuracy claims.

Evaluation of the accepted Shampine dense polynomial has its own explicit
floating-point enclosure. For Cartesian component $k$, normalized dense-step
coordinate $\theta$, accepted step $h$, DOPRI stages $K_{i,k}$, and dense
coefficients $P_{iq}$, define

```math
\begin{aligned}
\widehat r_k(\theta)
&=y_{0,k}+h\sum_{i=1}^{7}K_{i,k}
  \sum_{q=1}^{4}P_{iq}\theta^q,\\
S_{r,k}(\theta)
&=|y_{0,k}|+|h|\sum_{i=1}^{7}|K_{i,k}|
 \sum_{q=1}^{4}|P_{iq}|\,|\theta|^q,\\
u&=2^{-53},\\
\nu_{128}&=\mathrm{up}(128u),&
d_{128}&=\mathrm{down}(1-\nu_{128}),\\
\gamma_{128}&=\mathrm{up}\!\left(\frac{\nu_{128}}{d_{128}}\right),&
\overline S_{r,k}(\theta)
&=\mathrm{up}\!\left(\frac{\max\{1,S_{r,k}(\theta)\}}
{\mathrm{down}(1-\gamma_{128})}\right),\\
e_{r,k}(\theta)
&=\mathrm{up}\!\left(\gamma_{128}\overline S_{r,k}(\theta)\right),\\
Q_0&=0,&
Q_k&=\mathrm{up}\!\left(Q_{k-1}+\mathrm{up}(e_{r,k}^2)\right),\quad k=1,2,3,\\
E_r(\theta)&=\mathrm{up}\!\left(\sqrt{Q_3}\right).
\end{aligned}
\tag{A26c}
```

This is Higham's outward-rounded
$\gamma_n=(n u)/(1-n u)$ construction. The point evaluator and its positive
absolute-monomial scale require at most 82 rounding-capable source operations
per position component and 102 per derivative component; the independently
audited $n=128$ ceiling therefore retains a 26-operation margin over the
longest case. The represented positive formation sum can itself round
downward, so its denominator is rounded downward before the forward-error
factor is applied. Component errors are combined only after their individual
bounds have been proved, using outward-rounded squares, additions, and square
root; this retains the Euclidean envelope instead of replacing it with a
cross-component L1 scale. Cancellation inside a Shampine coefficient cannot
shrink the bound because $S_{r,k}$ sums the absolute monomials before their
signs cancel. The derivative uses differentiated absolute monomials and its
own component scales $S_{r',k}$ in the same construction. The broader
Bernstein control-vector formation and affine-restriction paths for the
restricted cubic derivative and quadratic second derivative retain their
separate audited $\gamma_{1024}$ allowances. This is a bound on evaluating the
**accepted dense polynomial**; it is deliberately separate from DOPRI
truncation error, the production science-integral estimator, the repeated
integration enabled in reference mode, and meteorological uncertainty.

The Cartesian, spherical-coordinate, normalized-grid, and grid-snap
enclosures are converted to one equivalent positional envelope

```math
E_{\mathrm{pc}}
=\max\{E_{\mathrm{ECEF}},E_{\phi},E_{\lambda}\}
\le 10^{-3}\ \mathrm{m}.
\tag{A26d}
```

The 1-mm inequality is a hard **acceptance ceiling**, not an error substituted
into every sample. It is enforced at every horizontal-root evaluation,
physical sample, and metric midpoint; exceeding it makes the node unavailable.
The residual uses the smaller uncertainty actually derived at that sample.
Let $e_r$ be the outward dense-ECEF formation and Cartesian-arithmetic bound,
$\rho_-$ and $p_-$ the positive spherical and cylindrical radius bounds, and
$\Delta\phi$, $\Delta\lambda$ the native grid increments. The implementation
forms outward coordinate-fraction uncertainties of the form

```math
\begin{aligned}
\delta\phi&=\mathrm{up}\!\left(\arcsin\frac{e_r}{\rho_-}+\eta_\phi\right),&
\delta\lambda&=\mathrm{up}\!\left(\arcsin\frac{e_r}{p_-}+\eta_\lambda\right),\\
\delta u&=\mathrm{up}\!\left(\frac{\delta\phi}{\Delta\phi}
+\eta_u+\delta_{\mathrm{snap}}\right),&
\delta v&=\mathrm{up}\!\left(\frac{\delta\lambda}{\Delta\lambda}
+\eta_v+\delta_{\mathrm{snap}}\right),\\
E_{\mathrm{coord}}[F]
&=\mathrm{up}\!\left(G_{u,F}\delta u+G_{v,F}\delta v\right).
\end{aligned}
\tag{A26d1}
```

$G_{u,F}$ and $G_{v,F}$ are the outward, field-specific maximum opposing-edge
differences of the four native corners; their subtraction roundoff is included.
Thus a nearly constant field receives a much smaller coordinate uncertainty
than a steep field at the same ray point. The equivalent metric quantity
$E_{\mathrm{pc}}$ is retained only for the 1-mm admission check and for
conservative reach/denominator construction. It is not blindly multiplied by
a path Lipschitz constant for every residual.

Grid-line roots are not found by asking whether a rounded `asin` or `atan2`
coordinate equals a grid value. For boundary longitude $\lambda_b$ and
latitude $\phi_b$, the planner isolates the ECEF predicates

```math
\begin{aligned}
g_{\lambda}(\boldsymbol r)
&=y\cos\lambda_b-x\sin\lambda_b,\\
g_{\phi}(\boldsymbol r)
&=z\cos\phi_b-\sqrt{x^2+y^2}\sin\phi_b.
\end{aligned}
\tag{A26e}
```

Their spatial gradient norm is at most one away from the polar singularity,
so the actual sample-specific dense-ECEF bound $e_r$, not the 1-mm ceiling,
enters each residual interval directly. Longitude and latitude events coalesce only under the explicit
compound-corner identity rule; ordinary nearby events remain fail-closed.

Smooth physical HHL, full-level, surface, and fixed cloud-tier boundaries, and
smooth bilinear WMO decision predicates, additionally receive a local
transversality certificate before the generic terminal-leaf rule is used. The
PBL boundary receives the same certificate only inside a partition with one
proved branch of `clamp(MH,500 m,2000 m)`. Bilinear weights are non-negative
and sum to one, so an outward-expanded four-corner range can prove a complete
cell to be on the lower constant, identity, or upper constant branch. For a
mixed cell the planner first isolates both raw bilinear decision fields

```math
D_{500}(s)=MH(s)-500\ \mathrm{m},
\qquad
D_{2000}(s)=MH(s)-2000\ \mathrm{m}.
\tag{A26e1}
```

Their strict root evidence becomes mandatory path endpoints. Each resulting
subinterval must clear the 0.5-mm side guard and must give the same certified
branch at all one-sided and quarter probes before the corresponding smooth PBL
surface is isolated. At the midpoint $m=(a+b)/2$ of such an interval, with
half-width $h=(b-a)/2$, reconstructed depth $M(m)$, a certified path Lipschitz
bound $L_M$, and its unit-aware binary64 enclosure $\eta_M$, the
implementation forms

```math
I_M=
\left[
\mathrm{down}\!\left(M(m)-L_Mh-\eta_M\right),
\mathrm{up}\!\left(M(m)+L_Mh+\eta_M\right)
\right].
```

If $I_M\subseteq(-\infty,500\,\mathrm m]$, the smooth boundary fields are
`HSURF + const`; if $I_M\subseteq[500\,\mathrm m,2000\,\mathrm m]$, they are
`HSURF + MH`; and if $I_M\subseteq[2000\,\mathrm m,\infty)$, they are again
`HSURF + const`. Indeterminate decision evidence, evidence entering a
branch-sensitive probe span, or a branch change inside a certified partition
fails closed. On the upper branch, `HSURF+2000 m` is algebraically identical
to the configured low-cloud top; that shared physical zero set is registered
once rather than rejected as two nearby events.
Let $\boldsymbol r(s)$ be the stored DOPRI dense ray,
$\rho=\lVert\boldsymbol r\rVert$, $p=\sqrt{x^2+y^2}$, and let $U_B$ and $A$
be outward Bernstein bounds for $\lVert\boldsymbol r'\rVert$ and
$\lVert\boldsymbol r''\rVert$ on $I=[a,b]$. The common metric speed is
deliberately

```math
U=\max\{1,U_B\}.
```

Here $s$ is geometric arc length, so the exact trajectory has
$\lVert\boldsymbol r'(s)\rVert=1$. Consequently `1` is a known value of the
metric speed in the exact system, while $U_B$ is the outward numerical upper
bound; their maximum is the conservative upper bound used for coordinate
reach. Let
$m=(a+b)/2$, $\Delta s^+=\mathrm{up}(b-a)$,
$\boldsymbol r_m=(x_m,y_m,z_m)$, and let $E_r(m)$ be (A26c). Unlike the older
latitude-support construction, the current contract derives the two metric
denominators directly from the evaluated ECEF point. With `down`/`up`
including directed rounding and the declared operand-scale enclosure,

```math
\begin{aligned}
\rho_{m,-}&=\mathrm{down}\!\left(
\sqrt{x_m^2+y_m^2+z_m^2}-E_r(m)-\eta_\rho\right),\\
p_{m,-}&=\mathrm{down}\!\left(
\sqrt{x_m^2+y_m^2}-E_r(m)-\eta_p\right),\\
d_I&=\mathrm{up}\!\left(U\frac{\Delta s^+}{2}\right),\\
\rho_{\min}&=\mathrm{down}(\rho_{m,-}-d_I),\\
p_{\min}&=\mathrm{down}(p_{m,-}-d_I),\\
\sigma_\phi&=\mathrm{up}\!\left(\frac{\rho_{\min}}{p_{\min}}\right)
\ge\max\{1,|\tan\phi|\},\\
\Phi_1&=\frac{U}{\rho_{\min}},&
\Lambda_1&=\frac{U}{p_{\min}},\\
\Phi_2&=\frac{A}{\rho_{\min}}
+\frac{U^2\sigma_\phi}{\rho_{\min}^2},&
\Lambda_2&=\frac{A}{p_{\min}}+\frac{U^2}{p_{\min}^2}.
\end{aligned}
```

Here $\eta_\rho$ and $\eta_p$ enclose formation of the two norms after the
dense-position error has already been included. Both positive denominators
must remain finite and strictly positive. Computing $p_{\min}$ from
$\sqrt{x_m^2+y_m^2}$ rather than from $\rho_{\min}\cos\phi$ avoids coupling
two separately rounded coordinate transforms and preserves the tighter,
direct high-latitude cylindrical-radius certificate.

For normalized cell coordinates
$u=(\phi-\phi_S)/\Delta\phi$ and
$v=(\lambda-\lambda_W)/\Delta\lambda$, define the outward corner-difference
bounds

```math
\begin{aligned}
G_u&=\max\{|h_{10}-h_{00}|,|h_{11}-h_{01}|\},\\
G_v&=\max\{|h_{01}-h_{00}|,|h_{11}-h_{10}|\},\\
C&=|h_{11}-h_{10}-h_{01}+h_{00}|.
\end{aligned}
```

Each edge difference is enlarged by two corner roundoff envelopes and $C$ by
four. Bilinearity then yields the complete mixed-term bound

```math
\begin{aligned}
H_2={}&G_u\frac{\Phi_2}{\Delta\phi}
+G_v\frac{\Lambda_2}{\Delta\lambda}
+2C\frac{\Phi_1}{\Delta\phi}\frac{\Lambda_1}{\Delta\lambda},\\
f(s)={}&\rho(s)-R_{\mathrm{ICON}}-H(u(s),v(s)),\\
M_f={}&A+\frac{U^2}{\rho_{\min}}+H_2
\ge\sup_{s\in I}|f''(s)|.
\end{aligned}
```

Every sign, clearance, and secant decision uses a residual interval rather
than the nominal binary64 value alone. If $\widehat f(x)$ is the reconstructed
value, $S_f$ is the raw operand scale, and $\Delta_f$ is the Lipschitz
variation used by a clearance test, then the evaluation term is selected from
the actual dependency graph:

```math
E_{f,\mathrm{eval}}=
\begin{cases}
e_r,
&\text{ECEF latitude/longitude predicate},\\
E_{\mathrm{coord}}[F],
&\text{bilinear WMO or other field-only predicate},\\
\mathrm{up}\!\left(e_r+\sum_k E_{\mathrm{coord}}[H_k]\right),
&f=z-\sum_k H_k\quad\text{(HHL, full level, PBL, cloud tier)},\\
\mathrm{up}\!\left(e_r+E_{H_{200},\mathrm{coord}}+E_{H_{200},\mathrm{arith}}\right),
&f=z-H_{200}.
\end{cases}
\tag{A26i}
```

Here the physical-boundary sum contains exactly the operands used by that
boundary: for example surface height alone for a fixed cloud top, and surface
height plus mixed-layer depth on the identity branch of PBL. The separate
unit-aware scalar arithmetic allowance below still covers formation of the
nominal residual. Therefore

```math
\begin{aligned}
\eta_f(x,\Delta_f)
&=\mathrm{up}\!\left(
64u\max\{1,|\widehat f(x)|,|\Delta_f|,|S_f|\}
\right),\\
E_f(x,\Delta_f)
&=\mathrm{up}\!\left(E_{f,\mathrm{eval}}(x)
+\eta_f(x,\Delta_f)\right),\\
F_x(\Delta_f)
&=\left[
\mathrm{down}(\widehat f(x)-E_f(x,\Delta_f)),
\mathrm{up}(\widehat f(x)+E_f(x,\Delta_f))
\right].
\end{aligned}
\tag{A26f}
```

For ordinary sign classification $\Delta_f=0$; for root-free clearance it is
the upward Lipschitz variation on the tested interval. A sign exists only when
the complete interval is strictly above or below zero. This same residual
contract is used by the WMO, PBL, HHL, cloud-tier, surface, and horizontal
ECEF predicates.

Let outward endpoint residual intervals be
$F_a=[F_a^-,F_a^+]$, $F_b=[F_b^-,F_b^+]$, and let
$W=[W^-,W^+]$ enclose $w=b-a>0$. Directed interval subtraction and division
form

```math
\begin{aligned}
D&=[F_b^- - F_a^+,\ F_b^+ - F_a^-],\\
Q&=D/W,\\
f'(I)&\subseteq
\left[Q^- - \frac{M_fW^+}{2},\\
      Q^+ + \frac{M_fW^+}{2}\right].
\end{aligned}
\tag{A26g}
```

The factor $1/2$ is exact: the secant is the mean derivative on $I$, and
$w^{-1}\int_a^b|x-t|\,dt\le w/2$. This local enclosure is intersected with
the independent radial-derivative-minus-boundary-slope enclosure. All
positive arithmetic is rounded outward. Cubic $\boldsymbol r'$ and quadratic
$\boldsymbol r''$ are first restricted to each accepted DOPRI segment and
enclosed by their Bernstein control vectors.

If the resulting derivative interval excludes zero and an ancestor has strict
opposite endpoint residual signs, the intermediate value theorem proves
existence and Rolle's theorem proves uniqueness. That proof is retained while
the bracket is refined; it is not discarded when a small residual interval
later contains zero. For the retained proof bracket
$B_k=[\ell_k,r_k]$, derivative enclosure $D=[d^-,d^+]$, and an outward
residual interval $F_x=[F_x^-,F_x^+]$ at a represented point $x$, the
contractor is

```math
\begin{aligned}
B_k&=[\ell_k,r_k],\qquad D=[d^-,d^+],\qquad 0\notin D,\\
q^-&=\mathrm{down}\!\left(
\min_{i,j\in\{-,+\}}\frac{F_x^i}{d^j}
\right),&
q^+&=\mathrm{up}\!\left(
\max_{i,j\in\{-,+\}}\frac{F_x^i}{d^j}
\right),\\
N_x&=[\mathrm{down}(x-q^+),\mathrm{up}(x-q^-)],\\
B_{k+1}&=B_k\cap N_x\cap S_k(x),\\
S_k(x)&=
\begin{cases}
[x,r_k],&\sigma_D\sigma_F<0,\\
[\ell_k,x],&\sigma_D\sigma_F>0,\\
B_k,&0\in F_x.
\end{cases}
\end{aligned}
\tag{A26h}
```

Here $\sigma_D$ is the strict sign of $D$ and $\sigma_F$ exists only when the
complete interval $F_x$ is strictly signed. The mean-value theorem makes
$x-F_x/D$ a necessary enclosure for the same already-proved unique root. All
four endpoint quotients and both subtractions are rounded outward. A residual
interval containing zero receives no nominal sign. Contractor images of the
two signed ancestor endpoints are applied first and are followed by the
midpoint image. The coarser diagnostic radius
$\max\{|F_x^-|,|F_x^+|\}/\inf|D|$ remains a valid superset, but it is not used
in place of the asymmetric interval-Newton image.

If the midpoint contraction still leaves an unlocalised bracket
$B=[\ell,r]$, let $w=r-\ell$, $d=2\delta_{\mathrm{root}}$, and let
$d_{64}^-$ denote the immediately preceding binary64 number below $d$. The
two off-centre probes are constructed from

```math
\begin{aligned}
w'&=
\begin{cases}
\max\{d_{64}^-,w/2\},&w>d,\\
w/2,&w\le d,
\end{cases}\\
\mu&=(w-w')/2,\qquad
x_L=\ell+\mu,\qquad x_R=r-\mu.
\end{aligned}
\tag{A26j}
```

Both probes receive the same strict-sign and interval-Newton contraction; the
second may be skipped only after the first has already met the public radius.
This rule prevents arbitrarily small midpoint contractions from postponing
off-centre evidence until the complete isolation budget is exhausted. If
neither probe contracts an unlocalised bracket, the node fails closed.
Acceptance requires both outward-rounded represented half-widths
$\mathrm{up}(m-\ell)$ and $\mathrm{up}(r-m)$ to be at most 0.2 mm; a nominal
total width alone is insufficient. A derivative enclosure containing zero, a
missing ancestor existence proof, or contradictory orientation never receives
this contractor. It therefore cannot absorb a tangent, paired, or triple root
and never treats a nominal near-zero value as an exact root.

Without that certificate, a nominal floating-point zero is not treated as an
exact mathematical root: its full residual interval is retained. At a
terminal leaf no wider than $\delta_{\mathrm{proof}}=0.2$ mm, each non-zero sign
change records the actual sign-changing half-leaf; every other indeterminate
leaf remains unresolved and rejects the node. Both halves are examined
independently, so evidence in one half never suppresses search in the other.
The public maximum root-localisation radius is
$\delta_{\mathrm{root}}=0.2$ mm, equal to the terminal proof scale. A proof
bracket is still retained explicitly; equality of scales does not turn a
nominal zero into an exact event.

Historically, a production ICON-EU WMO residual exposed the first inadequate
micrometre contract: its rigorously enclosed root bracket had a
`58.3140936 micrometre` diameter, so the former 5-micrometre root radius was
unattainable even with further bisection. A later full-system audit showed the
deeper issue: dense-polynomial formation, spherical-coordinate conversion,
grid normalization, and residual classification must share one enforced
envelope rather than each relying on a smaller local epsilon. The current
1-mm position/coordinate-evaluation ceiling and 0.2-mm proof/root scales
replace the interim 50/100/125-micrometre contract. They remain negligible against ICON-EU native
horizontal and vertical resolution and change numerical ownership only; they
do not claim increased meteorological accuracy.

A terminal leaf that is neither resolved by (A26g), proved root-free by
Lipschitz clearance, nor
produces exact or sign-changing root evidence is unresolved, and **any such
leaf rejects the complete node**. Intersection with, adjacency to, or global
proximity to retained evidence is never accepted as a substitute for an
absence proof. Consequently, absent a strict monotonicity certificate, even
exact `[s,s]` evidence does not suppress an uncleared adjacent remainder; that
exact-root case still fails closed. Within
one scalar event, only bit-identical root evidence — the
same representative, left endpoint, right endpoint, and exactness flag — is
discarded as a duplicate; the metadata field cannot union different brackets,
and an evaluated nominal zero with a non-zero envelope is never marked exact.
Separately localised non-identical roots of the same scalar event 0.4 mm or less
apart fail closed. At the later cross-event candidate-compaction stage,
candidates whose represented `pathM` values are bit-identical form one compound
geometric breakpoint even when their event identities differ. The complete
sorted union of event identities is retained and every associated transition
is applied at that same section. Bit-distinct coordinates are never averaged
or merged; if their separation is at most 0.4 mm, the node fails closed. The
sole horizontal exception is an exact grid corner consisting of exactly one
`latitude/*` and one `longitude/*` identity: both path coordinates and both
underlying scalar zeros must be exactly equal. A compound corner plus either
of its existing constituents is idempotent; a second event on either axis or
any third identity is rejected.

The declared side-uncertainty guard retains the independent 0.4-mm
numerical-proximity threshold and the 0.2-mm root-radius allowance:

```math
\delta_{\mathrm{side}}
=0.0004\ \mathrm{m}+\frac{0.0002\ \mathrm{m}}{2}
=0.0005\ \mathrm{m}.
\tag{A27}
```

This is a numerical ownership allowance, not a statistical confidence
interval, and it never certifies an otherwise unproved remainder. Every
physical planner probe must be separated from the stored compound-event
representative by strictly more than $0.0005$ m; a
probe at exactly the guard is not accepted. Every omitted one-sided
classification sliver between an event and its physical probe first attempts
an independent Lipschitz or one-sided derivative clearance. If neither proof
closes, path v23 records the sliver in the limited approximation union from
(A28g); it may be published only below the one-metre cumulative ceiling.
The science-kernel endpoint probe requests the 0.5-mm side guard and, for a
short event-forced interval, is clipped only where the independently derived
event-interval floor still permits it. Root
isolation recursively explores both halves of every interval, including both
sides of every detected root. A same-sign terminal interval is accepted as
empty only with a Lipschitz-clearance proof; otherwise it is refined locally to
`0.0002 m`. At that proof scale it must yield sign-changing root
evidence; any unresolved leaf fails closed without an overlap exception.
Boundary ownership is never delegated to a floating-point `nextafter` step.
Path v23 retains the v22 contraction of one same-event bracket whose
existence and uniqueness have already been proved. It does not change the
0.2-mm root radius, 0.2-mm terminal proof scale, 0.4-mm distinct-root
proximity rejection, 0.5-mm side guard, bit-identical compounding,
root-localisation contracts. It changes only the product treatment of an
unproved endpoint sliver: the interior root search remains complete, while the
sliver is exposed as a limited-quality approximation under (A28g). Kernel v29
retains this contract. Interval Newton and paired
probes neither merge bit-distinct roots nor choose a physical side inside the
0.5-mm guard.

For an omitted endpoint sliver, ordinary Lipschitz clearance remains the first
test. HHL, full-level, cloud-tier, and raw bilinear PBL-clamp predicates also
have a one-sided derivative certificate. Let $x_p$ be the nearest unambiguously
interior probe, $I_s$ the sliver between it and the horizontal-cell endpoint,
$S_p$ the secant/curvature derivative enclosure on the adjacent fully interior
probe interval, $M_f$ a bound for $|f''|$, and $L_f$ the independent global
bound for $|f'|$. The transported enclosure is

```math
D_s=\left(S_p+[-M_f|I_s|,M_f|I_s|]\right)\cap[-L_f,L_f].
\tag{A27a}
```

Write $D_s=[d^-,d^+]$, let
$F_p=[F_p^-,F_p^+]$ be the outward-rounded residual enclosure at $x_p$, and
let $w^+$ be an outward-rounded upper bound for the sliver width. With
$\mathrm{rd}_{-}$ and $\mathrm{rd}_{+}$ denoting directed rounding toward
$-\infty$ and $+\infty$, respectively, the complete one-sided residual
enclosures are

```math
\begin{aligned}
\mathcal F_{\mathrm{start}}
&=\left[
\mathrm{rd}_{-}\left(F_p^- - w^+\max(d^+,0)\right),
\mathrm{rd}_{+}\left(F_p^+ + w^+\max(-d^-,0)\right)
\right],\\
\mathcal F_{\mathrm{end}}
&=\left[
\mathrm{rd}_{-}\left(F_p^- - w^+\max(-d^-,0)\right),
\mathrm{rd}_{+}\left(F_p^+ + w^+\max(d^+,0)\right)
\right].
\end{aligned}
\tag{A27b}
```

The start formula follows from
$f(x)=f(x_p)-\int_x^{x_p}f'(t)\,dt$; the end formula follows from
$f(x)=f(x_p)+\int_{x_p}^{x}f'(t)\,dt$. The sliver is certified root-free only
when $0\notin\mathcal F_{\mathrm{start}}$ or
$0\notin\mathcal F_{\mathrm{end}}$, as appropriate. Thus motion away from
zero remains certifiable, but motion toward zero or a derivative enclosure
containing zero is also safe when its complete bounded displacement cannot
consume the strict residual margin. An empty derivative intersection,
non-finite bound, indeterminate probe residual, or final enclosure containing
zero fails closed. The ambiguously owned cell endpoint is never evaluated.
For every physical predicate, including WMO categories and the nonlinear
200-hPa fallback, an inconclusive endpoint-only root clearance is recorded in
the limited one-metre budget while root isolation over the certified interior
still runs in full. The ambiguous endpoint itself is never evaluated.

Known physical breakpoints are mandatory integration endpoints even when the
atomic interval between them is shorter than 1 cm. The 1-cm value remains only
the floor for a panel whose two endpoints were both created by adaptive
subdivision; it is not permission to delete, merge, or reclassify an
event-forced interval. A short event-forced interval is processed by the
highest-order applicable positive-weight GL5/GL3, GL3/GL2, or GL2/GL1 pair
when every node clears its endpoint evidence. Below the GL2 sampling boundary,
v29 instead permits the explicitly limited positive midpoint rule defined in
(A28f) when its three safe probes retain one partition and the total budget in
(A28g) is not exceeded; otherwise the node is unavailable. This
separation follows the same structural principle as
[GNU GSL QAGP integration with known singular points](https://www.gnu.org/software/gsl/doc/html/integration.html#qagp-adaptive-integration-with-known-singular-points),
where supplied difficult points delimit the integration ranges before adaptive
work begins. Astrosferum implements its own G7/K15 and positive-weight
Gauss–Legendre rules and does not call GSL.

For the outer Kronrod abscissa used by the Go constant expression,

```math
\begin{aligned}
g_{K15}
&=\frac{1-0.991455371120812639206854697526329}{2}\\
&=0.0042723144395936803965726512368355\ldots,\\
L_{K15,\mathrm{geom}}
&=\frac{\delta_{\mathrm{side}}}{g_{K15}}\\
&=0.11703258434497453171\ldots\ \mathrm{m},\\
L_{K15,\min}
&=(1+10^{-12})L_{K15,\mathrm{geom}}\\
&=0.11703258434509156429\ldots\ \mathrm{m}.
\end{aligned}
\tag{A28}
```

For a short interval $[a,b]$, let $c=(a+b)/2$, $h=(b-a)/2$, and let $f_j(s)$
be component $j$ of the original physical integrand (A15). The independent
five- and three-point Gauss–Legendre estimates are

```math
\begin{aligned}
I_{5,j}&=h\left[
w^{(5)}_0f_j(c)
+\sum_{k=1}^{2}w^{(5)}_k
\left(f_j(c-hx^{(5)}_k)+f_j(c+hx^{(5)}_k)\right)
\right],\\
I_{3,j}&=h\left[
w^{(3)}_0f_j(c)
+w^{(3)}_1
\left(f_j(c-hx^{(3)}_1)+f_j(c+hx^{(3)}_1)\right)
\right],\\
e_{j,5/3}&=\left|I_{5,j}-I_{3,j}\right|.
\end{aligned}
\tag{A28a}
```

The implemented positive nodes and weights are

```math
\begin{aligned}
(x^{(5)}_1,x^{(5)}_2)
&=(0.906179845938663992797626878299393,
   0.538469310105683091036314420700209),\\
(w^{(5)}_1,w^{(5)}_2,w^{(5)}_0)
&=(0.236926885056189087514264040719917,
   0.478628670499366468041291514835638,
   0.568888888888888888888888888888889),\\
x^{(3)}_1
&=0.774596669241483377035853079956480,\\
(w^{(3)}_1,w^{(3)}_0)
&=(0.555555555555555555555555555555556,
   0.888888888888888888888888888888889),\\
x^{(2)}_1
&=0.577350269189625764509148780501957,\\
w^{(2)}_1&=1,\\
w^{(1)}_0&=2.
\end{aligned}
\tag{A28b}
```

The additional positive-weight estimates and their independent comparisons are

```math
\begin{aligned}
I_{2,j}&=h\left[
f_j(c-hx^{(2)}_1)+f_j(c+hx^{(2)}_1)
\right],\\
I_{1,j}&=2h f_j(c),\\
e_{j,3/2}&=\left|I_{3,j}-I_{2,j}\right|,\\
e_{j,2/1}&=\left|I_{2,j}-I_{1,j}\right|.
\end{aligned}
\tag{A28b1}
```

An $n$-point Gauss--Legendre rule has algebraic degree of exactness $2n-1$:
GL5, GL3, GL2, and GL1 are exact through degrees 9, 5, 3, and 1,
respectively. Implementation regressions verify these declared orders on
polynomial inputs. Reconstructed atmospheric integrands are not generally
polynomials, however, so each higher/lower difference in (A28a) and (A28b1)
remains an engineering error estimator rather than a rigorous enclosure.

The outer GL5 node gives the smallest endpoint fraction among the two rules
and therefore controls the certified short-panel floor:

```math
\begin{aligned}
g_{GL5}
&=\frac{1-x^{(5)}_1}{2}\\
&=0.0469100770306680036011865608503035\ldots,\\
L_{GL5,\mathrm{geom}}
&=\frac{\delta_{\mathrm{side}}}{g_{GL5}}\\
&=0.010658690661989730616\ldots\ \mathrm{m},\\
L_{GL5,\min}
&=(1+10^{-12})L_{GL5,\mathrm{geom}}\\
&=0.010658690662000389306\ldots\ \mathrm{m}.
\end{aligned}
\tag{A28c}
```

The GL3 and GL2 outer nodes analogously define the two lower positive-rule
floors:

```math
\begin{aligned}
g_{GL3}
&=\frac{1-x^{(3)}_1}{2}
=0.112701665379258311482073460021760,\\
L_{GL3,\mathrm{geom}}
&=\frac{\delta_{\mathrm{side}}}{g_{GL3}}
=0.004436491673103708443\ldots\ \mathrm{m},\\
L_{GL3,\min}
&=(1+10^{-12})L_{GL3,\mathrm{geom}}
=0.004436491673108144934\ldots\ \mathrm{m}.
\end{aligned}
\tag{A28d}
```

```math
\begin{aligned}
g_{GL2}
&=\frac{1-x^{(2)}_1}{2}
=0.2113248654051871177454256097490215,\\
L_{GL2,\mathrm{geom}}
&=\frac{\delta_{\mathrm{side}}}{g_{GL2}}
=0.002366025403784438647\ldots\ \mathrm{m},\\
L_{GL2,\min}
&=(1+10^{-12})L_{GL2,\mathrm{geom}}
=0.002366025403786804672\ldots\ \mathrm{m}.
\end{aligned}
\tag{A28e}
```

The dimensionless `1e-12` factors are representation cushions for strict
binary64 comparisons, not physical uncertainties. Production v29 deliberately
contains no moment-fitted or extrapolatory short-panel rule. Such a rule can
have arbitrarily large signed weights when endpoint guards compress its sample
domain, so exact construction of its coefficients cannot bound amplification
of errors in the nonlinear atmospheric integrand. A certified interval upper
bound would be acceptable in principle, but the current primitive contract
does not provide the componentwise supremum or Lipschitz evidence required to
construct one without inventing a physical bound. Instead, v29 exposes the
insufficient quadrature evidence as limited quality and uses a positive
interior point estimate with a deliberately broad engineering allowance.

The v29/v23 rule selection is exact:

- a panel with two numerical endpoints uses G7/K15 and its 1-cm adaptive
  floor;
- otherwise $L>L_{K15,\min}$ uses G7/K15;
- otherwise $L_{GL5,\min}<L\le L_{K15,\min}$ uses independent GL5/GL3;
- otherwise $L_{GL3,\min}<L\le L_{GL5,\min}$ uses independent GL3/GL2;
- otherwise $L_{GL2,\min}<L\le L_{GL3,\min}$ uses independent GL2/GL1;
- otherwise $L\le L_{GL2,\min}$ uses the limited positive midpoint rule when a
  certified safe interior exists and the cumulative approximation ceiling is
  not exceeded;
- an impossible certified split or any partition-signature mismatch also
  fails closed. Path contract v23 must isolate and certify every reachable raw
  WMO decision breakpoint before quadrature; the science kernel never creates
  a breakpoint reactively.

For a limited panel $[a,b]$, let $L=b-a$, $m=a+L/2$, and let
$s_l,m,s_r$ be the two certified one-sided probes and the midpoint. The
published point estimate and engineering allowance are

```math
\begin{aligned}
I_{1,j}&=L f_j(m),\\
M_j&=L\max\{f_j(s_l),f_j(m),f_j(s_r)\},\\
e_{\mathrm{approx},j}&=\mathrm{nextup}\!\left(\max\{|I_{1,j}|,M_j\}\right).
\end{aligned}
\tag{A28f}
```

All five integrands are non-negative, all three represented points must have
the same exact partition signature, and the midpoint must lie strictly inside
both 0.5-mm event guards. Thus the reported range contains zero and extends to
at least twice the midpoint estimate; the error radius itself covers the
largest sampled panel-equivalent magnitude. This is an explicit
project engineering allowance, **not** a rigorous interval enclosure or an
embedded convergence proof. The conservative cloud-cover member uses the
already-computed raw-native corner/support envelope at those points.

The same limited-quality budget records a horizontal-cell endpoint sliver for
which neither the Lipschitz test nor the one-sided derivative test proved the
absence of a physical root. Root isolation still runs on the complete certified
interior; only the unresolved endpoint span follows the unambiguous interior
branch. Overlapping slivers are unioned. With $\mathcal P_{\mathrm{lim}}$ the
limited midpoint panels and $\mathcal S_{\mathrm{edge}}$ the union of such
endpoint spans, publication requires

```math
L_{\mathrm{approx}}
=\sum_{p\in\mathcal P_{\mathrm{lim}}}|p|
 +\left|\bigcup_{s\in\mathcal S_{\mathrm{edge}}}s\right|
\le 1\ \mathrm{m}.
\tag{A28g}
```

One metre is a versioned product ceiling, not a routine integration step or a
claim of atmospheric homogeneity. Above it, or without a safe interior point,
the node remains unavailable. The endpoint-span term records branch-ownership
uncertainty only: unlike a midpoint panel it contributes no quantitative term
to $E_{j,\mathrm{pub}}$, because the current native contract provides no proven
componentwise bound for the alternate branch inside that span. Therefore the
reported numerical-error fields do **not** bound this endpoint-branch effect;
the mandatory `limited` quality, non-zero `approximation_length_m`, and reason
code disclose it separately.

For an incoming absolute component budget $A_j$, represented midpoint
$m=a+(b-a)/2$, represented length $L=b-a$, and represented child lengths
$L_l=m-a$ and $L_r=b-m$, the subdivision uses

```math
\begin{aligned}
f_l&=\frac{L_l}{L},\\
A_{l,j}&=f_l A_j,\\
A_{r,j}&=A_j-A_{l,j},\\
r_l&=r_r=r.
\end{aligned}
```

Thus the right absolute budget is the residual rather than an independently
rounded product, so the two represented budgets preserve the incoming budget.
The relative tolerance is unchanged. Every adaptive midpoint is explicitly
marked numerical, whereas each breakpoint certified by path contract v23 —
including a raw WMO decision breakpoint — remains physical. Consequently each
child is checked against the floor corresponding to its actual endpoint types,
rather than inheriting the parent's two-sided guard.

In reference verification mode, the independent pass at half tolerance must
not merely repeat the coarse nodes. Before rule selection it bisects every
original atomic interval with two physical endpoints exactly once **except a
limited sub-GL2 panel**, which has no room for that split and remains explicitly
non-converged in both passes. For all ordinary panels this is independent of
which positive-weight production rule the coarse pass would select.
The represented midpoint is numerical; the two children are then processed
normally and are not recursively forced to split again. This supplies
genuinely different samples while retaining the same physical-event envelopes
and proportionally allocated absolute budgets. Production does not perform
this repeat.

All five integrands in (A15), and every retained G7/K15, GL5/GL3, GL3/GL2, or
GL2/GL1 weight, are non-negative. A negative panel or accumulated component can therefore only be
a numerical artefact: it is rounded to zero when its magnitude does not exceed
its own estimator, and a larger negative value fails closed. After all panels are
summed, compensated totals $I_j=\sum_p I_{p,j}$ and the strict embedded error
over ordinary converged panels
$E_{j,\mathrm{conv}}=\sum_{p\notin\mathcal P_{\mathrm{lim}}}e_{p,j}$ must
additionally satisfy the global gate

```math
E_{j,\mathrm{conv}}\le A_j+r|I_j|.
\tag{A28i}
```

Local acceptance alone cannot consume the same relative allowance repeatedly.
The published uncertainty remains
$E_{j,\mathrm{pub}}=E_{j,\mathrm{conv}}+
\sum_{p\in\mathcal P_{\mathrm{lim}}}e_{\mathrm{approx},p,j}$ and is propagated
through turbulence, cloud closure, and Overall. Failure of the strict
accumulated check makes the node unavailable. The limited allowance is not
compared with the convergence tolerance because the node explicitly reports
that quadrature did not converge on those panels.

Earlier kernels contained regressions for `0.0251479956 m` and
`0.0181762987 m` panels under the interim 125-micrometre guard. Those values
are retained only as historical failure evidence. Under v22/v23 they were
below the then-current 50-mm two-physical-endpoint floor and failed closed.
They exceed the historical v24 5-mm floor and were not rejected by v24 merely
because of their length. Current v29 regressions exercise limited midpoint
publication below the GL2 boundary, the one-metre fail-closed ceiling, and both
sides of the GL2, GL3, GL5, and K15 rule
boundaries, retain
distinct physical gaps of `1.53107653`, `1.9401`, `3.7474`, `3.9203`,
`4.802253`, `15.9325455`, `26.7341866`, `31.1486803`, `41.5455627`, and
`48.78 mm`, verify exact simultaneous-event
compounding, represented-node
clearance, and the independent finer split. Passing these regressions
is not a claim that the complete Astrodome production rollout has passed.

Every estimate evaluates the original reconstructed state and complete
component integrand at its own path nodes. No rule interpolates
seeing, `tau0`, cloud transmission, Overall, any other derived diagnostic, or
even a finished integrand value. The limited midpoint rule is visibly marked
and never presented as an embedded-converged result.

Clearance arithmetic is unit-aware. With binary64 machine spacing
`u=2^-52`, the implementation encloses cancellation by

```math
\begin{aligned}
\rho(f,Lh,S_f)
&=\mathrm{nextup}\!\left(
64u\max\{1,|f|,|Lh|,|S_f|\}
\right),\\
|f(m)|
&>\mathrm{nextup}\!\left(Lh+\rho(f,Lh,S_f)\right)
\quad\Longrightarrow\quad
\text{the interval of half-width }h\text{ is root-free}.
\end{aligned}
\tag{A29}
```

Here `S_f` is expressed in the native units of the residual, rather than being
an arbitrary dimensionless epsilon. For a physical height residual
`z-H(u,v)`, it includes the ICON spherical-Earth radius plus 200 km because
`z` is obtained by subtracting that radius from an ECEF norm. After the
compensated construction in (A26a), WMO decision fields retain the scale of
the raw operands that participate in cancellation: native HHL heights and
`500|T|` for lapse residuals, both HHL heights and the fixed offset for height
and span residuals, and native pressure together with `20000 Pa` for the
fallback predicate. Using only the much smaller already-cancelled residual
would understate binary64 uncertainty. Corner differences are widened by two such roundoff
allowances, metric denominators are rounded toward zero, and positive slopes,
sums, and products are rounded upward. The WMO prefilter drops a bilinear
predicate only when every corner is on one side of zero by more than this
unit-aware allowance. A same-sign corner set inside the allowance is retained
for the recursive fail-closed isolator.

The reconstructed 200-hPa fallback surface receives the same treatment through
the complete logarithm and quotient, not only through one final rounding step.
Writing

```math
\begin{aligned}
D&=\ln\!\frac{p_l}{p_u},&
N&=\ln\!\frac{p_l}{P_{200}},&
\alpha&=\frac{N}{D},\\
H_{200}&=h_l+\alpha(h_u-h_l),&
P_{200}&=20000\ \mathrm{Pa},
\end{aligned}
\tag{A30}
```

the implementation first widens the positive pressure and height corner
ranges. The dimensionless logarithmic roundoff scale below uses
$p_{\mathrm{ref}}=1\,\mathrm{Pa}$, matching the implementation's Pa-valued
operands:

```math
\begin{aligned}
p_{\mathrm{ref}}&=1\,\mathrm{Pa},\\
S_{\log}
&=\max\left\{
1,
\left|\ln\frac{P_{200}}{p_{\mathrm{ref}}}\right|,
\left|\ln\frac{p_{l,\min}}{p_{\mathrm{ref}}}\right|,
\left|\ln\frac{p_{l,\max}}{p_{\mathrm{ref}}}\right|
\right\},\\
\rho_{\log}&=\mathrm{nextup}(64uS_{\log}).
\end{aligned}
```

It then constructs the outward bounds

```math
\begin{aligned}
D_{\min}
&=\mathrm{nextdown}\!\left(
\frac{\Delta p_{\min}}{p_{l,\max}}
\right),\\
N_{\max}
&=\mathrm{nextup}\!\left(
\max_{p\in\{p_{l,\min},p_{l,\max}\}}
\left|\ln\frac{P_{200}}{p_{\mathrm{ref}}}
-\ln\frac{p}{p_{\mathrm{ref}}}\right|+2\rho_{\log}
\right),\\
L_{\ln p_l}&=\mathrm{nextup}\!\left(\frac{L_{p_l}}{p_{l,\min}}\right),&
L_{\ln p_u}&=\mathrm{nextup}\!\left(\frac{L_{p_u}}{p_{u,\min}}\right),\\
\alpha_{\max}&=\mathrm{nextup}\!\left(\frac{N_{\max}}{D_{\min}}\right),&
L_\alpha
&=\mathrm{nextup}\!\left(
\frac{L_{\ln p_l}}{D_{\min}}
+\frac{N_{\max}(L_{\ln p_l}+L_{\ln p_u})}
{\mathrm{nextdown}(D_{\min}^{2})}
\right),\\
L_{H_{200}}
&=\mathrm{nextup}\!\left(
L_{h_l}+L_\alpha\Delta h_{\max}
+\alpha_{\max}(L_{h_l}+L_{h_u})
\right).
\end{aligned}
\tag{A31}
```

The arithmetic error of the reconstructed fallback height is enclosed
separately from the path-coordinate error. Let $e_p$ and $e_h$ be the outward
64-ULP reconstruction allowances on one pressure and height value,
$e_{\log}$ the corresponding absolute logarithm allowance, and let
$\rho_N$, $\rho_D$, $\rho_\alpha$, $\rho_{\Delta h}$,
$\rho_{\times}$, and $\rho_{+}$ enclose the indicated binary64 operations.
The implementation constructs

```math
\begin{aligned}
e_{\log,l}&=\mathrm{up}\!\left(\frac{e_p}{p_{l,\min}}+e_{\log}\right),&
e_{\log,u}&=\mathrm{up}\!\left(\frac{e_p}{p_{u,\min}}+e_{\log}\right),\\
e_N&=\mathrm{up}(e_{\log,l}+e_{\log}+\rho_N),&
e_D&=\mathrm{up}(e_{\log,l}+e_{\log,u}+\rho_D),\\
D_{\mathrm{eval},\min}
&=\mathrm{down}(D_{\min}-e_D)>0,&
\widehat\alpha_{\max}
&=\mathrm{up}\!\left(\frac{N_{\max}+e_N}{D_{\mathrm{eval},\min}}\right),\\
e_\alpha
&=\mathrm{up}\!\left(
\frac{e_N}{D_{\mathrm{eval},\min}}
+\frac{N_{\max}e_D}{D_{\min}D_{\mathrm{eval},\min}}
+\rho_\alpha
\right),\\
e_{\Delta h}&=\mathrm{up}(2e_h+\rho_{\Delta h}),\\
e_{H_{200}}
&=\mathrm{up}\!\left(
e_h+e_\alpha\Delta h_{\max}
+\widehat\alpha_{\max}e_{\Delta h}
+\rho_{\times}+\rho_{+}
\right).
\end{aligned}
\tag{A31b}
```

If $D_{\mathrm{eval},\min}$ is not strictly positive or
$e_{H_{200}}>0.001\ \mathrm{m}$, the node fails closed. Otherwise every
fallback residual sample combines the actual dense ray-height error, the
coordinate uncertainty propagated through the four native $H_l,H_u,p_l,p_u$
fields, and this arithmetic term as stated in (A26i). It does not substitute
$L_f$ times the 1-mm admission ceiling. The nominal evaluation uses
the cancellation-resistant but mathematically identical expressions
$\log1p((p_l-P_{200})/P_{200})$ and
$\log1p((p_l-p_u)/p_u)$; (A31b) still encloses the complete operation chain
rather than relying on that implementation improvement.

For the monotonicity certificate, with $L_x$ and $B_x$ denoting outward first-
and second-path-derivative bounds, the implementation uses

```math
\begin{aligned}
L_{\log p}&=\mathrm{up}\!\left(\frac{L_p}{p_{\min}}\right),&
B_{\log p}&=\mathrm{up}\!\left(
\frac{B_p}{p_{\min}}+L_{\log p}^{,2}
\right),\\
L_D&=\mathrm{up}(L_{\log p_l}+L_{\log p_u}),&
B_D&=\mathrm{up}(B_{\log p_l}+B_{\log p_u}),\\
L_N&=L_{\log p_l},&B_N&=B_{\log p_l},\\
L_{1/D}&=\mathrm{up}\!\left(\frac{L_D}{D_{\min}^{2}}\right),&
B_{1/D}&=\mathrm{up}\!\left(
\frac{B_D}{D_{\min}^{2}}+\frac{2L_D^2}{D_{\min}^{3}}
\right),\\
B_\alpha&=\mathrm{up}\!\left(
\frac{B_N}{D_{\min}}+2L_NL_{1/D}+N_{\max}B_{1/D}
\right),\\
B_{H_{200}}&=\mathrm{up}\!\left(
B_{h_l}+B_\alpha\Delta h_{\max}
+2L_\alpha(L_{h_l}+L_{h_u})
+\alpha_{\max}(B_{h_l}+B_{h_u})
\right),\\
B_f&=\mathrm{up}\!\left(A_r+\frac{U^2}{\rho_{\min}}+B_{H_{200}}\right).
\end{aligned}
\tag{A31c}
```

On an interval of represented width $w$, a residual secant interval $S_I$
therefore gives

```math
f'(I)\subseteq
\left(S_I+\left[-\frac{B_fw}{2},\frac{B_fw}{2}\right]\right)
\cap[-L_f,L_f].
\tag{A31d}
```

Strict opposite endpoint signs preserved from an ancestor plus an interval in
(A31d) that excludes zero prove existence and uniqueness. If either the
arithmetic enclosure or derivative certificate remains inconclusive at the
declared proof floor, the complete node is unavailable. No finished $H_{200}$
surface is spatially interpolated.

The positive native separation `Delta p_min` keeps the logarithmic denominator
away from zero; `ln(x) >= (x-1)/x` supplies its lower bound. Every positive
addition, multiplication, and division in (A31) is rounded outward with the
denominator rounded downward. Finally the path-position derivative is added to
`L_H200` for the zero of `z-H200`. These are conservative floating-point
enclosures used by the project, not a claim of general interval-arithmetic
proof for the entire atmospheric model.

The selected G7/K15, GL5/GL3, GL3/GL2, or GL2/GL1 higher/lower-rule difference
is the project's declared production componentwise error estimator; it is not
claimed to be a rigorous mathematical enclosure. Each GL pair is independent
rather than embedded; GL5/GL3 shares only the centre, GL3/GL2 shares no nodes,
and GL2/GL1 shares no nodes. The
half-tolerance repeat in reference mode and the partition-signature checks are
engineering consistency tests, not proofs of smoothness. The quadrature also checks the actual HMNSP99
branch at its evaluation nodes. Path contract v23 must already have isolated
every reachable raw WMO decision breakpoint; any categorical mismatch observed
by quadrature fails closed rather than invoking an automatic kernel split.
These science-partition tolerances do not relax the separately validated ODE
top/terrain event tolerance.

The dense-path Lipschitz bounds are formed from the Bernstein control vectors
of the derivative of each accepted Shampine quartic segment; the RK endpoint
tangent diagnostic is not used as an interior derivative bound. Path planning
has an independent budget of 32768 recursive isolations per scalar field, so
the thousands of WMO predicates cannot consume the later physical-surface
allowance. The science quadrature separately retains its limit of 8192
subdivisions and depth 24. Reference verification additionally requires the
half-tolerance repeat to agree within the component bounds, `0.01` Overall,
and `10^{-4}` cloud transmission; otherwise the reference node is unavailable.
Compensated sums are used. Production uses the accumulated selected
higher/lower-rule estimator; reference diagnostics augment the fine estimator
with the repeat difference. Neither is an observational confidence interval.

Implemented identities:

- primitive input: `astrodome-icon-primitives-v2`;
- grid: `astrosferum-grid-geometry-v1` / `spherical-voronoi-v1`;
- straight ray: `astrodome-icon-sphere-straight-ray-v2`;
- full refraction:
  `astrodome-icon-sphere-refraction-full-ciddor-dopri54-v3`;
- refractivity: `ciddor-1996-phase-index-v1`;
- ODE: `dormand-prince-5-4-event-v3`;
- science path: `astrodome-science-path-v23`;
- science kernel: `astrodome-science-kernel-v29`.

Kernel v29 and path contract v23 use one strict boundary-ownership contract
for physical and horizontal events: a maximum 0.2-mm root-localisation radius,
0.4-mm proximity detection with fail-closed handling for bit-distinct roots, a
0.5-mm side guard, and a 0.2-mm proof scale. The separate 1-mm
position/coordinate-evaluation ceiling is unchanged. Bit-identical `pathM`
coordinates alone form a compound geometric breakpoint; its sorted event-ID
union is retained and all transitions are applied there. There is no nominal micrometre representative and
no `preview` override. An accepted root retains its complete evidence bracket;
the stored representative cannot substitute for the interval proof or choose
a side inside the guard.

Path contract v23 retains the v22 secant/curvature monotonicity certificate and
outward corner/interval arithmetic described above and adds the safeguarded
interval-Newton contractor in (A26h) and paired-probe rule (A26j). A mixed PBL cell is first
partitioned at strictly isolated `MH-500 m` and `MH-2000 m` decision roots;
each resulting interval must prove one branch of the clamp before receiving
the smooth certificate. Unresolved interior decision evidence remains
fail-closed; only the explicitly bounded endpoint-sliver case follows (A28g).
Where the upper clamp makes the PBL boundary exactly identical to the
low-cloud top `HSURF+2000 m`, the planner registers the common zero set once
rather than creating a false pair of distinct roots. Likewise,
`mean-lapse(lower, upper=lower+1)` is not registered twice because it is
algebraically identical to the instantaneous `lower/next-level` lapse
predicate; the WMO decision itself is still evaluated.

The dynamic dense-DOPRI formation-sum bound, direct ECEF denominators,
$U=\max\{1,U_B\}$, explicit residual intervals, and ECEF grid predicates are
part of path v23. Kernel v29 retains the removal of the ill-conditioned
extrapolatory Q5/Q3 path, adds positive-weight GL3/GL2 and GL2/GL1 pairs, and
uses the limited positive midpoint path below the GL2 floor under the explicit
one-metre cumulative approximation ceiling. Rule-specific recursion allocates
absolute tolerance by represented length, gives the right child the exact
residual budget, and leaves relative tolerance unchanged. The planner must
certify raw WMO decision breakpoints before quadrature; every partition
mismatch in the kernel remains fail-closed.

The bot calculator and cache, plus the independent site's dataset decoder and
browser, accept only the current v29/v23 contract with an explicitly supported
pinned grid profile; the production writer uses `production-v2`. Older datasets
are not migrated or reinterpreted; a new successful calculation must replace
an older fixture.
Older version identifiers below remain solely as historical numerical evidence,
not as supported serialized formats.

Historical performance evidence is intentionally reported separately from the scientific
equations. On immutable ICON-EU run `2026080812` (manifest SHA-256
`85d94a87e24a5eba4775baf4e46a4151c8c30021f51af6c54299cdbbabc5482e`), the
eight-CPU production-v2/v28/v22 calculation covered all 72 hours `f002..f073`
and all 129 nodes. Preloading 2,863 source columns took `357.118 s`; total wall
time was 33 min 14 s and peak cgroup memory was 15,127,642,112 bytes. Of 9,288
node-hours, 9,284 were available. Four physical spans of
`0.45534076..2.12574664 mm` were shorter than the positive-weight GL2/GL1
domain and failed closed as `integration_nonconvergence`; all other nodes were
available. The strict diagnostic consequently returned non-zero and is not
described as a 9,288/9,288 pass. Its report and executed test binary have
SHA-256 values `00977e620d60029e3e3f23f4aeb31352d614c9e75af6fa9cb8050f1830afdb31`
and `7831ec7a451930890645e6baba42cb5ea39322e4075ee6c935909c4002fef518`,
respectively. This is a v28/v22 baseline and not a measurement of the current
v29/v23 writer. Repeated cold/warm cycles, simultaneous synchronization, payload
measurement, and observational validation remain separate gates.

This numerical revision does not alter the product contracts: a dataset has
one to 72 consecutive native hourly forecast frames. An ordinary successfully
saved user visualization is retained for exactly 96 hours. One explicitly
marked `admin_fixture` may be retained without expiry. It is a read-only public
reference for unauthenticated visitors and remains visible to configured
Telegram administrators. An authenticated non-administrator cannot list or
open it and sees only owner-scoped 96-hour results. The fixture is a test
archive, not a cache hit eligible for new calculations.

This remains a reproducible NWP diagnostic, not a measurement. ICON cannot
resolve local obstruction, dome seeing, telescope thermal plumes, or sub-grid
cloud/turbulence. Fixed particle radii, cloud guards, HMNSP99, and the
engineering `1…10` utility require observational validation. Slant PWV is
physical, but no line-by-line passband/instrument transmission is claimed.
Refraction stops at ICON top and is not yet a vacuum-direction correction.
The one-sided endpoints and three quarter points now seed recursive coverage of
every complete horizontal-cell span; they are not the only samples. Endpoint
slivers require their own Lipschitz clearance, both halves are recursively
explored, and every remaining interval is either proved root-free, resolved by
the strict physical-surface monotonicity certificate (A26g), represented by
exact or sign-changing evidence, or rejected. No unresolved leaf is published.
For a root accepted through (A26g), opposite signs and a derivative interval
excluding zero formally prove both existence and uniqueness. This applies to
smooth bilinear WMO decision residuals as well as the eligible smooth physical
boundaries, while preserving the ancestor's whole-probe existence proof.
Predicates that cannot obtain that certificate retain the generic terminal
sign-change rule. Whenever recursion exposes separate non-identical evidence,
including evidence inside the 0.4-mm numerical-proximity window, the calculation
fails closed. This residual finite-resolution limitation is stated explicitly
and is not converted into a quality or confidence value.

Before isolation, every grid line reachable from each accepted DOPRI interval
is enumerated from an outward angular-reach enclosure. Equal endpoint signs do
not suppress a candidate, and sub-centimetre source intervals are not skipped.
Latitude and longitude events form a compound corner only for exact scalar
zeros at the identical path coordinate; overlap of two finite root brackets is
insufficient. A path that follows, or is numerically inseparable from, a grid
line therefore fails closed until a future explicit adjacent-cell ownership
contract can provide union derivative bounds. This is an availability
limitation, not a silent interpolation or side-selection rule.

An angular-discretization sensitivity run on immutable ICON-EU cycle
`2026080812` evaluated `f002/f020/f038/f056/f073` on 789 distinct directions:
production-v2, dense-v1, and an independent 513-node uniform-32 reference were
all computed through the complete physical kernel, with no interpolation of
finished diagnostics and no calculation or availability mismatch. The
production-v2 comparison had maximum Overall delta `4.495704`, worst
area-weighted hourly P95 `0.821550`, at most `8.565771%` of cap area above
`0.5`, and worst P95 `3.457452` for reference-cell centres below `20°` (cells
covering `10..17.5°`). Dense-v1 reduced those
four maxima to `1.744252`, `0.794451`, `7.393201%`, and `0.455779`. Preload and
calculation took `391.354 s` and `715.054 s`; report SHA-256 is
`3811f777a2c7ca471838d276bbb3acf974ac182587ef9c58fb257d1db8b31466`.
The comparison assigns each fine reference cell the nearest support-node
value with exact spherical-ring midpoint area weights; it does not create a
new scientific value. Because no scientifically sourced acceptance threshold
was specified before execution, the zero-failure `PASS` is only a successful
diagnostic execution, not proof that production-v2 is converged. In
particular, the low-elevation discretization remains a material controlled-
rollout limitation.

Public rollout therefore remains conditional on multi-site/multi-run angular-
grid acceptance criteria, cold/warm resource measurements, payload limits,
ordinary-forecast non-regression, and observational validation.

Numerical basis: Piessens et al.,
[DOI 10.1007/978-3-642-61786-7](https://doi.org/10.1007/978-3-642-61786-7);
Gander and Gautschi,
[DOI 10.1023/A:1022318402393](https://doi.org/10.1023/A:1022318402393).
