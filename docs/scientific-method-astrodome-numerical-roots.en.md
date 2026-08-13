# Astrosferum scientific method: certified geometry and root isolation

[← Astrodome physical model](scientific-method-astrodome-physics.en.md) ·
[Main scientific method](scientific-method.en.md) ·
[Quadrature and model-top closure →](scientific-method-astrodome-numerical-integration.en.md)

**Status:** part of the canonical scientific method, version 2.1. Splitting the
method across several files changes presentation only and allows GitHub to
render every mathematical expression reliably.

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
