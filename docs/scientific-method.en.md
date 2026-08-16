# Scientific method and Overall Astronomy Index

**Author:** Sergey Borzenkov\
**ORCID:** [0009-0005-5804-5011](https://orcid.org/0009-0005-5804-5011)\
**Project:** Astrosferum\
**Document type:** Research-software methodology and calculation note\
**Version:** 2.2\
**Revision date:** 16 August 2026

Status: research-software method and calculation note, revised 16 August 2026.
This document is the canonical description of sources, units, formulas,
control calculations, validation, uncertainty, and configurable engineering
decisions in `bot-astrosferum`.

The directional Horizon method published in
[section 7](scientific-method-horizon.en.md) is an implemented user-facing
capability. It is enabled by configuration, is deliberately limited to
ICON-EU, and follows the same reproducibility and current-run requirements as
the ordinary forecast.

The directional atmospheric Astrodome method in section 8 is implemented for
ICON-EU in this repository. Its presentation and saved-visualization catalogue
are deployed from the independent private `site-astrosferum` repository.
Production-v2/v34/v24 is the current writer; the complete v28/v22 numerical
measurement recorded in the linked section 8 appendices is an explicitly historical baseline;
multi-cycle resource, release, and observational validation remain separate
gates and do not change the formula contract.

## Document map

- Sections 1–2 define scientific status, outputs, and provenance.
- [Sections 3 and 4.1–4.9](scientific-method-overall.en.md) contain the
  canonical formula ledger, implementation details, calibration parameters,
  and control calculations for the ordinary point forecast.
- [Section 4.10](scientific-method-ephemerides.en.md) specifies the planning
  ephemerides and displayed sky paths.
- Section 5 defines reproducibility, validation, and responsible use.
- [Section 6](scientific-method-data-sources.en.md) records the measured
  data-source contracts.
- [Section 7](scientific-method-horizon.en.md) specifies the optional
  directional Horizon method.
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
[section 6](scientific-method-data-sources.en.md).


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

Together, the three files form the canonical section 8 of method version 2.2.
