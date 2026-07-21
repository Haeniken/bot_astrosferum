# Scientific status and reproducibility

## Status

`bot_astrosferum` is research-oriented scientific software: it acquires numerical weather-prediction data, applies documented physical and engineering models, and produces reproducible diagnostics for observational astronomy and astrophotography.

It is not a peer-reviewed scientific result, a calibrated observatory instrument, or a substitute for local measurements. In particular, its seeing and Overall Astronomy Index are model-derived estimates. Absolute accuracy requires validation against DIMM, MASS, SCIDAR, site SQM, all-sky cameras, and structured observing logs.

## Research question and outputs

The software asks a practical question: how suitable is a coordinate and hour for astronomical observing, given modeled turbulence, wind, cloud obstruction, fog, daylight, and astronomical events?

The answer is separated into auditable outputs:

- meteorological conditions and tiered cloud cover;
- effective cloud obstruction by native model level;
- wind speed, vector shear, and direction-change diagnostics;
- wind-derived and combined astronomy-condition indices;
- Sun, Moon, Jupiter, and Saturn events;
- point light-pollution context, reported separately from the hourly index.

## Data provenance

Every forecast image identifies the provider, product, model run, grid, time zone, algorithm version, and renderer version. The primary weather source is DWD ICON-EU open data. Light-pollution context uses an operator-pinned annual Light Pollution Atlas and a separate World Atlas 2015 comparison.

Downloaded model runs and atlases are deliberately absent from Git: they are large, replaceable upstream artifacts. Runtime manifests and versioned cache keys prevent products generated from incompatible schemas from being mixed.

## Method

The full formulas, coefficients, assumptions, and source references are maintained in:

- [Overall Astronomy Index re-evaluation](research-overall-index.en.md);
- [KISS architecture](architecture.en.md);
- [data-source verification](data-source-spike.en.md);
- [implementation status](implementation-status.en.md).

The combined index uses native ICON thermodynamics and turbulence in the boundary layer, HMNSP99 in the free atmosphere, wind-weighted coherence time, phase-aware cloud optical depth, a tier-aware unresolved-cloud guard, fog, and a deliberately mild surface-wind factor. Dew and light pollution do not reduce the index.

## Reproducibility

For a reproducible result, record:

1. source revision or release tag;
2. `config.yaml` without secrets and all `ASTRO_OVERALL_*` overrides;
3. ICON run ID and manifest schemas;
4. coordinates, resolved IANA time zone, and forecast interval;
5. algorithm and renderer versions printed on the images.

Then acquire the same upstream run, run `sync-icon-eu`, and use `render-point`. The numerical pipeline is deterministic for identical inputs and configuration; file-generation timestamps and upstream availability are operational metadata.

## Validation protocol

- Unit tests cover parsing, units, grid selection, optical-depth kernels, turbulence integration, cache identity, and retention.
- Ephemeris regressions use public examples for Saint Petersburg and Moscow; the Moscow Sun/Moon reference is from the USNO one-day service.
- Synthetic fixtures test rendering without entering the user-serving path.
- Production smoke tests verify a complete manifest and all seven PNG products.
- Observational validation is still required before claiming measured or site-calibrated accuracy.

Future calibration datasets should include timestamps, instrument or observing method, uncertainty, site altitude, and weather context. Training and validation sites must be separated to avoid site-specific overfitting.

## Uncertainty and responsible interpretation

- ICON-EU is a forecast model with finite grid spacing, not a point measurement.
- Sub-grid cloud, terrain, local heat sources, aerosol, dome seeing, and telescope setup can dominate actual conditions.
- Planetary rise/set calculations are planning approximations; demanding work should use a high-precision ephemeris.
- Bortle output is an approximate reference derived from modeled zenith brightness, not a visual classification performed at the site.
- The `1…10` indices rank modeled conditions; they are not the Pickering scale and must not be reported as observed seeing.

Public examples contain only Saint Petersburg and Moscow. User identifiers, saved locations, tokens, and production credentials are runtime data and must never enter research artifacts or issue reports.
