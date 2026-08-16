# Astrosferum scientific method: optical turbulence, clouds, and Overall

[← Main scientific method](scientific-method.en.md) ·
[Planning ephemerides →](scientific-method-ephemerides.en.md)

**Status:** canonical sections 3 and 4.1–4.9 of scientific method version 2.2.
Splitting the method across files changes presentation only; formulas,
numbering, provenance, and scientific interpretation are unchanged.

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
The discrete implementation searches the first qualifying native-level WMO
tropopause above the project 5-km floor. If it cannot diagnose one, the
200-hPa branch switch is a project fallback. It does not model a second WMO
tropopause or tropopause folds. These are declared limitations; no repeated
HMNSP99 branch switch is invented without a published mapping. Layer HMNSP99
uses arithmetic midpoint `P` and `T`; this is a numerical discretization, not
a formula prescribed by Wu et al., and remains subject to a vertical-refinement
benchmark.

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

For real-valued arithmetic, raising the signed gradient to `4/3` is identical
to `|dtheta/dz|^(4/3)` and `Cn²` must be non-negative; the modulus therefore
does not constitute a sign error. It also does not classify stable, neutral,
convective, or cloudy PBL regimes. The applicability and calibration of this
closure in those regimes is missing observational evidence. The Masciadri and
HMNSP99 one-sided `Cn²` values are not forced to match at `MH`; their
non-overlapping integrals have no gap or overlap, but boundary sensitivity and
the one-sided ratio remain diagnostics requiring validation. An arbitrary
`0.8…1.2 MH` blend is not part of the published method.

The two non-overlapping regions are integrated and converted to seeing:

```math
\begin{aligned}
h_{\mathrm{PBL}}
&=\mathrm{ICON\_MH}\ \mathrm{AGL},\qquad \mathrm{ICON\_MH}>0,\\
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
The current contract uses the positive native ICON mixed-layer depth without
a project clamp. If `MH` is missing, non-positive, or extends beyond the
contiguous native TKE support, the result is unavailable rather than being
computed with a substituted boundary.

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
\mathrm{profile\ quality}=\mathrm{model\ domain\ complete}
&\iff C_z\ge0.99\ \land\ C_h\ge0.99\ \land\\
z_{\mathrm{top}}\ge18\ \mathrm{km\ AGL},\\
\mathrm{Overall\ profile\ gate}=\mathrm{pass}
&\iff C_z\ge0.90\ \land\ C_h\ge0.90\ \land\\
z_{\mathrm{top}}\ge15\ \mathrm{km\ AGL}.
\end{aligned}\tag{F5c}
```

The serialized value is `model_domain_complete`: it means complete inside the
retained model domain, not complete atmosphere. Otherwise a valid
integrated profile is marked `limited`; an invalid profile is `unavailable`.
`C_h` exposes the stronger effect of upper-level gaps on `theta0`. Overall
rejects an hour below either `90%` structural threshold or with a model/profile
top below `15 km AGL`.
An hour that passes that safety gate but whose `ProfileQuality` is anything
other than model-domain complete retains its physical result and is marked partial with
`!`; a fully covered profile reaching `15…18 km AGL` is therefore usable only
as partial. A positive, unmodelled `Cn²` tail above `z_top` can only increase
`J`, `J_V`, and `J_h`; therefore no finite full-atmosphere error bound is
claimed from these structural ratios alone. Neither the
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
The current Astrodome dataset schema is `8`. Its penalty semantics remain the
bounded product definition above; schema 8 carries the explicit heuristic
contracts plus the versioned observing ephemerides and Polaris catalog
contract from section 4.10, the static GLO-30 terrain profile from section 7.4, and the
per-node informational skyline elevation/obstruction predicate. The narrow
`astrodome-dataset-writer-v9-observing-ephemerides` identity participates in the
calculation cache key. Calculation-request schema 6 also binds the
`celestial-observing-ephemerides-jpl-meeus-wgs84-h0-v4` ephemeris
identity, so an older or incompatible payload cannot be reused.
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

This missing-data interpretation is versioned as
`overall-astronomy-index-v3-native-mh-logp`: a direct `VIS`
field can support `FogHeuristic` even when PWV is absent and the separate
transparency heuristic is therefore unavailable. Missing PWV never means
clear air or zero water vapour.

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
forecast time uses the positive ICON single-level `MH` field (ecCodes `mld`,
mixed-layer depth in metres) directly:

```math
h_{\mathrm{PBL}}
=\mathrm{MH}\ \mathrm{AGL},\qquad \mathrm{MH}>0.
```

No synthetic lower or upper boundary is substituted. Each result records the
native `h_PBL` actually used. Missing/non-positive `MH`, or native TKE support
that does not reach this boundary, fails closed. The applicability of the
Masciadri/TKE closure in neutral, convective, or cloudy PBL regimes remains an
observational-calibration limitation; the non-negative `|dtheta/dz|^(4/3)`
term does not by itself distinguish these regimes.

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
&=\mathrm{ICON\_MH},\qquad \mathrm{ICON\_MH}>0,\\
J_{\mathrm{GL}}&=\int_{0}^{h_{\mathrm{PBL}}\ \mathrm{AGL}}C_n^2\,dz,\\
J_{\mathrm{FA}}&=\int_{h_{\mathrm{PBL}}\ \mathrm{AGL}}^{\mathrm{model\ top}}C_n^2\,dz,\\
J_{\mathrm{total}}&=J_{\mathrm{GL}}+J_{\mathrm{FA}}.
\end{aligned}
```

When the exact PBL cut lies between two positive pressure levels, pressure at
the cut is reconstructed hydrostatically in log space,
`p(f)=exp((1-f)ln(p0)+f ln(p1))`; it therefore stays positive, monotone, and
equals `sqrt(p0*p1)` at the geometric midpoint. Temperature and wind retain
linear primitive interpolation. No finished `Cn²`, seeing, or `tau0` value is
interpolated.

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

After adding `MH`, the field was `396 m` at both control leads. The native
396 m cutoff produced `1.9075″` at `f042` and `2.5158″` at `f048`; the former
500 m lower-clamp implementation produced `1.9145″` and `2.5478″`,
respectively. These historical server-only values demonstrate sensitivity to
the boundary; they are not observational calibration. Full hourly production
run `2026072106` was subsequently published
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

Fog can physically block observations, so the multiplier follows the
threshold-based warning `FogHeuristic`:

```math
q_{\mathrm{fog}}=
\begin{cases}
1.00, & \text{no heuristic fog signal},\\
0.75, & \text{possible-fog heuristic signal},\\
0.10, & \text{high fog-heuristic signal}.
\end{cases}
```

The three classes combine model `VIS`, relative humidity, and `T-Td`. They are
an uncalibrated warning heuristic, not a categorical observation or a
site-specific probability of radiation/advection fog. Model terrain cannot
resolve every hollow, slope, or coastal microscale circulation.

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
| PBL boundary | positive native ICON `MH`, fail closed outside TKE support |
| unresolved `CLC` guard: low / middle / high | `0.45 / 0.2475 / 0.081` |
| possible / high `FogHeuristic` factor | `0.75 / 0.10` |
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

The fixed-2-km comparison values `2.221″/3.644″`, native `MH=396 m` values
`1.9075″/2.5158″`, and former lower-clamp values `1.9145″/2.5478″` demonstrate
sensitivity to PBL treatment but are not ground truth. Production run
`2026072106` with
`surface-hourly-v17` and `cloud-hourly-v4` is synchronized and passed
post-deploy verification: 69 hours gave Overall `1.00…4.58`, no exact `10`,
and seeing `0.72…2.84″`. The next scientific
step is an hourly comparison against DIMM/MASS/SCIDAR or high-quality observing
logs around Saint Petersburg and Moscow. Until then, the
UI must say “model estimate” and must not call Overall a measured seeing value
or a Pickering scale.
