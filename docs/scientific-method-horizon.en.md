# Astrosferum scientific method: directional Horizon

[← Data-source contracts](scientific-method-data-sources.en.md) ·
[Main scientific method](scientific-method.en.md) ·
[Astrodome physical model →](scientific-method-astrodome-physics.en.md)

**Status:** canonical section 7 of scientific method version 2.2. Splitting
the method across files changes presentation only; equations, numbering,
provider contracts, and scientific interpretation are unchanged.

## 7. Directional Horizon analysis

### 7.1. Scope, time, and provider

The optional Horizon product answers a directional question for the 72 native
ICON-EU intervals `f001..f072`: in which of eight compass directions are
conditions least obstructed along a straight line of sight launched at
`10 deg` **geometric** elevation? `f000` is excluded because no physical
one-hour precipitation interval precedes the analysis time. The
directions and azimuths are
`N=0`, `NE=45`, `E=90`, `SE=135`, `S=180`, `SW=225`, `W=270`, and `NW=315 deg`.
The scientific contract is versioned as
`horizon-spherical-straight-los-native-mh-logp-glo30-informational-v13`.

All eight directions use that same `f001..f072` time axis. Native hourly
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

### 7.2. Spherical straight-line geometry

The production Horizon path is the following spherical straight chord. It does
not invoke the Ciddor/Dormand--Prince refraction solver of section 8; Astrodome
continues to use that full refracted geometry. With
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
`R` is the GRS80/IUGG arithmetic mean radius
`R_1=(2a+b)/3=6,371,008.7714 m`, rounded to 0.1 m; see
[Moritz (2000)](https://doi.org/10.1007/s001900050278). This is an explicit
spherical approximation for the fast product, not a WGS84 ellipsoidal ray.

At sea level this is `s_top=121.927 km` and `x_top=119.662 km`. Ground-distance
boundaries are at no more than `0.5 km`; the midpoint of each resulting chord
segment supplies one model lookup and the exact difference in `s` supplies its
quadrature length `ds`. At 10 degrees a full ground step changes ray altitude
by about 87 m, avoiding coarse PBL/cloud aliasing. The current constants produce
240 samples per direction at sea level.
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
to omitted refraction and unresolved terrain become much larger, whereas
`20 deg` is no longer a useful near-horizon diagnostic. Equations [F13]--[F15]
define the published Horizon path; they must not be interpreted as an apparent
or refracted coordinate.

### 7.3. Directional turbulence along the straight ray

Published `HorizonResult` turbulence and wind values are evaluated at the
500 m ground-track midpoint panels of [F13]--[F15]. Equations [F16]--[F17]
define the current v13 production calculation. Only raw pressure-level state is
interpolated in time; every nonlinear optical result is recomputed hourly.

At each segment midpoint and forecast hour, the ray altitude selects exactly one
local turbulence kernel already used by ordinary Overall: Masciadri/ICON TKE below
`h_surface + MH`, for positive native `MH`, and HMNSP99 above it. Model values are
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
top and explicitly lowers the composite data-quality heuristic.

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
molecular optical air mass for turbulence air mass. Omitted atmospheric
refraction remains a stated Horizon limitation; real vertical inhomogeneity is
represented by the sampled `C_n^2` profile.

### 7.4. Cloud, fog, precipitation, and model-surface closure

Published cloud transmission is evaluated from the same straight-ray midpoint
panels. Equation [F18] and its grouping by unique horizontal ICON cell and
height tier define the current v13 closure. Fog and precipitation are hourly
observer-cell diagnostics and therefore identical in all eight directions.

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

The current model-terrain screen at every midpoint is

```math
\mathrm{HHL}_{\mathrm{surface}}(\mathrm{midpoint})
\ge h_{\mathrm{ray}}(\mathrm{midpoint}).
\tag{F19}
```

Both sides of [F19] are absolute metres MSL.

A model-terrain block is an explicit index-1 veto. It is reported as a
calculated terrain veto only when the mandatory pressure/cloud state still
closes the atmospheric path; otherwise the cell remains unavailable, carries
both `model_terrain` and `unavailable_data`, and does not present zero-default
seeing, coherence time, or cloud transmission as physical diagnostics. This is a discrete midpoint
screen against the gridded ICON HHL surface, not a continuous intersection
solver and not a measured skyline. Ordinary Overall already uses observer-cell HHL as
its AGL origin; neither product adds a separate altitude bonus or penalty. HHL
is neither a local DEM nor an optical skyline
model and does not resolve local terrain or obstructions. The serialized
provenance is therefore `icon_hhl_model_surface`; the UI must say model
surface and must not call it an actual or surveyed horizon. DWD
describes ICON output and orography as grid-cell means in the
[ICON model description](https://www.dwd.de/EN/research/weatherforecasting/num_modelling/01_num_weather_prediction_modells/icon_description.html), and
[the ICON tutorial](https://www.dwd.de/DE/leistungen/nwv_icon_tutorial/pdf_einzelbaende/icon_tutorial2025.pdf)
defines HHL as vertical half-level height; neither is a local survey.

The implemented fine-terrain screen is a separate static Copernicus DEM GLO-30
line-of-sight product. It never replaces HHL in pressure, temperature,
humidity, or refractive-index reconstruction. The source is a digital surface
model (DSM), so vegetation, buildings, and infrastructure may contribute to
the returned surface; it is not a surveyed optical horizon. The public AWS
distribution used by the implementation is the 2021 GLO-30 release in
geographic WGS 84 (`EPSG:4326`), with orthometric heights in metres relative to
EGM2008. The provider describes a nominal 30 m product and quotes absolute
vertical accuracy below 4 m at 90% linear error; those source specifications
are not an uncertainty estimate for the derived skyline.

For observer DSM-surface orthometric height `h_0`, fixed aperture offset
`h_a=2 m`, an accepted native raster-cell centre `p` with DSM height `h_p`,
authalic project radius `R=6371008.8 m`, great-circle distance `s_p`, and
initial bearing `A_p`, the direct spherical elevation of that cell is

```math
\begin{aligned}
\alpha_p&=\frac{s_p}{R},\\
e_p
&=\mathrm{atan2}\!\left(
[R+h_p]\cos\alpha_p-[R+h_0+h_a],
[R+h_p]\sin\alpha_p
\right),\\
b(p)&=\left\lfloor(A_p+0.5^\circ)\bmod360^\circ\right\rfloor,\\
\gamma_k&=\max_{p:\ 0<s_p\le61000\,\mathrm{m},\ b(p)=k}e_p,
\qquad k=0,\ldots,359.
\end{aligned}\tag{F19a}
```

Equation [F19a] is direct geometric occlusion without atmospheric refraction.
The implementation visits every native GLO-30 raster-cell centre within
61 km exactly once and assigns it to one nearest integer-degree azimuth bin;
there is no sparse radial sampling and no hourly DEM work. The 61 km boundary
is a versioned numerical guard: with the accepted DSM range `-500...10500 m`
and the 2 m aperture, even the highest admissible surface above the lowest
admissible observer is below 10 degrees at 61 km, so a farther accepted cell
cannot reach the product's 10-degree reporting threshold. The full profile is built
once for exact source-object manifest and coordinates quantized to `10^-5 deg`,
then reused for every forecast hour.

The AWS COG adapter validates the exact tile ID, latitude-band raster width,
`3600` rows, Point grid, EPSG:4326, Float32 storage and unpacked DGED height
semantics. Heights are metres in EGM2008, with canonical `NoData=-32767`,
`scale=1`, and `offset=0`; NoData is masked before observer sampling or skyline
maxima. The source-object ETag, byte length and SHA-256 form a sorted manifest.
The admission/preparation key is not a scientific-result identity: after a
cold build, Horizon and Astrodome publish only under a final key containing
the exact profile and manifest SHA-256 digests.

The eight Horizon directions own disjoint half-open 45-degree sectors. For
`i=0,...,7`, with N at `i=0`, the exact integer-degree ownership rule is

```math
\begin{aligned}
S_i&=\left\{k\in\{0,\ldots,359\}:\
\left\lfloor\frac{(k+22.5)\bmod360}{45}\right\rfloor=i\right\},\\
\overline\gamma_i&=\frac{1}{45}\sum_{k\in S_i}\gamma_k,\\
\gamma_{i,\max}&=\max_{k\in S_i}\gamma_k.
\end{aligned}\tag{F19b}
```

Every one-degree sample belongs to exactly one sector, including the circular
N boundary. The Horizon chart displays `mean/maximum` and publishes the
informational predicate `gamma_i,max >= 10 deg`, so a narrow peak cannot be
lost in the displayed mean. This predicate reports that at least one azimuth
inside the sector reaches the evaluation elevation; it does not alter
availability, the atmospheric index, or its limiting factors. Astrodome keeps
all 360 samples. At a grid-node
azimuth `A=k+u`, `0<=u<1`, it uses only cyclic interpolation of the source
terrain profile,

```math
\gamma(A)=(1-u)\gamma_k+u\gamma_{(k+1)\bmod360},
\qquad I_{\mathrm{terrain}}(A,e_{\mathrm{node}})
=\mathbf{1}\!\left[e_{\mathrm{node}}\le\gamma(A)\right].
\tag{F19c}
```

No seeing, coherence time, cloud transmission, Overall, or confidence-like
quantity is interpolated by [F19c]. The Astrodome always retains the completed
refracted atmospheric cell, including its state, Overall, physical diagnostics,
quality, and limiting factors. It serializes `gamma(A)` and the informational
boolean `I_terrain` separately for display. The DEM profile itself remains
direct and static and is not a surveyed or refracted optical horizon. A true
ICON-HHL intersection or missing mandatory atmospheric input remains
fail-closed and may coexist with the informational GLO-30 fields. Missing or
invalid source tiles fail directional preparation when the feature is enabled.
Disabled operation is serialized explicitly as `source=disabled`; no flat or
HHL-derived substitute is fabricated.

Horizon serializes the sector predicate as
`terrain_sector_has_obstruction_at_evaluation_elevation`. The independent
`terrain_blocked` field remains reserved for an ICON-HHL intersection that
prevents completion of the mandatory atmospheric path. The two states are not
aliases and the browser must not infer one from the other.

Product definition and accuracy are from the
[Copernicus DEM collection description](https://dataspace.copernicus.eu/explore-data/data-collections/copernicus-contributing-missions/collections-description/COP-DEM)
and [Product Handbook](https://dataspace.copernicus.eu/sites/default/files/media/files/2024-06/geo1988-copernicusdem-spe-002_producthandbook_i5.0.pdf).
The distribution and access contract are documented by the
[Registry of Open Data on AWS](https://registry.opendata.aws/copernicus-dem/).

### 7.5. Overall and limiting factor

Production evaluates the same calibrated multiplicative Overall policy used by
the ordinary forecast from straight-ray physical seeing, `tau0`, cloud
closure, observer wind, fog heuristic, and hourly precipitation veto. Equation
[F20] defines that current mapping.

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
q_{\mathrm{precip}}
&=\begin{cases}
0,&R_{1\mathrm{h}}\ge R_{\mathrm{detect}},\\
1,&R_{1\mathrm{h}}<R_{\mathrm{detect}},
\end{cases}\\
Q_H&=f_{\mathrm{turbulence},H}T_H^{w_{\mathrm{cloud}}}
q_{\mathrm{surface}}q_{\mathrm{fog}}q_{\mathrm{precip}},\\
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

An ICON-HHL intersection of the atmospheric ray forces index 1 because the
mandatory directional atmospheric path cannot be completed honestly. The
separate GLO-30 sector skyline is informational and does not force index 1.
An incomplete direction is marked unavailable
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

Precipitation at or above the same hourly `R_detect=0.05 mm` default as ordinary
Overall is an operational veto and becomes the primary limiter; it does not
rewrite the separately reported seeing or cloud diagnostics. Daylight,
Moon/planet position, dew, Bortle class, aerosols, molecular Rayleigh
extinction, and user equipment are not separate terms in [F20].
Daylight and twilight are only visual bands on the hourly heatmap. Rayleigh
optical depth is a
real low-elevation attenuation described by
[Bodhaine et al. (1999)](https://doi.org/10.1175/1520-0426(1999)016%3C1854:ORODC%3E2.0.CO;2),
but it is not penalized because every sector is evaluated at the same elevation
and the current product has no complete aerosol/extinction closure. Its omission
must not be interpreted as a transparency prediction.

### 7.6. Lead-time and data-quality heuristics are not probabilities

The pure lead-time diagnostic is named `LeadTimeQualityHeuristic`:

```math
C_{\mathrm{lead}}
=\max\!\left(0.65,
0.96-0.26\frac{h_{\mathrm{native}}}{72}\right).
\tag{F21}
```

It is a deterministic ordering rule, not a probability, confidence interval,
or expected forecast error. Horizon combines it with straight-path coverage,
turbulence/cloud profile coverage, and ring availability as

```math
Q_{\mathrm{data}}
=\min\!\left(0.85,
0.50\min(P_{\mathrm{turbulence}},P_{\mathrm{cloud}})
+0.30C_{\mathrm{lead}}+0.20R_{\mathrm{dir}}\right),
\qquad
R_{\mathrm{dir}}=\frac{N_{\mathrm{available\ directions}}}{8},
```

as `DataQualityHeuristic`; an unavailable direction reports zero. This scalar
is a compact presentation diagnostic only. It does not replace the individual
components and is not used to recalculate atmospheric science.

Here `h_native` is the native three-hour pressure-profile term used before
same-run interpolation, so intermediate hours linearly interpolate the two
bracketing values of this ordering heuristic together with the raw profile.
The categorical quality is derived from that explicit Horizon heuristic. An
available path is `good` only when `C_lead>=0.85` and `Q_data>=0.60`, `usable`
when `0.75<=C_lead<0.85` and `Q_data>=0.60`, and otherwise `limited`;
missing mandatory closure is `unavailable`. No HHL uncertainty is hidden
inside a quantity named statistical confidence.

### 7.7. Validation and remaining limitations

Unit regressions cover the exact eight-ray geometric-10-degree plan and its
digest, spherical endpoints, midpoint source extraction, raw 3-hour pressure
interpolation, hourly precipitation differencing and veto, unavailable/terrain
states, heuristic quality semantics, the `f001..f072` interval, and ordered
time-by-eight-direction output. Astrodome section 8 numerical/full-run gates
remain mandatory for Astrodome but are not evidence for the separate straight
Horizon path; Horizon requires its own real ICON-EU smoke and timing gate.

Every release validation runs one real calculation from a current ICON-EU run,
an explicit ICON Global case with neither button nor job, localized readable
rendering, callback/queue/cache tests, full Go
lint/build checks, and production latency comparison while an ordinary
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
