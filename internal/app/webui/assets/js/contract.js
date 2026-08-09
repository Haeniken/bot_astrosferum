import { FRAME_COUNT, nodeDefinitions, profileFor } from "./profiles.js";

export const DATASET_SCHEMA_VERSION = 1;

const NODE_STATES = new Set(["valid", "precipitation_veto", "terrain_blocked", "unavailable"]);
const CURRENT_DATA_QUALITIES = new Set(["unavailable", "limited", "usable", "good"]);
const TWILIGHT_BANDS = new Set(["day", "light_twilight", "astronomical_twilight", "astronomical_night"]);
const PENALTY_KEYS = Object.freeze([
  "optical_turbulence",
  "cloud_obstruction",
  "surface_wind",
  "fog",
  "precipitation",
]);
const ISO_INSTANT = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;
const SHA256 = /^sha256:[0-9a-f]{64}$/;
const RUN_ID = /^\d{10}$/;
const VERSION = /^[a-z0-9][a-z0-9._+-]{0,127}$/i;
const INPUT_CONTRACT_VERSION = "astrodome-icon-primitives-v2";
const STRAIGHT_RAY_GEOMETRY_VERSION = "astrodome-icon-sphere-straight-ray-v2";
const REFRACTED_RAY_GEOMETRY_VERSION = "astrodome-icon-sphere-refraction-full-ciddor-dopri54-v3";
const REFRACTION_INTEGRATOR_VERSION = "dormand-prince-5-4-event-v3";
const REFRACTIVITY_VERSION = "ciddor-1996-phase-index-v1";
const CURRENT_SCIENCE_VERSION = "astrodome-science-kernel-v29";
const CURRENT_SCIENCE_PATH_VERSION = "astrodome-science-path-v23";
const DIRECTION_COORDINATE = "apparent_at_aperture";
const DIRECTION_REFERENCE_SURFACE = "icon_sphere_hsurf_plus_2m";
const DIRECTION_REFERENCE_WAVELENGTH_M = 500e-9;

export class ContractError extends Error {
  constructor(path, message) {
    super(`${path}: ${message}`);
    this.name = "ContractError";
    this.path = path;
  }
}

export function validateVisualizations(value) {
  const response = record(value, "$");
  const items = array(response.visualizations, "$.visualizations");
  return items.map((itemValue, index) => {
    const path = `$.visualizations[${index}]`;
    const item = record(itemValue, path);
    string(item.id, `${path}.id`, /^viz_[0-9a-f]{32}$/);
    if (typeof item.name !== "string" || item.name.length > 64) {
      fail(`${path}.name`, "expected a string no longer than 64 characters");
    }
    finite(item.latitude, `${path}.latitude`, -90, 90);
    finite(item.longitude, `${path}.longitude`, -180, 180);
    if (string(item.provider, `${path}.provider`) !== "icon-eu") {
      fail(`${path}.provider`, "only icon-eu Astrodome datasets are supported");
    }
    string(item.run_id, `${path}.run_id`, RUN_ID);
    string(item.grid_profile, `${path}.grid_profile`, /^(?:dense-v1|sparse-storage-v1|production-v2)$/);
    string(item.grid_geometry_digest, `${path}.grid_geometry_digest`, SHA256);
    finite(item.dataset_bytes, `${path}.dataset_bytes`, 1, 128 * 1024 * 1024);
    const generated = instant(item.generated_at, `${path}.generated_at`);
    const expires = instant(item.expires_at, `${path}.expires_at`);
    const adminFixture = boolean(item.admin_fixture, `${path}.admin_fixture`);
    if (!adminFixture && (expires <= generated || expires - generated > 96 * 60 * 60 * 1000)) {
      fail(`${path}.expires_at`, "must be within 96 hours after generation");
    }
    return Object.freeze({ ...item });
  });
}

function fail(path, message) {
  throw new ContractError(path, message);
}

function record(value, path) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    fail(path, "expected an object");
  }
  return value;
}

function array(value, path, length = null) {
  if (!Array.isArray(value)) {
    fail(path, "expected an array");
  }
  if (length !== null && value.length !== length) {
    fail(path, `expected ${length} entries, received ${value.length}`);
  }
  return value;
}

function string(value, path, pattern = null) {
  if (typeof value !== "string" || value.length === 0) {
    fail(path, "expected a non-empty string");
  }
  if (pattern && !pattern.test(value)) {
    fail(path, "has an invalid format");
  }
  return value;
}

function finite(value, path, minimum = -Infinity, maximum = Infinity) {
  if (typeof value !== "number" || !Number.isFinite(value) || value < minimum || value > maximum) {
    fail(path, `expected a finite number in [${minimum}, ${maximum}]`);
  }
  return value;
}

function nullableFinite(value, path, minimum = -Infinity, maximum = Infinity) {
  if (value === null) {
    return null;
  }
  return finite(value, path, minimum, maximum);
}

function boolean(value, path) {
  if (typeof value !== "boolean") {
    fail(path, "expected a boolean");
  }
  return value;
}

function enumeration(value, path, allowed) {
  string(value, path);
  if (!allowed.has(value)) {
    fail(path, `unsupported value ${JSON.stringify(value)}`);
  }
  return value;
}

function instant(value, path) {
  string(value, path, ISO_INSTANT);
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds)) {
    fail(path, "expected a valid RFC 3339 instant");
  }
  return milliseconds;
}

function closeTo(actual, expected, tolerance, path) {
  if (Math.abs(actual - expected) > tolerance) {
    fail(path, `expected ${expected}, received ${actual}`);
  }
}

function validateLocation(value, path, requireTimeZone) {
  const location = record(value, path);
  finite(location.latitude, `${path}.latitude`, -90, 90);
  finite(location.longitude, `${path}.longitude`, -180, 180);
  if (requireTimeZone) {
    string(location.time_zone, `${path}.time_zone`, /^[A-Za-z_+-]+(?:\/[A-Za-z0-9_+.-]+)+$|^UTC$/);
  }
  if (Object.hasOwn(location, "surface_elevation_m")) {
    finite(location.surface_elevation_m, `${path}.surface_elevation_m`, -500, 9000);
  }
}

function validateGrid(gridValue, profile, frameCount) {
  const grid = record(gridValue, "$.grid");
  if (grid.frame_count !== frameCount) {
    fail("$.grid.frame_count", `must equal ${frameCount}`);
  }
  if (grid.node_count !== profile.nodeCount) {
    fail("$.grid.node_count", `must equal ${profile.nodeCount}`);
  }
  const rings = array(grid.rings, "$.grid.rings", profile.rings.length);
  for (const [index, expected] of profile.rings.entries()) {
    const ring = record(rings[index], `$.grid.rings[${index}]`);
    finite(ring.elevation_deg, `$.grid.rings[${index}].elevation_deg`, 10, 89.999);
    closeTo(ring.elevation_deg, expected.elevationDeg, 1e-6, `$.grid.rings[${index}].elevation_deg`);
    if (ring.azimuth_count !== expected.azimuthCount) {
      fail(`$.grid.rings[${index}].azimuth_count`, `must equal ${expected.azimuthCount}`);
    }
    finite(ring.azimuth_step_deg, `$.grid.rings[${index}].azimuth_step_deg`, 0, 360);
    closeTo(ring.azimuth_step_deg, expected.azimuthStepDeg, 1e-9, `$.grid.rings[${index}].azimuth_step_deg`);
  }
  const zenith = record(grid.zenith, "$.grid.zenith");
  closeTo(finite(zenith.elevation_deg, "$.grid.zenith.elevation_deg"), 90, 1e-12, "$.grid.zenith.elevation_deg");
  if (zenith.azimuth !== null) {
    fail("$.grid.zenith.azimuth", "zenith azimuth must be null");
  }
}

function validateQuality(value, path, category, currentScience) {
  const quality = record(value, path);
  finite(quality.lead_quality, `${path}.lead_quality`, 0, 1);
  finite(quality.geometry_coverage, `${path}.geometry_coverage`, 0, 1);
  finite(quality.turbulence_path_coverage, `${path}.turbulence_path_coverage`, 0, 1);
  finite(quality.cloud_path_coverage, `${path}.cloud_path_coverage`, 0, 1);
  nullableFinite(quality.humidity_path_coverage, `${path}.humidity_path_coverage`, 0, 1);
  finite(quality.temporal_resolution_hours, `${path}.temporal_resolution_hours`, Number.MIN_VALUE, 24);
  finite(quality.quadrature_convergence, `${path}.quadrature_convergence`, 0, 1);
  if (currentScience && quality.approximation_length_m === undefined) {
    fail(`${path}.approximation_length_m`, "is required by the current science contract");
  }
  const approximationLength = quality.approximation_length_m === undefined
    ? 0
    : finite(quality.approximation_length_m, `${path}.approximation_length_m`, 0, 1);
  finite(quality.top_closure, `${path}.top_closure`, 0, 1);
  boolean(quality.mandatory_complete, `${path}.mandatory_complete`);
  const reasons = array(quality.reason_codes, `${path}.reason_codes`);
  for (const [index, reason] of reasons.entries()) {
    string(reason, `${path}.reason_codes[${index}]`, VERSION);
  }
  if (category === "unavailable" && quality.mandatory_complete) {
    fail(`${path}.mandatory_complete`, "cannot be true for unavailable data");
  }
  if (category !== "unavailable" && !quality.mandatory_complete) {
    fail(`${path}.mandatory_complete`, "must be true for a published quality category");
  }
  const hasApproximationReason = reasons.includes("short_path_approximation");
  if (approximationLength > 0) {
    if (!hasApproximationReason) {
      fail(`${path}.reason_codes`, "must include short_path_approximation for a non-zero approximated path");
    }
    if (quality.mandatory_complete && (category !== "limited" || quality.quadrature_convergence !== 0)) {
      fail(path, "an available approximated result must be limited with zero quadrature convergence");
    }
  } else if (hasApproximationReason) {
    fail(`${path}.reason_codes`, "cannot include short_path_approximation for a zero approximated path");
  }
}

function validateNumericalError(value, path) {
  const numerical = record(value, path);
  nullableFinite(numerical.overall_absolute, `${path}.overall_absolute`, 0, 10);
  const turbulence = nullableFinite(numerical.turbulence_integral_relative, `${path}.turbulence_integral_relative`, 0, 1);
  const integrated = nullableFinite(numerical.integrated_cn2_relative, `${path}.integrated_cn2_relative`, 0, 1);
  const windWeighted = nullableFinite(numerical.wind_weighted_cn2_relative, `${path}.wind_weighted_cn2_relative`, 0, 1);
  nullableFinite(numerical.cloud_transmission_absolute, `${path}.cloud_transmission_absolute`, 0, 1);
  const components = [integrated, windWeighted].filter((component) => component !== null);
  if (components.length === 0 ? turbulence !== null : turbulence === null || Math.abs(turbulence - Math.max(...components)) > 1e-12) {
    fail(`${path}.turbulence_integral_relative`, "must equal the maximum defined component relative numerical error");
  }
  return numerical;
}

function validateNodeGeometry(node, path, rayGeometryVersion) {
  const mode = enumeration(node.geometry_mode, `${path}.geometry_mode`, new Set(["straight-compat", "refraction-full"]));
  if (mode === "straight-compat") {
    if (rayGeometryVersion !== STRAIGHT_RAY_GEOMETRY_VERSION || node.direction_at_model_top_ecef !== null) {
      fail(`${path}.geometry_mode`, "straight geometry must not claim a refracted model-top tangent");
    }
    return;
  }
  if (rayGeometryVersion !== REFRACTED_RAY_GEOMETRY_VERSION) {
    fail(`${path}.geometry_mode`, "full-refraction node does not match the dataset ray geometry");
  }
  if (node.direction_at_model_top_ecef === null) {
    if (node.state !== "unavailable" && node.state !== "terrain_blocked") {
      fail(`${path}.direction_at_model_top_ecef`, "completed refraction ray requires its model-top tangent");
    }
    return;
  }
  const direction = record(node.direction_at_model_top_ecef, `${path}.direction_at_model_top_ecef`);
  const x = finite(direction.x, `${path}.direction_at_model_top_ecef.x`);
  const y = finite(direction.y, `${path}.direction_at_model_top_ecef.y`);
  const z = finite(direction.z, `${path}.direction_at_model_top_ecef.z`);
  closeTo(Math.hypot(x, y, z), 1, 1e-8, `${path}.direction_at_model_top_ecef`);
}

function validateFactors(value, path) {
  const factors = record(value, path);
  let product = 1;
  for (const key of PENALTY_KEYS) {
    const factor = finite(factors[key], `${path}.${key}`, 0, 1);
    product *= factor;
  }
  return product;
}

function validatePenalties(value, path) {
  const contributions = array(value, path, PENALTY_KEYS.length);
  const seen = new Set();
  let total = 0;
  for (const [index, contributionValue] of contributions.entries()) {
    const contribution = record(contributionValue, `${path}[${index}]`);
    const key = string(contribution.key, `${path}[${index}].key`);
    if (!PENALTY_KEYS.includes(key) || seen.has(key)) {
      fail(`${path}[${index}].key`, "must be a unique canonical penalty key");
    }
    seen.add(key);
    total += finite(contribution.loss_fraction, `${path}[${index}].loss_fraction`, 0, 1);
  }
  return total;
}

function validateUnavailableNode(node, path) {
  boolean(node.tau0_unbounded_above, `${path}.tau0_unbounded_above`);
  for (const key of [
    "overall",
    "seeing_arcsec_500nm",
    "tau0_ms_500nm",
    "tau0_conservative_ms_500nm",
    "J",
    "JV",
    "nominal_cloud_transmission",
    "conservative_cloud_transmission",
    "effective_cloud_transmission",
    "cloud_optical_depth_liquid",
    "cloud_optical_depth_ice",
    "slant_water_vapour_kg_m2",
    "penalty_loss_fraction",
  ]) {
    if (node[key] !== null) {
      fail(`${path}.${key}`, "must be null when the directional result is unavailable");
    }
  }
  if (node.factors !== null) {
    fail(`${path}.factors`, "must be null when the directional result is unavailable");
  }
  for (const key of [
    "overall_absolute",
    "turbulence_integral_relative",
    "integrated_cn2_relative",
    "wind_weighted_cn2_relative",
    "cloud_transmission_absolute",
  ]) {
    if (node.numerical_error[key] !== null) {
      fail(`${path}.numerical_error.${key}`, "must be null when the directional result is unavailable");
    }
  }
  array(node.penalty_contributions, `${path}.penalty_contributions`, 0);
}

function validateAvailableNode(node, path) {
  const overall = finite(node.overall, `${path}.overall`, 1, 10);
  finite(node.seeing_arcsec_500nm, `${path}.seeing_arcsec_500nm`, 0, 60);
  nullableFinite(node.tau0_ms_500nm, `${path}.tau0_ms_500nm`, 0, 10000);
  nullableFinite(node.tau0_conservative_ms_500nm, `${path}.tau0_conservative_ms_500nm`, 0, 10000);
  boolean(node.tau0_unbounded_above, `${path}.tau0_unbounded_above`);
  finite(node.J, `${path}.J`, 0, Number.MAX_VALUE);
  finite(node.JV, `${path}.JV`, 0, Number.MAX_VALUE);
  const nominalCloud = finite(node.nominal_cloud_transmission, `${path}.nominal_cloud_transmission`, 0, 1);
  const conservativeCloud = finite(node.conservative_cloud_transmission, `${path}.conservative_cloud_transmission`, 0, 1);
  const effectiveCloud = finite(node.effective_cloud_transmission, `${path}.effective_cloud_transmission`, 0, 1);
  if (conservativeCloud > nominalCloud + 1e-15) {
    fail(`${path}.conservative_cloud_transmission`, "must not exceed nominal cloud transmission");
  }
  closeTo(effectiveCloud, conservativeCloud, 1e-15, `${path}.effective_cloud_transmission`);
  finite(node.cloud_optical_depth_liquid, `${path}.cloud_optical_depth_liquid`, 0, Number.MAX_VALUE);
  finite(node.cloud_optical_depth_ice, `${path}.cloud_optical_depth_ice`, 0, Number.MAX_VALUE);
  nullableFinite(node.slant_water_vapour_kg_m2, `${path}.slant_water_vapour_kg_m2`, 0, 1000);
  finite(node.numerical_error.overall_absolute, `${path}.numerical_error.overall_absolute`, 0, 10);
  finite(node.numerical_error.cloud_transmission_absolute, `${path}.numerical_error.cloud_transmission_absolute`, 0, 1);
  const factorProduct = validateFactors(node.factors, `${path}.factors`);
  const contributionTotal = validatePenalties(node.penalty_contributions, `${path}.penalty_contributions`);
  const loss = finite(node.penalty_loss_fraction, `${path}.penalty_loss_fraction`, 0, 1);
  closeTo(contributionTotal, loss, 2e-6, `${path}.penalty_contributions`);
  closeTo(factorProduct, 1 - loss, 2e-6, `${path}.factors`);
  closeTo(overall, 1 + 9 * (1 - loss), 2e-5, `${path}.overall`);
  if (node.state === "precipitation_veto") {
    closeTo(node.factors.precipitation, 0, 1e-12, `${path}.factors.precipitation`);
    closeTo(overall, 1, 2e-5, `${path}.overall`);
  } else if (node.factors.precipitation === 0) {
    fail(`${path}.state`, "must report precipitation_veto when the precipitation factor is zero");
  }
}

function validateNode(nodeValue, expected, path, rayGeometryVersion, currentScience) {
  const node = record(nodeValue, path);
  if (expected.zenith) {
    if (node.azimuth_deg !== null) {
      fail(`${path}.azimuth_deg`, "zenith azimuth must be null");
    }
  } else {
    closeTo(finite(node.azimuth_deg, `${path}.azimuth_deg`, 0, 359.999999999), expected.azimuthDeg, 1e-7, `${path}.azimuth_deg`);
  }
  closeTo(finite(node.elevation_deg, `${path}.elevation_deg`, 10, 90), expected.elevationDeg, 1e-6, `${path}.elevation_deg`);
  const state = enumeration(node.state, `${path}.state`, NODE_STATES);
  const quality = enumeration(
    node.data_quality,
    `${path}.data_quality`,
    CURRENT_DATA_QUALITIES,
  );
  string(node.limiting_factor, `${path}.limiting_factor`, VERSION);
  validateQuality(node.quality_components, `${path}.quality_components`, quality, currentScience);
  validateNumericalError(node.numerical_error, `${path}.numerical_error`);
  validateNodeGeometry(node, path, rayGeometryVersion);
  if (state === "unavailable" || state === "terrain_blocked") {
    validateUnavailableNode(node, path);
  } else {
    validateAvailableNode(node, path);
  }
}

function expectedTwilightBand(solarAltitude) {
  if (solarAltitude >= 0) {
    return "day";
  }
  if (solarAltitude >= -12) {
    return "light_twilight";
  }
  if (solarAltitude >= -18) {
    return "astronomical_twilight";
  }
  return "astronomical_night";
}

function validateFrame(frameValue, index, validTime, expectedNodes, rayGeometryVersion, currentScience) {
  const path = `$.frames[${index}]`;
  const frame = record(frameValue, path);
  const frameTime = instant(frame.valid_at, `${path}.valid_at`);
  if (frameTime !== validTime) {
    fail(`${path}.valid_at`, "must equal the corresponding valid_times entry");
  }
  record(frame.surface_common, `${path}.surface_common`);
  const solarAltitude = finite(frame.solar_altitude_deg, `${path}.solar_altitude_deg`, -90, 90);
  const band = enumeration(frame.twilight_band, `${path}.twilight_band`, TWILIGHT_BANDS);
  const expectedBand = expectedTwilightBand(solarAltitude);
  if (band !== expectedBand) {
    fail(`${path}.twilight_band`, `must be ${expectedBand} for solar altitude ${solarAltitude}`);
  }
  const nodes = array(frame.nodes, `${path}.nodes`, expectedNodes.length);
  for (const [nodeIndex, expected] of expectedNodes.entries()) {
    validateNode(nodes[nodeIndex], expected, `${path}.nodes[${nodeIndex}]`, rayGeometryVersion, currentScience);
  }
}

export function validateDataset(value) {
  return validateDatasetContract(value);
}

// A saved immutable visualization must use the same exact current scientific
// contract as a new result. Older contracts are rejected, never reinterpreted.
export function validateArchivedDataset(value) {
  return validateDatasetContract(value);
}

function validateDatasetContract(value) {
  const dataset = record(value, "$");
  if (dataset.schema_version !== DATASET_SCHEMA_VERSION) {
    fail("$.schema_version", `must equal ${DATASET_SCHEMA_VERSION}`);
  }
  if (dataset.provider !== "icon-eu") {
    fail("$.provider", "Astrodome is available only for icon-eu");
  }
  string(dataset.model_product, "$.model_product");
  string(dataset.model_grid, "$.model_grid");
  string(dataset.run_id, "$.run_id", RUN_ID);
  string(dataset.run_manifest_digest, "$.run_manifest_digest", SHA256);
  instant(dataset.run_base_time, "$.run_base_time");
  instant(dataset.generated_at, "$.generated_at");
  validateLocation(dataset.requested_location, "$.requested_location", true);
  validateLocation(dataset.model_location, "$.model_location", false);
  for (const key of [
    "input_contract_version",
    "geometry_version",
    "refraction_version",
    "refractivity_version",
    "science_version",
    "calibration_version",
  ]) {
    string(dataset[key], `$.${key}`, VERSION);
  }
  if (dataset.input_contract_version !== INPUT_CONTRACT_VERSION) {
    fail("$.input_contract_version", `must equal ${INPUT_CONTRACT_VERSION}`);
  }
  const sciencePathVersion = dataset.science_path_version;
  if (sciencePathVersion !== undefined) {
    string(sciencePathVersion, "$.science_path_version", VERSION);
  }
  const currentScience = dataset.science_version === CURRENT_SCIENCE_VERSION
    && sciencePathVersion === CURRENT_SCIENCE_PATH_VERSION
    && dataset.calibration_version === CURRENT_SCIENCE_VERSION;
  if (!currentScience) {
    fail("$.science_version", "uses an unsupported or mixed scientific contract");
  }
  string(dataset.source_column_plan_digest, "$.source_column_plan_digest", SHA256);
  string(dataset.science_calibration_sha256, "$.science_calibration_sha256", SHA256);
  if (dataset.direction_coordinate !== DIRECTION_COORDINATE
    || dataset.direction_reference_surface !== DIRECTION_REFERENCE_SURFACE
    || dataset.direction_reference_wavelength_m !== DIRECTION_REFERENCE_WAVELENGTH_M
    || dataset.vacuum_direction_available !== false) {
    fail("$.direction_coordinate", "current datasets require the apparent-at-aperture direction contract");
  }
  const profile = profileFor(dataset.grid_profile);
  if (!profile) {
    fail("$.grid_profile", `unsupported profile ${JSON.stringify(dataset.grid_profile)}`);
  }
  if (dataset.geometry_version !== profile.geometryVersion) {
    fail("$.geometry_version", `must equal ${profile.geometryVersion}`);
  }
  if (dataset.ray_geometry_version === REFRACTED_RAY_GEOMETRY_VERSION) {
    if (dataset.refraction_version !== REFRACTION_INTEGRATOR_VERSION
      || dataset.refractivity_version !== REFRACTIVITY_VERSION) {
      fail("$.ray_geometry_version", "full-refraction version provenance is inconsistent");
    }
  } else if (dataset.ray_geometry_version === STRAIGHT_RAY_GEOMETRY_VERSION) {
    if (dataset.refraction_version !== "not-applicable" || dataset.refractivity_version !== "not-applicable") {
      fail("$.ray_geometry_version", "straight geometry must not claim refraction versions");
    }
  } else {
    fail("$.ray_geometry_version", "uses an unsupported ray geometry");
  }
  if (dataset.grid_geometry_digest !== profile.digest) {
    fail("$.grid_geometry_digest", `does not match the embedded ${profile.name} geometry`);
  }
  const validTimeValues = array(dataset.valid_times, "$.valid_times");
  if (validTimeValues.length < 1 || validTimeValues.length > FRAME_COUNT) {
    fail("$.valid_times", `must contain 1..${FRAME_COUNT} entries`);
  }
  validateGrid(dataset.grid, profile, validTimeValues.length);

  const validTimes = validTimeValues.map((valueAt, index) => (
    instant(valueAt, `$.valid_times[${index}]`)
  ));
  for (let index = 1; index < validTimes.length; index += 1) {
    if (validTimes[index] - validTimes[index - 1] !== 60 * 60 * 1000) {
      fail(`$.valid_times[${index}]`, "timestamps must be strictly hourly");
    }
  }

  const frames = array(dataset.frames, "$.frames", validTimes.length);
  const expectedNodes = nodeDefinitions(profile);
  for (let index = 0; index < validTimes.length; index += 1) {
    validateFrame(
      frames[index],
      index,
      validTimes[index],
      expectedNodes,
      dataset.ray_geometry_version,
      currentScience,
    );
  }
  return Object.freeze({ dataset, profile, nodes: expectedNodes, validTimes: Object.freeze(validTimes) });
}

export function validateProfile(value) {
  const profile = record(value, "$");
  if (profile.authenticated === false) {
    return Object.freeze({ authenticated: false, language: profile.language === "ru" ? "ru" : "en" });
  }
  const language = profile.language === "ru" ? "ru" : "en";
  const telegramUserID = finite(profile.telegram_user_id, "$.telegram_user_id", 1, Number.MAX_SAFE_INTEGER);
  if (!Number.isSafeInteger(telegramUserID)) {
    fail("$.telegram_user_id", "must be a safe positive integer");
  }
  const csrfToken = string(profile.csrf_token, "$.csrf_token", /^[A-Za-z0-9_-]{32,512}$/);
  return Object.freeze({
    authenticated: true,
    language,
    displayName: `Telegram #${telegramUserID}`,
    telegramUserID,
    csrfToken,
  });
}

export function validatePoints(value) {
  const envelope = record(value, "$");
  const points = array(envelope.points, "$.points");
  if (points.length > 10) {
    fail("$.points", "at most 10 saved points are allowed");
  }
  return Object.freeze(points.map((pointValue, index) => {
    const path = `$.points[${index}]`;
    const point = record(pointValue, path);
    if (!Number.isSafeInteger(point.id) || point.id <= 0) {
      fail(`${path}.id`, "must be a safe positive integer");
    }
    return Object.freeze({
      id: point.id,
      name: string(point.name, `${path}.name`),
      latitude: finite(point.latitude, `${path}.latitude`, -90, 90),
      longitude: finite(point.longitude, `${path}.longitude`, -180, 180),
    });
  }));
}

export function validateAvailability(value) {
  const availability = record(value, "$");
  const provider = typeof availability.provider === "string" && availability.provider !== ""
    ? availability.provider : null;
  const runID = typeof availability.run_id === "string" && availability.run_id !== ""
    ? availability.run_id : null;
  const freshnessSeconds = Number.isFinite(availability.freshness_seconds)
    ? availability.freshness_seconds : null;
  const gridProfile = typeof availability.grid_profile === "string" && availability.grid_profile !== ""
    ? availability.grid_profile : null;
  const reason = typeof availability.reason === "string" && availability.reason !== ""
    ? availability.reason : null;
  const result = {
    enabled: boolean(availability.enabled, "$.enabled"),
    available: boolean(availability.available, "$.available"),
    workerAvailable: boolean(availability.worker_available, "$.worker_available"),
    running: boolean(availability.running, "$.running"),
    queueDepth: finite(availability.queue_length, "$.queue_length", 0, 100000),
    provider,
    runID,
    freshnessSeconds,
    gridProfile,
    stale: Object.hasOwn(availability, "stale")
      ? boolean(availability.stale, "$.stale")
      : reason === "stale" || reason === "stale_run",
    reason,
  };
  if (!Number.isInteger(result.queueDepth)) {
    fail("$.queue_depth", "must be an integer");
  }
  if (provider !== null && provider !== "icon-eu") {
    fail("$.provider", "must be icon-eu or null");
  }
  if (runID !== null) {
    string(runID, "$.run_id", RUN_ID);
  }
  if (freshnessSeconds !== null) {
    finite(freshnessSeconds, "$.freshness_seconds", 0, 31 * 24 * 3600);
  }
  if (gridProfile !== null && !profileFor(gridProfile)) {
    fail("$.grid_profile", "is not supported by this client");
  }
  if (Object.hasOwn(availability, "run_base_time") && availability.run_base_time !== "0001-01-01T00:00:00Z") {
    instant(availability.run_base_time, "$.run_base_time");
  }
  if (result.available) {
    if (!result.enabled || provider !== "icon-eu" || runID === null || gridProfile === null) {
      fail("$", "an available result must identify its ICON-EU run and grid");
    }
  }
  return Object.freeze(result);
}

export function validateJob(value) {
  const job = record(value, "$");
  const state = enumeration(job.state, "$.state", new Set([
    "queued", "running", "ready", "failed", "cancelled",
  ]));
  const id = string(job.id, "$.id", /^[A-Za-z0-9_-]{20,128}$/);
  const runID = typeof job.run_id === "string" && job.run_id !== "" ? job.run_id : null;
  if (runID !== null) {
    string(runID, "$.run_id", RUN_ID);
  }
  const profile = typeof job.grid_profile === "string" && job.grid_profile !== ""
    ? profileFor(job.grid_profile) : null;
  if (job.grid_profile && !profile) {
    fail("$.grid_profile", "does not name a supported profile");
  }
  const geometryDigest = typeof job.grid_geometry_digest === "string" && job.grid_geometry_digest !== ""
    ? job.grid_geometry_digest : null;
  if (geometryDigest !== null) {
    string(geometryDigest, "$.grid_geometry_digest", SHA256);
    if (!profile || geometryDigest !== profile.digest) {
      fail("$.grid_geometry_digest", "does not match the advertised profile");
    }
  }
  let queuePosition = null;
  if (job.queue_position !== null) {
    queuePosition = finite(job.queue_position, "$.queue_position", 1, 100000);
    if (!Number.isInteger(queuePosition)) {
      fail("$.queue_position", "must be an integer");
    }
  }
  instant(job.created_at, "$.created_at");
  instant(job.updated_at, "$.updated_at");
  if (state === "ready") {
    if (runID === null || profile === null || geometryDigest === null) {
      fail("$", "a ready job must carry run and exact grid identity");
    }
  }
  return Object.freeze({
    id,
    runID,
    state,
    queuePosition,
    gridProfile: profile?.name ?? null,
    gridGeometryDigest: geometryDigest,
    failureCode: typeof job.failure_code === "string" ? job.failure_code : null,
  });
}

export const penaltyKeys = PENALTY_KEYS;
