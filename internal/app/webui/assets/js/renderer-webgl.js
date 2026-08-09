import { directionVector, nearestNodeIndex } from "./geometry.js";
import { colorForNode, qualityCode } from "./palette.js";
import { projectDirection, viewProjectionMatrix } from "./view-controller.js";

const VERTEX_SHADER = `#version 300 es
in vec3 a_position;
in vec3 a_color;
in float a_quality;
uniform mat4 u_view_projection;
uniform float u_alpha;
flat out vec3 v_color;
flat out float v_quality;
void main() {
  gl_Position = u_view_projection * vec4(a_position, 1.0);
  v_color = a_color;
  v_quality = a_quality;
}`;

const FRAGMENT_SHADER = `#version 300 es
precision highp float;
flat in vec3 v_color;
flat in float v_quality;
uniform float u_alpha;
out vec4 output_color;
void main() {
  vec3 color = v_color;
  if (v_quality < 0.5) {
    float hatch = step(5.0, mod(gl_FragCoord.x + gl_FragCoord.y, 10.0));
    color *= mix(0.48, 0.72, hatch);
  } else if (v_quality < 1.5) {
    float hatch = step(6.0, mod(gl_FragCoord.x - gl_FragCoord.y, 12.0));
    color = mix(color * 0.58, vec3(0.62, 0.43, 0.31), hatch * 0.42);
  } else if (v_quality < 2.5) {
    float hatch = step(8.0, mod(gl_FragCoord.x + gl_FragCoord.y, 16.0));
    color = mix(color, vec3(0.96, 0.68, 0.28), hatch * 0.24);
  }
  output_color = vec4(color, u_alpha);
}`;

export class WebGLDomeRenderer {
  constructor(canvas, overlay, geometry, translate) {
    this.canvas = canvas;
    this.overlay = overlay;
    this.geometry = geometry;
    this.translate = translate;
    this.gl = canvas.getContext("webgl2", {
      alpha: true,
      antialias: true,
      depth: true,
      failIfMajorPerformanceCaveat: true,
      powerPreference: "high-performance",
    });
    if (!this.gl) {
      throw new Error("WebGL2 is unavailable");
    }
    this.program = makeProgram(this.gl, VERTEX_SHADER, FRAGMENT_SHADER);
    this.locations = {
      position: this.gl.getAttribLocation(this.program, "a_position"),
      color: this.gl.getAttribLocation(this.program, "a_color"),
      quality: this.gl.getAttribLocation(this.program, "a_quality"),
      matrix: this.gl.getUniformLocation(this.program, "u_view_projection"),
      alpha: this.gl.getUniformLocation(this.program, "u_alpha"),
    };
    this.meshPosition = makeBuffer(this.gl, geometry.trianglePositions, this.gl.STATIC_DRAW);
    this.meshColor = makeBuffer(this.gl, new Float32Array(geometry.triangleCellIDs.length * 3), this.gl.DYNAMIC_DRAW);
    this.meshQuality = makeBuffer(this.gl, new Float32Array(geometry.triangleCellIDs.length), this.gl.DYNAMIC_DRAW);
    this.borderPosition = makeBuffer(this.gl, geometry.borderPositions, this.gl.STATIC_DRAW);
    this.gridPosition = makeBuffer(this.gl, geometry.gridPositions, this.gl.STATIC_DRAW);
    this.boundaryPosition = makeBuffer(this.gl, geometry.boundaryPositions, this.gl.STATIC_DRAW);
    this.skirtPosition = makeBuffer(this.gl, geometry.skirtPositions, this.gl.STATIC_DRAW);
    this.selectionPosition = this.gl.createBuffer();
    this.selectionCount = 0;
    this.lastFrame = null;
    this.lastLayer = "overall";
    this.lastSelected = -1;
    this.lastState = { yawDeg: 0, pitchDeg: 28, fovDeg: 72 };
    this.canvas.addEventListener("webglcontextlost", (event) => event.preventDefault());
  }

  pick(direction) {
    return nearestNodeIndex(this.geometry.vectors, direction, this.geometry.profile.outerBoundaryDeg);
  }

  setFrame(frame, layer) {
    this.lastFrame = frame;
    this.lastLayer = layer;
    const colors = new Float32Array(this.geometry.triangleCellIDs.length * 3);
    const qualities = new Float32Array(this.geometry.triangleCellIDs.length);
    for (let vertex = 0; vertex < this.geometry.triangleCellIDs.length; vertex += 1) {
      const node = frame.nodes[this.geometry.triangleCellIDs[vertex]];
      const color = colorForNode(node, layer);
      colors[vertex * 3] = color[0];
      colors[vertex * 3 + 1] = color[1];
      colors[vertex * 3 + 2] = color[2];
      qualities[vertex] = qualityCode(node);
    }
    const gl = this.gl;
    gl.bindBuffer(gl.ARRAY_BUFFER, this.meshColor);
    gl.bufferSubData(gl.ARRAY_BUFFER, 0, colors);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.meshQuality);
    gl.bufferSubData(gl.ARRAY_BUFFER, 0, qualities);
  }

  setSelected(index) {
    this.lastSelected = index;
    const cell = this.geometry.cells[index];
    if (!cell) {
      this.selectionCount = 0;
      return;
    }
    const positions = [];
    for (let vertex = 0; vertex < cell.polygon.length; vertex += 1) {
      const current = cell.polygon[vertex];
      const next = cell.polygon[(vertex + 1) % cell.polygon.length];
      positions.push(current[0] * 0.991, current[1] * 0.991, current[2] * 0.991);
      positions.push(next[0] * 0.991, next[1] * 0.991, next[2] * 0.991);
    }
    this.selectionCount = positions.length / 3;
    this.gl.bindBuffer(this.gl.ARRAY_BUFFER, this.selectionPosition);
    this.gl.bufferData(this.gl.ARRAY_BUFFER, new Float32Array(positions), this.gl.DYNAMIC_DRAW);
  }

  render(state = this.lastState) {
    if (!this.lastFrame) {
      return;
    }
    this.lastState = state;
    const gl = this.gl;
    const { width, height } = resizeCanvas(this.canvas);
    resizeOverlay(this.overlay, width, height, this.canvas.clientWidth, this.canvas.clientHeight);
    gl.viewport(0, 0, width, height);
    gl.clearColor(0, 0, 0, 0);
    gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT);
    gl.useProgram(this.program);
    gl.disable(gl.CULL_FACE);
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
    gl.enable(gl.DEPTH_TEST);
    gl.depthFunc(gl.LEQUAL);
    gl.uniformMatrix4fv(this.locations.matrix, false, viewProjectionMatrix(state, width / Math.max(1, height)));

    this.drawConstant(this.skirtPosition, this.geometry.skirtPositions.length / 3, gl.TRIANGLES, [0.026, 0.055, 0.105], 1, 0.86);
    this.drawMesh();
    gl.disable(gl.DEPTH_TEST);
    this.drawConstant(this.borderPosition, this.geometry.borderPositions.length / 3, gl.LINES, [0.025, 0.065, 0.13], 4, 0.82);
    this.drawConstant(this.gridPosition, this.geometry.gridPositions.length / 3, gl.LINES, [0.19, 0.66, 0.78], 4, 0.34);
    this.drawConstant(this.gridPosition, this.geometry.gridPositions.length / 3, gl.LINES, [0.55, 0.89, 0.96], 2, 0.72);
    this.drawConstant(this.boundaryPosition, this.geometry.boundaryPositions.length / 3, gl.LINES, [0.18, 0.85, 0.85], 6, 0.32);
    this.drawConstant(this.boundaryPosition, this.geometry.boundaryPositions.length / 3, gl.LINES, [0.68, 1, 0.95], 3, 0.96);
    if (this.selectionCount > 0) {
      this.drawConstant(this.selectionPosition, this.selectionCount, gl.LINES, [1, 0.64, 0.18], 8, 0.34);
      this.drawConstant(this.selectionPosition, this.selectionCount, gl.LINES, [1, 0.92, 0.48], 3, 1);
    }
    this.drawOverlay(state);
  }

  drawMesh() {
    const gl = this.gl;
    bindAttribute(gl, this.locations.position, this.meshPosition, 3);
    bindAttribute(gl, this.locations.color, this.meshColor, 3);
    bindAttribute(gl, this.locations.quality, this.meshQuality, 1);
    gl.uniform1f(this.locations.alpha, 1);
    gl.drawArrays(gl.TRIANGLES, 0, this.geometry.triangleCellIDs.length);
  }

  drawConstant(positionBuffer, count, mode, color, quality, alpha = 1) {
    const gl = this.gl;
    bindAttribute(gl, this.locations.position, positionBuffer, 3);
    gl.disableVertexAttribArray(this.locations.color);
    gl.vertexAttrib3f(this.locations.color, color[0], color[1], color[2]);
    gl.disableVertexAttribArray(this.locations.quality);
    gl.vertexAttrib1f(this.locations.quality, quality);
    gl.uniform1f(this.locations.alpha, alpha);
    gl.drawArrays(mode, 0, count);
  }

  drawOverlay(state) {
    const context = this.overlay.getContext("2d");
    const width = this.overlay.clientWidth;
    const height = this.overlay.clientHeight;
    context.clearRect(0, 0, width, height);
    context.save();
    context.font = "600 12px system-ui, sans-serif";
    context.textAlign = "center";
    context.textBaseline = "middle";
    context.lineWidth = 3;
    context.strokeStyle = "rgba(7, 17, 35, .88)";
    context.fillStyle = "rgba(236, 246, 255, .94)";
    this.drawSelectedOverlay(context, state, width, height);
    const labelAzimuth = Math.round(state.yawDeg / 22.5) * 22.5;
    for (const elevation of [10, 20, 30, 45, 60, 75, 90]) {
      const point = projectDirection(directionVector(labelAzimuth, elevation), state, width, height);
      if (!point) {
        continue;
      }
      const text = elevation === 10 ? this.translate("outerShort") : `${elevation}\u00b0`;
      if (elevation === 10) {
        drawBadge(context, text, point.x, point.y);
        continue;
      }
      context.strokeText(text, point.x, point.y);
      context.fillText(text, point.x, point.y);
    }
    this.drawCompass(context, state, width);
    context.restore();
  }

  drawSelectedOverlay(context, state, width, height) {
    const cell = this.geometry.cells[this.lastSelected];
    const vector = this.geometry.vectors[this.lastSelected];
    if (!cell || !vector) {
      return;
    }
    const center = projectDirection(vector, state, width, height);
    if (!center) {
      return;
    }

    context.save();
    const glow = context.createRadialGradient(center.x, center.y, 2, center.x, center.y, 34);
    glow.addColorStop(0, "rgba(255, 224, 116, .24)");
    glow.addColorStop(.34, "rgba(255, 181, 67, .10)");
    glow.addColorStop(1, "rgba(255, 181, 67, 0)");
    context.fillStyle = glow;
    context.beginPath();
    context.arc(center.x, center.y, 34, 0, Math.PI * 2);
    context.fill();

    const projected = cell.polygon.map((point) => projectDirection(point, state, width, height));
    const visible = projected.filter(Boolean);
    if (visible.length === projected.length && visible.length >= 3) {
      context.beginPath();
      context.moveTo(visible[0].x, visible[0].y);
      for (let index = 1; index < visible.length; index += 1) {
        context.lineTo(visible[index].x, visible[index].y);
      }
      context.closePath();
      context.shadowColor = "rgba(255, 198, 76, .95)";
      context.shadowBlur = 18;
      context.strokeStyle = "rgba(255, 190, 64, .38)";
      context.lineWidth = 7;
      context.stroke();
      context.shadowBlur = 7;
      context.strokeStyle = "rgba(255, 239, 158, .98)";
      context.lineWidth = 2;
      context.stroke();
    }

    context.shadowColor = "rgba(255, 220, 116, .85)";
    context.shadowBlur = 10;
    context.strokeStyle = "rgba(255, 242, 179, .96)";
    context.lineWidth = 1.5;
    context.beginPath();
    context.arc(center.x, center.y, 7, 0, Math.PI * 2);
    context.stroke();
    context.restore();
  }

  drawCompass(context, state, width) {
    const radius = width < 560 ? 44 : 55;
    const x = width - radius - 15;
    // Keep the compass below the in-view mode/fullscreen toolbar in both the
    // regular and fullscreen layouts. The canvas-relative offset also remains
    // correct after responsive resizing.
    const y = radius + 70;
    context.fillStyle = "rgba(3, 12, 29, .84)";
    context.strokeStyle = "rgba(107, 228, 220, .72)";
    context.lineWidth = 1;
    context.beginPath();
    context.arc(x, y, radius, 0, Math.PI * 2);
    context.fill();
    context.stroke();
    const directions = [
      [0, "N"], [22.5, "NNE"], [45, "NE"], [67.5, "ENE"],
      [90, "E"], [112.5, "ESE"], [135, "SE"], [157.5, "SSE"],
      [180, "S"], [202.5, "SSW"], [225, "SW"], [247.5, "WSW"],
      [270, "W"], [292.5, "WNW"], [315, "NW"], [337.5, "NNW"],
    ];
    const labelRadius = radius - 11;
    for (const [azimuth, key] of directions) {
      const angle = (azimuth - state.yawDeg - 90) * Math.PI / 180;
      const cardinal = azimuth % 90 === 0;
      context.font = `${cardinal ? 800 : 650} ${cardinal ? 10 : 7}px system-ui, sans-serif`;
      context.fillStyle = cardinal ? "rgba(170, 255, 245, .98)" : "rgba(224, 241, 250, .86)";
      context.fillText(this.translate(`compass.${key}`), x + Math.cos(angle) * labelRadius, y + Math.sin(angle) * labelRadius);
    }
    context.strokeStyle = "#ffd66f";
    context.lineWidth = 2;
    context.beginPath();
    context.moveTo(x, y - 31);
    context.lineTo(x - 4, y - 21);
    context.lineTo(x + 4, y - 21);
    context.closePath();
    context.stroke();
  }

  destroy() {
    const gl = this.gl;
    for (const buffer of [
      this.meshPosition, this.meshColor, this.meshQuality, this.borderPosition,
      this.gridPosition, this.boundaryPosition, this.skirtPosition, this.selectionPosition,
    ]) {
      gl.deleteBuffer(buffer);
    }
    gl.deleteProgram(this.program);
  }
}

function drawBadge(context, text, centerX, centerY) {
  const paddingX = 8;
  const height = 22;
  const width = context.measureText(text).width + paddingX * 2;
  const left = centerX - width / 2;
  const top = centerY - height / 2;
  context.save();
  context.fillStyle = "rgba(4, 20, 38, .92)";
  context.strokeStyle = "rgba(118, 245, 229, .88)";
  context.lineWidth = 1;
  roundedRect(context, left, top, width, height, 7);
  context.fill();
  context.stroke();
  context.fillStyle = "rgba(202, 255, 246, .98)";
  context.fillText(text, centerX, centerY + .5);
  context.restore();
}

function roundedRect(context, x, y, width, height, radius) {
  const r = Math.min(radius, width / 2, height / 2);
  context.beginPath();
  context.moveTo(x + r, y);
  context.lineTo(x + width - r, y);
  context.quadraticCurveTo(x + width, y, x + width, y + r);
  context.lineTo(x + width, y + height - r);
  context.quadraticCurveTo(x + width, y + height, x + width - r, y + height);
  context.lineTo(x + r, y + height);
  context.quadraticCurveTo(x, y + height, x, y + height - r);
  context.lineTo(x, y + r);
  context.quadraticCurveTo(x, y, x + r, y);
  context.closePath();
}

function makeProgram(gl, vertexSource, fragmentSource) {
  const vertex = makeShader(gl, gl.VERTEX_SHADER, vertexSource);
  const fragment = makeShader(gl, gl.FRAGMENT_SHADER, fragmentSource);
  const program = gl.createProgram();
  gl.attachShader(program, vertex);
  gl.attachShader(program, fragment);
  gl.linkProgram(program);
  gl.deleteShader(vertex);
  gl.deleteShader(fragment);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const detail = gl.getProgramInfoLog(program);
    gl.deleteProgram(program);
    throw new Error(`WebGL program link failed: ${detail}`);
  }
  return program;
}

function makeShader(gl, type, source) {
  const shader = gl.createShader(type);
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const detail = gl.getShaderInfoLog(shader);
    gl.deleteShader(shader);
    throw new Error(`WebGL shader compilation failed: ${detail}`);
  }
  return shader;
}

function makeBuffer(gl, content, usage) {
  const buffer = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
  gl.bufferData(gl.ARRAY_BUFFER, content, usage);
  return buffer;
}

function bindAttribute(gl, location, buffer, size) {
  gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
  gl.enableVertexAttribArray(location);
  gl.vertexAttribPointer(location, size, gl.FLOAT, false, 0, 0);
}

function resizeCanvas(canvas) {
  const scale = Math.min(2, window.devicePixelRatio || 1);
  const width = Math.max(1, Math.round(canvas.clientWidth * scale));
  const height = Math.max(1, Math.round(canvas.clientHeight * scale));
  if (canvas.width !== width || canvas.height !== height) {
    canvas.width = width;
    canvas.height = height;
  }
  return { width, height };
}

function resizeOverlay(canvas, pixelWidth, pixelHeight, cssWidth, cssHeight) {
  if (canvas.width !== pixelWidth || canvas.height !== pixelHeight) {
    canvas.width = pixelWidth;
    canvas.height = pixelHeight;
  }
  const context = canvas.getContext("2d");
  context.setTransform(pixelWidth / Math.max(1, cssWidth), 0, 0, pixelHeight / Math.max(1, cssHeight), 0, 0);
}
