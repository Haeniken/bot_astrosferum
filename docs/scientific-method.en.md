# Scientific method and Overall Astronomy Index

**Author:** Sergey Borzenkov\
**ORCID:** [0009-0005-5804-5011](https://orcid.org/0009-0005-5804-5011)\
**Project:** Astrosferum\
**Document type:** Research-software methodology and calculation note\
**Version:** 2.0\
**Revision date:** 12 August 2026

Status: research-software method and calculation note, revised 12 August 2026.
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
- Canonical section 8 is published as three linked parts: the Astrodome
  physical model, certified geometry and root isolation, then quadrature and
  model-top closure.

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
- hourly topocentric planning ephemerides for the Sun, Moon, Mercury, Venus,
  Mars, Jupiter, Saturn, Uranus, Neptune, and Pluto;
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
The current Astrodome dataset schema is `3`. Its penalty semantics remain the
bounded product definition above, while schema 3 additionally carries the
versioned celestial distance and ring-aspect diagnostics from section 4.10.
The narrow `astrodome-dataset-writer-v4-celestial-distance-aspect` identity participates in the
calculation cache key, so an older or incompatible payload cannot be reused.
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

### 4.10. Planning ephemerides and displayed sky paths

The celestial overlay is an independent planning product and **does not enter
Overall, seeing, cloud transmission, or any meteorological value**. Its
canonical order is Sun, Moon, Mercury, Venus, Mars, Jupiter, Saturn, Uranus,
Neptune, and Pluto. Earth is the observer and is therefore not a displayed
target. Every stored sample is evaluated at the exact UTC hour of the forecast
axis; azimuth is geodetic, clockwise from true north, and altitude is measured
from the local astronomical horizon. The current location type has no observer
height, so the topocentric observer is placed on the reference ellipsoid at
zero height. This approximation is negligible at the plotted scale but must be
remembered close to the horizon. The common declared numerical domain is
`1885-01-02 <= t < 2050-01-01`. The one-day lower-bound margin keeps the
light-time-retarded Pluto state inside the series' published interval; the
upper boundary remains exclusive. Requests outside this domain fail closed
instead of silently switching approximation families.

For Mercury through Neptune, the implementation uses the fitted Keplerian
elements published by [JPL Solar System
Dynamics](https://ssd.jpl.nasa.gov/planets/approx_pos.html). This is a
**published planning approximation**, not a replacement for an integrated DE
ephemeris. With Julian ephemeris date `JDE`, the number of Julian centuries
from J2000 and the linearly propagated elements are

```math
T = \frac{JDE-2451545.0}{36525},
\qquad
x(T)=x_0+\dot{x}T.
```

The argument of perihelion, corrected mean anomaly, Kepler equation, and
orbital-plane coordinates are

```math
\omega=\varpi-\Omega,
```

```math
M=L-\varpi+bT^2+c\cos(fT)+s\sin(fT),
```

```math
M=E-e\sin E,
```

```math
x'=a(\cos E-e),
\qquad
y'=a\sqrt{1-e^2}\sin E,
\qquad
z'=0.
```

The JPL rotation by `omega`, inclination `I`, and ascending node `Omega`
places this vector in the J2000 ecliptic frame. The implementation subtracts
the Earth-Moon-barycentre vector and performs one light-time iteration,

```math
\Delta_0=\lVert \mathbf r_p(t)-\mathbf r_{EMB}(t)\rVert,
```

```math
\mathbf r_g(t)=
\mathbf r_p\!\left(t-\frac{\Delta_0}{c}\right)-\mathbf r_{EMB}(t).
```

The JPL table provides the Earth-Moon barycentre rather than the Earth's
centre. A conservative `5,000 km` upper bound for their separation and a
conservative `0.25 au` lower bound for the geocentric distance of the displayed
planets give a directional contribution below

```math
\arcsin\!\left(
\frac{5000\ \mathrm{km}}{0.25\times149597870.7\ \mathrm{km}}
\right)<0.0077\ \mathrm{degree}.
```

This is a declared part of the planning approximation, not an unreported
high-precision Earth ephemeris.

#### Distance, model-range closeness, radial motion, and Saturn rings

The distance diagnostic is separate from sky position and observing
suitability. For the Sun and Moon, `D(t)` is referenced to the Earth centre.
For planets it is referenced to JPL's Earth–Moon barycentre and the
light-time-retarded target, so the serialized field is
`reference_distance_km`, not an exact geocentric range. It does not enter
Overall.

The project closeness scale uses the finite set of every whole UTC hour in the
common model domain:

```math
\mathcal T_h=\{1885\text{-}01\text{-}02T00{:}00Z+n\,\mathrm{h}\mid t<2050\text{-}01\text{-}01T00{:}00Z\}.
```

For each body, the checked-in `D_min` and `D_max` are generated by exhaustive
evaluation of every member of `T_h` with the same ephemeris version. A runtime
distance outside the stored range is rejected; there is no silent clamp. For
an exact hour,

```math
C(t)=100\frac{D_{\max}-D(t)}{D_{\max}-D_{\min}}\ \%,
\qquad t\in\mathcal T_h.
```

`100%` therefore denotes the closest sampled hour of this approximate model,
and `0%` the farthest. It is not a probability, brightness, angular size,
physical percentage change, periapsis/apoapsis, or a continuous global
extremum. Chart 1 shows the integer-rounded arithmetic mean over the exact
hourly samples present in that forecast,

```math
\bar C_N=\mathrm{round}\!\left(\frac{1}{N}\sum_{i=1}^{N}C(t_i)\right),
```

whereas the selected-hour interactive value is displayed to two decimal
places. That difference is presentation precision only.

Signed radial velocity uses a centred one-hour difference, with one-sided
stencils only at the model-domain boundaries:

```math
v_r(t)=\frac{D(t+30\,\mathrm{min})-D(t-30\,\mathrm{min})}{3600\,\mathrm{s}}.
```

The unit is `km/s`; `v_r<0` means approaching and `v_r>0` means receding,
matching JPL Horizons quantity 20. There is no arbitrary stationary deadband.
The `30 min` half-step is a numerical choice for an hourly planning product.
Independent DE441/Horizons controls at `2026-08-12T00:00Z` bound the tested
Mars–Pluto residuals by `2,000,000 km` in distance and `0.08 km/s` in range
rate. These are planning-regression limits, not guarantees for all epochs.

For Saturn, the signed ring-opening angle uses the IAU north pole at the
light-emission epoch:

```math
\alpha_p=40.589^\circ-0.036^\circ T,
\qquad
\delta_p=83.537^\circ-0.004^\circ T.
```

```math
B=\arcsin\!\left(\widehat{\mathbf p}_{N}\cdot
\widehat{\mathbf r}_{Saturn\rightarrow EMB}\right).
```

Positive `B` exposes Saturn's IAU north side; negative `B` exposes the south
side. Pole coefficients are from the [IAU Working Group report](https://doi.org/10.1007/s10569-017-9805-5),
and the sign follows the [NASA PDS ring-elevation convention](https://pds.nasa.gov/datastandards/documents/dd/all/current/ch105s175.html).
An independent Horizons direction-and-pole construction gives
`-8.98046 degrees` on `2026-08-12`; the implementation regression limit is
`0.08 degree`. `B` controls ring opening, not the full sky-plane position
angle of the rings.

It then rotates to J2000 equatorial coordinates and applies the Meeus
precession, nutation, and annual-aberration transformation to the apparent
place of date. Pluto is evaluated separately with the periodic series in
chapter 37 of Jean Meeus, *Astronomical Algorithms*, as implemented by the
pinned `github.com/soniakeys/meeus/v3` module. The Sun and Moon retain the
existing Meeus solar and lunar series; the Moon is topocentric before the
horizontal conversion.

For right ascension `alpha`, declination `delta`, geocentric distance
`Delta`, geodetic latitude `phi`, and local hour angle `H`, the topocentric
conversion uses the reference-ellipsoid flattening `f=1/298.257`. Define

```math
H=\mathrm{GAST}(t_{UT1})+\lambda_{east}-\alpha.
```

The equatorial coordinates are apparent places, therefore the rotation uses
Greenwich apparent sidereal time (GAST), including nutation in right ascension,
rather than GMST. The runtime has UTC but no live Earth-orientation input and
uses `UTC approximately equals UT1`. [IERS defines
GAST](https://www.iers.org/iers/en/service/glossary/functions/glossary/G) as
the Greenwich hour angle of the true equinox and keeps
[`absolute value of UT1-UTC` below `0.9 s`](https://www.iers.org/SharedDocs/Glossareintraege/EN/C/utc?nn=ef326957-149a-433c-8880-5730bfcd389d).
For the modern UTC regime since 1972, the latter corresponds to less than
`0.0038 degree` of Earth rotation. Before 1972, an input timestamp is treated
as a UTC-like proxy for UT1 and this `0.9 s` bound is not claimed. The modern
bound is an angular-direction bound; azimuth itself is ill-conditioned
arbitrarily close to the zenith.

Now define

```math
u=\arctan\!\big((1-f)\tan\phi\big),
\qquad
\rho_s=(1-f)\sin u,
\qquad
\rho_c=\cos u,
\qquad
p=\frac{R_\oplus}{\Delta}.
```

Then

```math
A=\cos\delta\sin H,
```

```math
B=\cos\delta\cos H-\rho_c p,
```

```math
C=\sin\delta-\rho_s p,
```

```math
H'=\mathrm{atan2}(A,B),
\qquad
\delta'=\arcsin\!\left(\frac{C}{\sqrt{A^2+B^2+C^2}}\right).
```

The airless altitude and north-through-east azimuth are

```math
h=\arcsin\!\left(
\sin\phi\sin\delta'+\cos\phi\cos\delta'\cos H'
\right),
```

```math
Az=\mathrm{atan2}\!\left(
-\cos\delta'\sin H',
\sin\delta'\cos\phi-\cos\delta'\cos H'\sin\phi
\right)\pmod{2\pi}.
```

The planning apparent altitude uses the existing standard-refraction
approximation only for `h >= -1 degree`; below that boundary it equals the
airless altitude:

```math
h_{app}=h+
\frac{0.0002967}
{\tan\!\left(h+\frac{0.00312536}{h+0.08901179}\right)}.
```

This refraction is a **project numerical convention** at standard conditions,
not a pressure/temperature measurement at the observing site. Both geometric
and apparent altitude are serialized, so the distinction is not hidden.

The browser draws a smooth dashed curve between adjacent exact hourly unit
directions with shortest-arc spherical linear interpolation. For `0 <= q <=
1`, `theta=acos(clamp(u0 dot u1,-1,1))`,

```math
\mathbf u(q)=
\frac{\sin((1-q)\theta)}{\sin\theta}\mathbf u_0+
\frac{\sin(q\theta)}{\sin\theta}\mathbf u_1.
```

The curve is subdivided into twelve presentation segments per hour (five
minutes). Slerp is **only drawing geometry**: the selected marker and tooltip
use the exact stored hourly sample, and no Overall or meteorological field is
interpolated. A deterministic great-circle fallback makes the mathematical
antipodal edge case total; adjacent physical hourly ephemerides are not
antipodal.

JPL describes the short-interval elements as suitable for scheduling and
pointing, with nominal heliocentric-longitude errors from `10` to `600`
arcseconds depending on planet. High-precision work must use [JPL
Horizons](https://ssd-api.jpl.nasa.gov/doc/horizons.html) or a current
integrated DE ephemeris. An independent Horizons `AIRLESS` observer-table
control at `53.65 N, 37.3462 E`, `2026-08-12 00:00 UTC` covers all ten bodies.
The largest difference is `0.0987 degree` in Saturn azimuth. A second control
contains thirty Mars-through-Pluto positions at five three-month dates from
`2026-01-15` through `2027-01-15`; its maxima are `0.0856 degree` in azimuth
and `0.0662 degree` in altitude. All sixty individual seasonal components and
all twenty all-body components stay below the fixed `0.15-degree` regression
limit. The Horizons query fixes `CENTER=coord@399`, the exact observer
coordinates at zero height, `TIME_TYPE=UT`, `QUANTITIES=4`, `ICRF`, `AIRLESS`,
`EXTRA_PREC=YES`, and DE441. These controls are evidence against sign, frame,
time, seasonal-propagation, and azimuth-wrap errors; they are not a global
accuracy calibration. The
photorealistic body icons are generated presentation assets and do not encode
angular diameter, orientation, phase, brightness, or scale.

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

To let GitHub render every mathematical expression reliably, the large
section 8 is published as three linked parts. This split changes presentation
only; the formulas, numbering, and scientific contract remain one whole.

- [Astrodome physical model](scientific-method-astrodome-physics.en.md):
  scope, source data, geometry, refraction, and line-of-sight physics.
- [Certified geometry and root isolation](scientific-method-astrodome-numerical-roots.en.md):
  primitive reconstruction, events, certified brackets, and roots.
- [Quadrature and model-top closure](scientific-method-astrodome-numerical-integration.en.md):
  side guards, adaptive quadrature, model top, quality, and validation.

Together, the three files form the canonical section 8 of method version 2.0.
