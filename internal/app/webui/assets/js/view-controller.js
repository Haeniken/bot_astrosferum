const DEG = Math.PI / 180;

export class ViewController {
  constructor(canvas, onChange, onPick) {
    this.canvas = canvas;
    this.onChange = onChange;
    this.onPick = onPick;
    this.state = { yawDeg: 0, pitchDeg: 28, fovDeg: 72 };
    this.pointer = null;
    this.listeners = [];
    this.listen(canvas, "pointerdown", (event) => this.pointerDown(event));
    this.listen(canvas, "pointermove", (event) => this.pointerMove(event));
    this.listen(canvas, "pointerup", (event) => this.pointerUp(event));
    this.listen(canvas, "pointercancel", (event) => this.pointerUp(event));
    this.listen(canvas, "wheel", (event) => this.wheel(event), { passive: false });
    this.listen(canvas, "keydown", (event) => this.keyDown(event));
    this.listen(canvas, "dblclick", (event) => this.pick(event.clientX, event.clientY));
  }

  listen(target, type, listener, options) {
    target.addEventListener(type, listener, options);
    this.listeners.push(() => target.removeEventListener(type, listener, options));
  }

  pointerDown(event) {
    if (event.button !== 0 && event.pointerType === "mouse") {
      return;
    }
    this.canvas.focus({ preventScroll: true });
    this.canvas.setPointerCapture(event.pointerId);
    this.pointer = { id: event.pointerId, x: event.clientX, y: event.clientY, moved: false };
  }

  pointerMove(event) {
    if (!this.pointer || this.pointer.id !== event.pointerId) {
      return;
    }
    const dx = event.clientX - this.pointer.x;
    const dy = event.clientY - this.pointer.y;
    if (Math.abs(dx) + Math.abs(dy) > 1) {
      this.pointer.moved = true;
    }
    this.pointer.x = event.clientX;
    this.pointer.y = event.clientY;
    this.state.yawDeg = normalizeDegrees(this.state.yawDeg - dx * 0.18);
    this.state.pitchDeg = clamp(this.state.pitchDeg + dy * 0.14, -12, 88);
    this.onChange(this.snapshot());
  }

  pointerUp(event) {
    if (!this.pointer || this.pointer.id !== event.pointerId) {
      return;
    }
    const moved = this.pointer.moved;
    this.pointer = null;
    if (this.canvas.hasPointerCapture(event.pointerId)) {
      this.canvas.releasePointerCapture(event.pointerId);
    }
    if (!moved) {
      this.pick(event.clientX, event.clientY);
    }
  }

  wheel(event) {
    event.preventDefault();
    this.state.fovDeg = clamp(this.state.fovDeg + Math.sign(event.deltaY) * 4, 34, 100);
    this.onChange(this.snapshot());
  }

  keyDown(event) {
    let handled = true;
    const rotationStep = event.shiftKey ? 2.5 : 7.5;
    switch (event.key) {
    case "ArrowLeft":
    case "a":
    case "A":
      this.state.yawDeg = normalizeDegrees(this.state.yawDeg - rotationStep);
      break;
    case "ArrowRight":
    case "d":
    case "D":
      this.state.yawDeg = normalizeDegrees(this.state.yawDeg + rotationStep);
      break;
    case "ArrowUp":
    case "w":
    case "W":
      this.state.pitchDeg = clamp(this.state.pitchDeg + rotationStep, -12, 88);
      break;
    case "ArrowDown":
    case "s":
    case "S":
      this.state.pitchDeg = clamp(this.state.pitchDeg - rotationStep, -12, 88);
      break;
    case "+":
    case "=":
      this.state.fovDeg = clamp(this.state.fovDeg - 5, 34, 100);
      break;
    case "-":
    case "_":
      this.state.fovDeg = clamp(this.state.fovDeg + 5, 34, 100);
      break;
    case "Home":
      this.state = { yawDeg: 0, pitchDeg: 28, fovDeg: 72 };
      break;
    case "Enter": {
      const { forward } = cameraBasis(this.state);
      this.onPick(forward, null);
      break;
    }
    default:
      handled = false;
    }
    if (handled) {
      event.preventDefault();
      this.onChange(this.snapshot());
    }
  }

  pick(clientX, clientY) {
    this.onPick(rayFromCanvasPoint(this.canvas, clientX, clientY, this.state), { clientX, clientY });
  }

  snapshot() {
    return Object.freeze({ ...this.state });
  }

  destroy() {
    for (const remove of this.listeners.splice(0)) {
      remove();
    }
  }
}

export function cameraBasis(state) {
  const yaw = state.yawDeg * DEG;
  const pitch = state.pitchDeg * DEG;
  const forward = normalize([
    Math.cos(pitch) * Math.sin(yaw),
    Math.cos(pitch) * Math.cos(yaw),
    Math.sin(pitch),
  ]);
  let right = normalize(cross(forward, [0, 0, 1]));
  if (Math.abs(forward[2]) > 0.9999) {
    right = [Math.cos(yaw), -Math.sin(yaw), 0];
  }
  const up = normalize(cross(right, forward));
  return Object.freeze({ forward, right, up });
}

export function rayFromCanvasPoint(canvas, clientX, clientY, state) {
  const rect = canvas.getBoundingClientRect();
  const x = 2 * (clientX - rect.left) / Math.max(1, rect.width) - 1;
  const y = 1 - 2 * (clientY - rect.top) / Math.max(1, rect.height);
  const aspect = Math.max(1e-6, rect.width / Math.max(1, rect.height));
  const tangent = Math.tan(state.fovDeg * DEG / 2);
  const { forward, right, up } = cameraBasis(state);
  return normalize([
    forward[0] + right[0] * x * tangent * aspect + up[0] * y * tangent,
    forward[1] + right[1] * x * tangent * aspect + up[1] * y * tangent,
    forward[2] + right[2] * x * tangent * aspect + up[2] * y * tangent,
  ]);
}

export function viewProjectionMatrix(state, aspect) {
  const { forward, right, up } = cameraBasis(state);
  const view = new Float32Array([
    right[0], up[0], -forward[0], 0,
    right[1], up[1], -forward[1], 0,
    right[2], up[2], -forward[2], 0,
    0, 0, 0, 1,
  ]);
  const near = 0.01;
  const far = 4;
  const f = 1 / Math.tan(state.fovDeg * DEG / 2);
  const projection = new Float32Array([
    f / aspect, 0, 0, 0,
    0, f, 0, 0,
    0, 0, (far + near) / (near - far), -1,
    0, 0, 2 * far * near / (near - far), 0,
  ]);
  return multiply4x4(projection, view);
}

export function projectDirection(vector, state, width, height) {
  const { forward, right, up } = cameraBasis(state);
  const depth = dot(vector, forward);
  if (depth <= 0.01) {
    return null;
  }
  const tangent = Math.tan(state.fovDeg * DEG / 2);
  const aspect = width / Math.max(1, height);
  const ndcX = dot(vector, right) / (depth * tangent * aspect);
  const ndcY = dot(vector, up) / (depth * tangent);
  if (Math.abs(ndcX) > 1.15 || Math.abs(ndcY) > 1.15) {
    return null;
  }
  return Object.freeze({
    x: (ndcX + 1) * width / 2,
    y: (1 - ndcY) * height / 2,
    depth,
  });
}

function multiply4x4(left, right) {
  const output = new Float32Array(16);
  for (let column = 0; column < 4; column += 1) {
    for (let row = 0; row < 4; row += 1) {
      let value = 0;
      for (let index = 0; index < 4; index += 1) {
        value += left[index * 4 + row] * right[column * 4 + index];
      }
      output[column * 4 + row] = value;
    }
  }
  return output;
}

function cross(left, right) {
  return [
    left[1] * right[2] - left[2] * right[1],
    left[2] * right[0] - left[0] * right[2],
    left[0] * right[1] - left[1] * right[0],
  ];
}

function dot(left, right) {
  return left[0] * right[0] + left[1] * right[1] + left[2] * right[2];
}

function normalize(vector) {
  const length = Math.hypot(vector[0], vector[1], vector[2]);
  return [vector[0] / length, vector[1] / length, vector[2] / length];
}

function normalizeDegrees(value) {
  return ((value % 360) + 360) % 360;
}

function clamp(value, minimum, maximum) {
  return Math.max(minimum, Math.min(maximum, value));
}
