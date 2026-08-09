const OVERALL = Object.freeze([
  [17, 18, 33],
  [43, 24, 74],
  [72, 31, 104],
  [105, 38, 123],
  [145, 48, 126],
  [184, 61, 119],
  [220, 83, 104],
  [244, 120, 93],
  [251, 166, 108],
  [252, 222, 155],
]);

const SEQUENTIAL = Object.freeze([
  [17, 28, 54],
  [26, 55, 91],
  [31, 88, 122],
  [36, 121, 145],
  [48, 151, 156],
  [76, 179, 154],
  [119, 201, 141],
  [169, 219, 124],
  [215, 232, 132],
  [250, 240, 168],
]);

const CATEGORICAL = Object.freeze({
  none: [79, 192, 162],
  optical_turbulence: [167, 113, 224],
  cloud_obstruction: [72, 144, 220],
  surface_wind: [242, 176, 76],
  fog: [126, 169, 181],
  precipitation: [229, 83, 83],
  coarse_terrain: [151, 112, 77],
  unavailable_data: [103, 111, 130],
});

const UNAVAILABLE = [70, 76, 91];
const TERRAIN = [92, 65, 52];

export const LAYERS = Object.freeze([
  "overall",
  "cloud",
  "seeing",
  "tau0",
  "water_vapour",
  "limiting_factor",
  "data_quality",
]);

export function colorForNode(node, layer) {
  if (node.state === "unavailable") {
    return normalized(UNAVAILABLE);
  }
  if (node.state === "terrain_blocked") {
    return normalized(TERRAIN);
  }
  switch (layer) {
  case "overall":
    return normalized(OVERALL[bin(node.overall, 1, 10)]);
  case "cloud":
    return normalized(SEQUENTIAL[9 - bin(1 - node.effective_cloud_transmission, 0, 1)]);
  case "seeing":
    return normalized(SEQUENTIAL[9 - bin(node.seeing_arcsec_500nm, 0.4, 4)]);
  case "tau0": {
    const value = node.tau0_ms_500nm ?? node.tau0_conservative_ms_500nm;
    return value === null ? normalized(UNAVAILABLE) : normalized(SEQUENTIAL[bin(value, 0.5, 10)]);
  }
  case "water_vapour":
    return node.slant_water_vapour_kg_m2 === null
      ? normalized(UNAVAILABLE)
      : normalized(SEQUENTIAL[9 - bin(node.slant_water_vapour_kg_m2, 0, 80)]);
  case "limiting_factor":
    return normalized(CATEGORICAL[node.limiting_factor] ?? CATEGORICAL.unavailable_data);
  case "data_quality":
    return normalized({
      unavailable: UNAVAILABLE,
      limited: [225, 144, 66],
      usable: [61, 161, 202],
      good: [72, 190, 133],
    }[node.data_quality] ?? UNAVAILABLE);
  default:
    return normalized(UNAVAILABLE);
  }
}

export function qualityCode(node) {
  if (node.state === "unavailable") {
    return 0;
  }
  if (node.state === "terrain_blocked") {
    return 1;
  }
  return {
    unavailable: 0,
    limited: 2,
    usable: 3,
    good: 4,
  }[node.data_quality] ?? 0;
}

export function legendForLayer(layer) {
  switch (layer) {
  case "overall":
    return OVERALL.map((color, index) => ({ value: String(index + 1), color: cssColor(color) }));
  case "cloud":
    return [0, 25, 50, 75, 100].map((value) => ({
      value: `${value}%`, color: cssColor(SEQUENTIAL[9 - bin(value / 100, 0, 1)]),
    }));
  case "seeing":
    return [0.5, 1, 2, 3, 4].map((value) => ({
      value: `${value}\u2033`, color: cssColor(SEQUENTIAL[9 - bin(value, 0.4, 4)]),
    }));
  case "tau0":
    return [0.5, 2, 4, 7, 10].map((value) => ({
      value: `${value} ms`, color: cssColor(SEQUENTIAL[bin(value, 0.5, 10)]),
    }));
  case "water_vapour":
    return [0, 10, 25, 50, 80].map((value) => ({
      value: `${value}`, color: cssColor(SEQUENTIAL[9 - bin(value, 0, 80)]),
    }));
  case "limiting_factor":
    return Object.entries(CATEGORICAL).map(([value, color]) => ({ value, color: cssColor(color) }));
  case "data_quality":
    return [
      { value: "unavailable", color: cssColor(UNAVAILABLE) },
      { value: "limited", color: cssColor([225, 144, 66]) },
      { value: "usable", color: cssColor([61, 161, 202]) },
      { value: "good", color: cssColor([72, 190, 133]) },
    ];
  default:
    return [];
  }
}

export function penaltyColor(key) {
  return cssColor(CATEGORICAL[key] ?? CATEGORICAL.unavailable_data);
}

function bin(value, minimum, maximum) {
  const fraction = Math.max(0, Math.min(1, (value - minimum) / (maximum - minimum)));
  return Math.min(9, Math.floor(fraction * 10));
}

function normalized(color) {
  return Object.freeze([color[0] / 255, color[1] / 255, color[2] / 255]);
}

function cssColor(color) {
  return `rgb(${color[0]} ${color[1]} ${color[2]})`;
}
