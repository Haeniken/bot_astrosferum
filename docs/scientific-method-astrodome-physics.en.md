# Astrosferum scientific method: Astrodome physical model

[← Main scientific method](scientific-method.en.md) ·
[Certified geometry and root isolation →](scientific-method-astrodome-numerical-roots.en.md)

**Status:** part of the canonical scientific method, version 2.2. Splitting the
method across several files changes presentation only and allows GitHub to
render every mathematical expression reliably.

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

The separate calculation-request schema is v6. It carries the fixed science
and path versions, the apparent-direction contract, the complete validated
static terrain skyline and its digest, and
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
MH,\qquad MH>0.
\tag{A13}
```

No project clamp replaces this provider primitive; missing/non-positive `MH`
or insufficient native vertical support is unavailable. Below it, the
Masciadri TKE kernel is evaluated pointwise; above it, HMNSP99's
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
