# Astrosferum scientific method: quadrature and model-top closure

[← Certified geometry and root isolation](scientific-method-astrodome-numerical-roots.en.md) ·
[Main scientific method](scientific-method.en.md)

**Status:** part of the canonical scientific method, version 2.1. Splitting the
method across several files changes presentation only and allows GitHub to
render every mathematical expression reliably.

## 8.8 (continued). Side guards, quadrature, and model-top closure

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
sliver is exposed as a limited-quality approximation under (A28g). Kernel v30
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
- science kernel: `astrodome-science-kernel-v33-glo30-informational-skyline`.

Kernel v33 retains the v30 atmospheric integration core and the v32 static
GLO-30 profile, but makes that profile an independent informational skyline:
it no longer replaces a completed atmospheric node state. Kernel v30 and path contract v23 use one strict boundary-ownership contract
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
part of path v23. Kernel v30 retains the removal of the ill-conditioned
extrapolatory Q5/Q3 path, adds positive-weight GL3/GL2 and GL2/GL1 pairs, and
uses the limited positive midpoint path below the GL2 floor under the explicit
one-metre cumulative approximation ceiling. Rule-specific recursion allocates
absolute tolerance by represented length, gives the right child the exact
residual budget, and leaves relative tolerance unchanged. The planner must
certify raw WMO decision breakpoints before quadrature; every partition
mismatch in the kernel remains fail-closed.

The bot calculator and cache, plus the independent site's dataset decoder and
browser, accept only the current v33/v23 contract with an explicitly supported
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
v33/v23 writer. Repeated cold/warm cycles, simultaneous synchronization, payload
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
