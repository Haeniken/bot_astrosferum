const DENSE_RINGS = [
  [10, 60],
  [11.343, 52],
  [13.035, 44],
  [15.236, 40],
  [18.22, 32],
  [20, 32],
  [22.481, 28],
  [30, 20],
  [40, 16],
  [50, 12],
  [60, 8],
  [70, 4],
  [80, 4],
];

const SPARSE_RINGS = [
  [10, 16],
  [20, 16],
  [30, 16],
  [45, 16],
  [60, 16],
  [75, 16],
];

const PRODUCTION_V2_RINGS = [
  [10, 16],
  [20, 16],
  [30, 16],
  [40, 16],
  [50, 16],
  [60, 16],
  [70, 16],
  [80, 16],
];

const DENSE_DESCRIPTOR = `astrosferum-grid-geometry-v1
profile=dense-v1
coordinate=ENU
azimuth=degrees-clockwise-from-north
outer-boundary-elevation=10
tessellation=spherical-voronoi-v1
outer-boundary-segments=360
rings=10/60/6;11.343/52/6.923076923076923;13.035/44/8.181818181818182;15.236/40/9;18.22/32/11.25;20/32/11.25;22.481/28/12.857142857142858;30/20/18;40/16/22.5;50/12/30;60/8/45;70/4/90;80/4/90
zenith=90/null
node-order=rings-ascending-elevation/azimuth-ascending/zenith-last`;

const SPARSE_DESCRIPTOR = `astrosferum-grid-geometry-v1
profile=sparse-storage-v1
coordinate=ENU
azimuth=degrees-clockwise-from-north
outer-boundary-elevation=10
tessellation=spherical-voronoi-v1
outer-boundary-segments=360
rings=10/16/22.5;20/16/22.5;30/16/22.5;45/16/22.5;60/16/22.5;75/16/22.5
zenith=90/null
node-order=rings-ascending-elevation/azimuth-ascending/zenith-last`;

const PRODUCTION_V2_DESCRIPTOR = `astrosferum-grid-geometry-v1
profile=production-v2
coordinate=ENU
azimuth=degrees-clockwise-from-north
outer-boundary-elevation=10
tessellation=spherical-voronoi-v1
outer-boundary-segments=360
rings=10/16/22.5;20/16/22.5;30/16/22.5;40/16/22.5;50/16/22.5;60/16/22.5;70/16/22.5;80/16/22.5
zenith=90/null
node-order=rings-ascending-elevation/azimuth-ascending/zenith-last`;

function buildProfile(name, digest, canonicalDescriptor, ringPairs) {
  const rings = ringPairs.map(([elevationDeg, azimuthCount]) => Object.freeze({
    elevationDeg,
    azimuthCount,
    azimuthStepDeg: 360 / azimuthCount,
  }));
  const nodeCount = rings.reduce((sum, ring) => sum + ring.azimuthCount, 1);
  return Object.freeze({
    name,
    digest,
    canonicalDescriptor,
    geometryVersion: "astrosferum-grid-geometry-v1",
    outerBoundaryDeg: 10,
    outerBoundarySegments: 360,
    tessellation: "spherical-voronoi-v1",
    nodeCount,
    rings: Object.freeze(rings),
  });
}

export const PROFILE_DEFINITIONS = Object.freeze({
  "dense-v1": buildProfile(
    "dense-v1",
    "sha256:e034cc4b4933ea503cd4c01c814885c8a21b2a5dd03706471885226fbdabe2bc",
    DENSE_DESCRIPTOR,
    DENSE_RINGS,
  ),
  "sparse-storage-v1": buildProfile(
    "sparse-storage-v1",
    "sha256:47f2412d8927ca7cf7691ad749cb20b4b03bc08da77a5353f0bb9459d0aa21ff",
    SPARSE_DESCRIPTOR,
    SPARSE_RINGS,
  ),
  "production-v2": buildProfile(
    "production-v2",
    "sha256:600524c0a0ea5c21f7aaa879d3967ef007b8d5804b2217f7345703010b62af3e",
    PRODUCTION_V2_DESCRIPTOR,
    PRODUCTION_V2_RINGS,
  ),
});

export function profileFor(name) {
  return Object.hasOwn(PROFILE_DEFINITIONS, name) ? PROFILE_DEFINITIONS[name] : null;
}

export function nodeDefinitions(profile) {
  const nodes = [];
  for (const [ringIndex, ring] of profile.rings.entries()) {
    for (let azimuthIndex = 0; azimuthIndex < ring.azimuthCount; azimuthIndex += 1) {
      nodes.push(Object.freeze({
        index: nodes.length,
        ringIndex,
        azimuthIndex,
        azimuthDeg: azimuthIndex * ring.azimuthStepDeg,
        elevationDeg: ring.elevationDeg,
        zenith: false,
      }));
    }
  }
  nodes.push(Object.freeze({
    index: nodes.length,
    ringIndex: null,
    azimuthIndex: null,
    azimuthDeg: null,
    elevationDeg: 90,
    zenith: true,
  }));
  if (nodes.length !== profile.nodeCount) {
    throw new Error(`profile ${profile.name} produced ${nodes.length} nodes, expected ${profile.nodeCount}`);
  }
  return Object.freeze(nodes);
}

export const FRAME_COUNT = 72;
export const OUTER_BOUNDARY_DEG = 10;
