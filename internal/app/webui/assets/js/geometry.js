import { nodeDefinitions } from "./profiles.js";

const DEG = Math.PI / 180;
const VORONOI_CANDIDATE_NEIGHBOURS = 48;

export function directionVector(azimuthDeg, elevationDeg) {
  const azimuth = (azimuthDeg ?? 0) * DEG;
  const elevation = elevationDeg * DEG;
  const horizontal = Math.cos(elevation);
  return Object.freeze([
    horizontal * Math.sin(azimuth),
    horizontal * Math.cos(azimuth),
    Math.sin(elevation),
  ]);
}

export function azimuthElevation(vector) {
  const normalized = normalize(vector);
  let azimuthDeg = Math.atan2(normalized[0], normalized[1]) / DEG;
  if (azimuthDeg < 0) {
    azimuthDeg += 360;
  }
  return Object.freeze({
    azimuthDeg,
    elevationDeg: Math.asin(clamp(normalized[2], -1, 1)) / DEG,
  });
}

export function buildDomeGeometry(profile) {
  const nodes = nodeDefinitions(profile);
  const vectors = nodes.map((node) => directionVector(node.azimuthDeg, node.elevationDeg));

  const cells = [];
  const trianglePositions = [];
  const triangleCellIDs = [];
  const borderPositions = [];
  for (let cellID = 0; cellID < vectors.length; cellID += 1) {
    const polygon = sphericalVoronoiCell(vectors, cellID, profile.outerBoundaryDeg);
    const center = normalize(polygon.reduce((sum, vertex) => add(sum, vertex), [0, 0, 0]));
    for (let index = 0; index < polygon.length; index += 1) {
      const next = (index + 1) % polygon.length;
      appendVertex(trianglePositions, center);
      appendVertex(trianglePositions, polygon[index]);
      appendVertex(trianglePositions, polygon[next]);
      triangleCellIDs.push(cellID, cellID, cellID);
      appendVertex(borderPositions, scale(polygon[index], 0.997));
      appendVertex(borderPositions, scale(polygon[next], 0.997));
    }
    cells.push(Object.freeze({ cellID, polygon: Object.freeze(polygon) }));
  }

  const gridPositions = buildReferenceGrid(profile.outerBoundaryDeg);
  const boundaryPositions = buildBoundary(profile.outerBoundaryDeg);
  const skirt = buildSkirt(profile.outerBoundaryDeg);
  return Object.freeze({
    profile,
    nodes,
    vectors: Object.freeze(vectors),
    cells: Object.freeze(cells),
    trianglePositions: new Float32Array(trianglePositions),
    triangleCellIDs: new Uint16Array(triangleCellIDs),
    borderPositions: new Float32Array(borderPositions),
    gridPositions: new Float32Array(gridPositions),
    boundaryPositions: new Float32Array(boundaryPositions),
    skirtPositions: new Float32Array(skirt),
  });
}

// A spherical nearest-support cell is the intersection of homogeneous
// half-spaces x·(support-neighbour) >= 0 and the small-circle cap
// x·Up >= sin(10°). Its vertices therefore are intersections of two
// bisector great circles, or of one bisector with the 10° small circle.
// Values are attached once per resulting cell; this is visual tessellation,
// never interpolation of a meteorological or derived quantity.
function sphericalVoronoiCell(vectors, cellID, boundaryElevationDeg) {
  const support = vectors[cellID];
  const constraints = vectors
    .map((vector, index) => ({ normal: subtract(support, vector), index, proximity: dot(support, vector) }))
    .filter((entry) => entry.index !== cellID)
    .sort((left, right) => right.proximity - left.proximity);
  const boundaryZ = Math.sin(boundaryElevationDeg * DEG);
  const tangentReference = Math.abs(support[2]) < 0.9 ? [0, 0, 1] : [1, 0, 0];
  const tangentX = normalize(cross(tangentReference, support));
  const tangentY = cross(support, tangentX);
  let candidateCount = Math.min(VORONOI_CANDIDATE_NEIGHBOURS, constraints.length);
  while (candidateCount <= constraints.length) {
    const candidates = constraints.slice(0, candidateCount);
    const vertices = voronoiVertices(candidates, boundaryZ);
    if (vertices.length >= 3) {
      vertices.sort((left, right) => (
        Math.atan2(dot(left, tangentY), dot(left, tangentX)) -
        Math.atan2(dot(right, tangentY), dot(right, tangentX))
      ));
      const polygon = densifySmallCircleEdges(vertices, boundaryZ);
      const omitted = constraints.slice(candidateCount);
      // Convexity makes an omitted hemisphere redundant when every polygon
      // vertex is inside it. If that proof fails, increase the exact
      // intersection set; the usual dense-v1 case stops at 48.
      if (!omitted.some((constraint) => polygon.some((vertex) => dot(vertex, constraint.normal) < -2e-9))) {
        return polygon;
      }
    }
    if (candidateCount === constraints.length) {
      break;
    }
    candidateCount = Math.min(constraints.length, candidateCount * 2);
  }
  throw new Error(`embedded geometry produced an incomplete Voronoi cell ${cellID}`);
}

function voronoiVertices(constraints, boundaryZ) {
  const vertices = [];
  for (let left = 0; left < constraints.length; left += 1) {
    for (let right = left + 1; right < constraints.length; right += 1) {
      const axis = cross(constraints[left].normal, constraints[right].normal);
      const length = Math.hypot(axis[0], axis[1], axis[2]);
      if (length <= 1e-12) {
        continue;
      }
      const intersection = scale(axis, 1 / length);
      acceptVertex(vertices, intersection, constraints, boundaryZ);
      acceptVertex(vertices, scale(intersection, -1), constraints, boundaryZ);
    }
  }
  for (const constraint of constraints) {
    for (const intersection of greatCircleSmallCircleIntersections(constraint.normal, boundaryZ)) {
      acceptVertex(vertices, intersection, constraints, boundaryZ);
    }
  }
  return vertices;
}

function acceptVertex(destination, vertex, constraints, boundaryZ) {
  if (vertex[2] < boundaryZ - 2e-9 || !insideAll(vertex, constraints)) {
    return;
  }
  if (!destination.some((existing) => dot(existing, vertex) > 1 - 1e-12)) {
    destination.push(vertex);
  }
}

function insideAll(vertex, constraints) {
  return constraints.every((constraint) => dot(vertex, constraint.normal) >= -2e-9);
}

function greatCircleSmallCircleIntersections(normal, boundaryZ) {
  const horizontalSquared = normal[0] * normal[0] + normal[1] * normal[1];
  if (horizontalSquared <= 1e-20) {
    return [];
  }
  const horizontal = Math.sqrt(horizontalSquared);
  const radius = Math.sqrt(Math.max(0, 1 - boundaryZ * boundaryZ));
  const lineOffset = -normal[2] * boundaryZ;
  const closestSquared = lineOffset * lineOffset / horizontalSquared;
  if (closestSquared > radius * radius + 1e-12) {
    return [];
  }
  const closest = [
    normal[0] * lineOffset / horizontalSquared,
    normal[1] * lineOffset / horizontalSquared,
  ];
  const offset = Math.sqrt(Math.max(0, radius * radius - closestSquared));
  const perpendicular = [-normal[1] / horizontal, normal[0] / horizontal];
  return [
    normalize([closest[0] + perpendicular[0] * offset, closest[1] + perpendicular[1] * offset, boundaryZ]),
    normalize([closest[0] - perpendicular[0] * offset, closest[1] - perpendicular[1] * offset, boundaryZ]),
  ];
}

function densifySmallCircleEdges(vertices, boundaryZ) {
  const result = [];
  for (let index = 0; index < vertices.length; index += 1) {
    const start = vertices[index];
    const end = vertices[(index + 1) % vertices.length];
    result.push(start);
    if (Math.abs(start[2] - boundaryZ) > 2e-8 || Math.abs(end[2] - boundaryZ) > 2e-8) {
      continue;
    }
    const startAzimuth = Math.atan2(start[0], start[1]) / DEG;
    const endAzimuth = Math.atan2(end[0], end[1]) / DEG;
    const delta = ((endAzimuth - startAzimuth + 540) % 360) - 180;
    const segments = Math.ceil(Math.abs(delta));
    for (let step = 1; step < segments; step += 1) {
      result.push(directionVector(startAzimuth + delta * step / segments, Math.asin(boundaryZ) / DEG));
    }
  }
  return Object.freeze(result);
}

export function nearestNodeIndex(vectors, direction, minimumElevationDeg = 10) {
  const target = normalize(direction);
  if (Math.asin(clamp(target[2], -1, 1)) / DEG < minimumElevationDeg - 1e-7) {
    return -1;
  }
  let bestIndex = -1;
  let bestDot = -Infinity;
  for (let index = 0; index < vectors.length; index += 1) {
    const similarity = dot(target, vectors[index]);
    if (similarity > bestDot) {
      bestDot = similarity;
      bestIndex = index;
    }
  }
  return bestIndex;
}

export function polarPoint(vector, centerX, centerY, radius, outerBoundaryDeg = 10) {
  const position = azimuthElevation(vector);
  const radial = radius * (90 - position.elevationDeg) / (90 - outerBoundaryDeg);
  const azimuth = position.azimuthDeg * DEG;
  return Object.freeze({
    x: centerX + radial * Math.sin(azimuth),
    y: centerY - radial * Math.cos(azimuth),
  });
}

function buildReferenceGrid(outerBoundaryDeg) {
  const positions = [];
  const rings = [20, 30, 45, 60, 75];
  for (const elevation of rings) {
    for (let azimuth = 0; azimuth < 360; azimuth += 2) {
      appendVertex(positions, scale(directionVector(azimuth, elevation), 0.994));
      appendVertex(positions, scale(directionVector(azimuth + 2, elevation), 0.994));
    }
  }
  for (let azimuth = 0; azimuth < 360; azimuth += 22.5) {
    for (let elevation = outerBoundaryDeg; elevation < 90; elevation += 2) {
      appendVertex(positions, scale(directionVector(azimuth, elevation), 0.994));
      appendVertex(positions, scale(directionVector(azimuth, Math.min(90, elevation + 2)), 0.994));
    }
  }
  return positions;
}

function buildBoundary(outerBoundaryDeg) {
  const positions = [];
  for (let azimuth = 0; azimuth < 360; azimuth += 1) {
    appendVertex(positions, scale(directionVector(azimuth, outerBoundaryDeg), 0.992));
    appendVertex(positions, scale(directionVector(azimuth + 1, outerBoundaryDeg), 0.992));
  }
  return positions;
}

function buildSkirt(outerBoundaryDeg) {
  const positions = [];
  const lower = -28;
  for (let azimuth = 0; azimuth < 360; azimuth += 2) {
    const next = azimuth + 2;
    const a = directionVector(azimuth, lower);
    const b = directionVector(next, lower);
    const c = directionVector(next, outerBoundaryDeg);
    const d = directionVector(azimuth, outerBoundaryDeg);
    appendVertex(positions, a);
    appendVertex(positions, b);
    appendVertex(positions, c);
    appendVertex(positions, a);
    appendVertex(positions, c);
    appendVertex(positions, d);
  }
  return positions;
}

function appendVertex(destination, vector) {
  destination.push(vector[0], vector[1], vector[2]);
}

function add(left, right) {
  return [left[0] + right[0], left[1] + right[1], left[2] + right[2]];
}

function subtract(left, right) {
  return [left[0] - right[0], left[1] - right[1], left[2] - right[2]];
}

function cross(left, right) {
  return [
    left[1] * right[2] - left[2] * right[1],
    left[2] * right[0] - left[0] * right[2],
    left[0] * right[1] - left[1] * right[0],
  ];
}

function scale(vector, factor) {
  return [vector[0] * factor, vector[1] * factor, vector[2] * factor];
}

function dot(left, right) {
  return left[0] * right[0] + left[1] * right[1] + left[2] * right[2];
}

function normalize(vector) {
  const length = Math.hypot(vector[0], vector[1], vector[2]);
  if (!Number.isFinite(length) || length <= Number.EPSILON) {
    throw new Error("cannot normalize a degenerate direction");
  }
  return [vector[0] / length, vector[1] / length, vector[2] / length];
}

function clamp(value, minimum, maximum) {
  return Math.max(minimum, Math.min(maximum, value));
}
