import { directionVector, nearestNodeIndex, polarPoint } from "./geometry.js";
import { colorForNode, qualityCode } from "./palette.js";

export class CanvasDomeRenderer {
  constructor(canvas, geometry, translate) {
    this.canvas = canvas;
    this.geometry = geometry;
    this.translate = translate;
    this.context = canvas.getContext("2d", { alpha: false });
    if (!this.context) {
      throw new Error("Canvas 2D is unavailable");
    }
    this.frame = null;
    this.layer = "overall";
    this.selected = -1;
    this.layout = null;
  }

  setFrame(frame, layer) {
    this.frame = frame;
    this.layer = layer;
  }

  setSelected(index) {
    this.selected = index;
  }

  pick(clientX, clientY) {
    if (!this.layout) {
      return -1;
    }
    const rect = this.canvas.getBoundingClientRect();
    const x = clientX - rect.left;
    const y = clientY - rect.top;
    const dx = x - this.layout.centerX;
    const dy = y - this.layout.centerY;
    const distance = Math.hypot(dx, dy);
    if (distance > this.layout.radius) {
      return -1;
    }
    const elevation = 90 - distance / this.layout.radius * 80;
    let azimuth = Math.atan2(dx, -dy) * 180 / Math.PI;
    if (azimuth < 0) {
      azimuth += 360;
    }
    return nearestNodeIndex(this.geometry.vectors, directionVector(azimuth, elevation), 10);
  }

  render() {
    if (!this.frame) {
      return;
    }
    const { width, height, scale } = resize(this.canvas);
    const cssWidth = width / scale;
    const cssHeight = height / scale;
    const context = this.context;
    context.setTransform(scale, 0, 0, scale, 0, 0);
    drawBackdrop(context, cssWidth, cssHeight, this.frame.twilight_band);
    const radius = Math.max(48, Math.min(cssWidth, cssHeight) * 0.43);
    const centerX = cssWidth / 2;
    const centerY = cssHeight / 2;
    this.layout = { centerX, centerY, radius };

    context.save();
    context.beginPath();
    context.arc(centerX, centerY, radius + 14, 0, Math.PI * 2);
    context.strokeStyle = "rgba(20, 47, 79, .92)";
    context.lineWidth = 26;
    context.shadowColor = "rgba(78, 218, 212, .22)";
    context.shadowBlur = 20;
    context.stroke();
    context.restore();

    for (const cell of this.geometry.cells) {
      const node = this.frame.nodes[cell.cellID];
      const color = colorForNode(node, this.layer);
      context.beginPath();
      for (const [index, vector] of cell.polygon.entries()) {
        const point = polarPoint(vector, centerX, centerY, radius, 10);
        if (index === 0) {
          context.moveTo(point.x, point.y);
        } else {
          context.lineTo(point.x, point.y);
        }
      }
      context.closePath();
      context.fillStyle = rgb(color);
      context.fill();
      const quality = qualityCode(node);
      if (quality < 3) {
        hatch(context, quality, centerX - radius, centerY - radius, radius * 2);
      }
      context.strokeStyle = "rgba(5, 17, 36, .78)";
      context.lineWidth = 0.65;
      context.stroke();
    }

    drawGrid(context, centerX, centerY, radius, this.translate);
    drawSelected(context, this.geometry, this.selected, centerX, centerY, radius);
  }
}

function drawGrid(context, centerX, centerY, radius, translate) {
  context.save();
  context.strokeStyle = "rgba(148, 235, 239, .68)";
  context.fillStyle = "rgba(238, 248, 255, .95)";
  context.lineWidth = 1;
  context.shadowColor = "rgba(73, 212, 211, .48)";
  context.shadowBlur = 6;
  context.font = "600 11px system-ui, sans-serif";
  context.textAlign = "center";
  context.textBaseline = "middle";
  for (const elevation of [10, 20, 30, 45, 60, 75]) {
    const ringRadius = radius * (90 - elevation) / 80;
    context.beginPath();
    context.arc(centerX, centerY, ringRadius, 0, Math.PI * 2);
    if (elevation === 10) {
      context.strokeStyle = "rgba(133, 255, 237, .98)";
      context.lineWidth = 2.6;
      context.shadowBlur = 13;
    } else {
      context.strokeStyle = "rgba(148, 235, 239, .68)";
      context.lineWidth = 1;
      context.shadowBlur = 6;
    }
    context.stroke();
    const label = elevation === 10 ? translate("outerShort") : `${elevation}\u00b0`;
    if (elevation === 10) {
      drawBadge(context, label, centerX + 2, centerY - ringRadius + 13);
    } else {
      context.fillText(label, centerX + 2, centerY - ringRadius + 10);
    }
  }
  context.fillText("90\u00b0 \u00b7 Z", centerX, centerY);
  for (let index = 0; index < 16; index += 1) {
    const azimuth = index * 22.5 * Math.PI / 180;
    context.beginPath();
    context.moveTo(centerX, centerY);
    context.lineTo(centerX + radius * Math.sin(azimuth), centerY - radius * Math.cos(azimuth));
    context.stroke();
  }
  const compass = [
    [0, "N"], [22.5, "NNE"], [45, "NE"], [67.5, "ENE"],
    [90, "E"], [112.5, "ESE"], [135, "SE"], [157.5, "SSE"],
    [180, "S"], [202.5, "SSW"], [225, "SW"], [247.5, "WSW"],
    [270, "W"], [292.5, "WNW"], [315, "NW"], [337.5, "NNW"],
  ];
  for (const [azimuthDeg, key] of compass) {
    const azimuth = azimuthDeg * Math.PI / 180;
    const cardinal = azimuthDeg % 90 === 0;
    context.font = `${cardinal ? 800 : 650} ${cardinal ? 12 : 9}px system-ui, sans-serif`;
    context.fillStyle = cardinal ? "rgba(175, 255, 246, .98)" : "rgba(232, 247, 255, .88)";
    context.fillText(
      translate(`compass.${key}`),
      centerX + (radius + 18) * Math.sin(azimuth),
      centerY - (radius + 18) * Math.cos(azimuth),
    );
  }
  context.restore();
}

function drawSelected(context, geometry, selected, centerX, centerY, radius) {
  const cell = geometry.cells[selected];
  const vector = geometry.vectors[selected];
  if (!cell || !vector) {
    return;
  }
  const target = polarPoint(vector, centerX, centerY, radius, geometry.profile.outerBoundaryDeg);
  context.save();
  context.strokeStyle = "rgba(255, 214, 103, .34)";
  context.lineWidth = 1.2;
  context.setLineDash([4, 6]);
  context.beginPath();
  context.moveTo(centerX, centerY);
  context.lineTo(target.x, target.y);
  context.stroke();
  context.setLineDash([]);

  context.beginPath();
  for (const [index, point] of cell.polygon.entries()) {
    const projected = polarPoint(point, centerX, centerY, radius, geometry.profile.outerBoundaryDeg);
    if (index === 0) {
      context.moveTo(projected.x, projected.y);
    } else {
      context.lineTo(projected.x, projected.y);
    }
  }
  context.closePath();
  context.shadowColor = "rgba(255, 190, 62, .95)";
  context.shadowBlur = 18;
  context.strokeStyle = "rgba(255, 190, 62, .42)";
  context.lineWidth = 7;
  context.stroke();
  context.shadowBlur = 7;
  context.strokeStyle = "rgba(255, 239, 153, 1)";
  context.lineWidth = 2.2;
  context.stroke();
  context.fillStyle = "rgba(255, 245, 185, .98)";
  context.beginPath();
  context.arc(target.x, target.y, 3.5, 0, Math.PI * 2);
  context.fill();
  context.restore();
}

function drawBackdrop(context, width, height, band) {
  context.fillStyle = twilightBackground(band);
  context.fillRect(0, 0, width, height);

  context.save();
  const cyanNebula = context.createRadialGradient(width * .72, height * .26, 0, width * .72, height * .26, Math.max(width, height) * .56);
  cyanNebula.addColorStop(0, "rgba(39, 142, 173, .18)");
  cyanNebula.addColorStop(.36, "rgba(24, 82, 126, .09)");
  cyanNebula.addColorStop(1, "rgba(6, 17, 38, 0)");
  context.fillStyle = cyanNebula;
  context.fillRect(0, 0, width, height);
  const violetNebula = context.createRadialGradient(width * .18, height * .75, 0, width * .18, height * .75, Math.max(width, height) * .44);
  violetNebula.addColorStop(0, "rgba(96, 61, 143, .12)");
  violetNebula.addColorStop(.5, "rgba(36, 39, 91, .06)");
  violetNebula.addColorStop(1, "rgba(6, 17, 38, 0)");
  context.fillStyle = violetNebula;
  context.fillRect(0, 0, width, height);

  const count = Math.max(80, Math.min(320, Math.round(width * height / 3800)));
  const starOpacity = twilightStarOpacity(band);
  for (let index = 0; index < count; index += 1) {
    const x = hash(index * 4 + 1) * width;
    const y = hash(index * 4 + 2) * height;
    const bright = hash(index * 4 + 3);
    const radius = bright > .96 ? 1.45 : bright > .78 ? .9 : .52;
    const alpha = starOpacity * (.42 + bright * .53);
    context.fillStyle = bright > .91
      ? `rgba(198, 230, 255, ${alpha})`
      : `rgba(239, 247, 255, ${alpha})`;
    context.beginPath();
    context.arc(x, y, radius, 0, Math.PI * 2);
    context.fill();
  }
  context.restore();
}

function hash(value) {
  const sine = Math.sin(value * 12.9898 + 78.233) * 43758.5453;
  return sine - Math.floor(sine);
}

function drawBadge(context, text, centerX, centerY) {
  const width = context.measureText(text).width + 16;
  const height = 22;
  const left = centerX - width / 2;
  const top = centerY - height / 2;
  context.save();
  context.shadowBlur = 0;
  context.fillStyle = "rgba(4, 20, 38, .92)";
  context.strokeStyle = "rgba(118, 245, 229, .9)";
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

function hatch(context, quality, x, y, size) {
  context.save();
  context.clip();
  context.strokeStyle = quality === 1 ? "rgba(255, 194, 116, .48)" : "rgba(238, 246, 255, .28)";
  context.lineWidth = quality === 2 ? 1 : 1.5;
  const gap = quality === 2 ? 13 : 9;
  for (let offset = -size; offset < size * 2; offset += gap) {
    context.beginPath();
    context.moveTo(x + offset, y);
    context.lineTo(x + offset + size, y + size);
    context.stroke();
  }
  context.restore();
}

function resize(canvas) {
  const scale = Math.min(2, window.devicePixelRatio || 1);
  const width = Math.max(1, Math.round(canvas.clientWidth * scale));
  const height = Math.max(1, Math.round(canvas.clientHeight * scale));
  if (canvas.width !== width || canvas.height !== height) {
    canvas.width = width;
    canvas.height = height;
  }
  return { width, height, scale };
}

function rgb(color) {
  return `rgb(${Math.round(color[0] * 255)} ${Math.round(color[1] * 255)} ${Math.round(color[2] * 255)})`;
}

function twilightBackground(band) {
  return {
    day: "#183c5d",
    light_twilight: "#162c4d",
    astronomical_twilight: "#0c1b38",
    astronomical_night: "#061126",
  }[band] ?? "#061126";
}

function twilightStarOpacity(band) {
  return {
    day: .04,
    light_twilight: .10,
    astronomical_twilight: .22,
    astronomical_night: .34,
  }[band] ?? .34;
}
