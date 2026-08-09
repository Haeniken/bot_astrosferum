import {
  validateAvailability,
  validateArchivedDataset,
  validateDataset,
  validateJob,
  validatePoints,
  validateProfile,
  validateVisualizations,
} from "./contract.js";

const SMALL_RESPONSE_LIMIT = 1024 * 1024;
const DATASET_RESPONSE_LIMIT = 128 * 1024 * 1024;

export class APIError extends Error {
  constructor(status, code, message) {
    super(message || code || `HTTP ${status}`);
    this.name = "APIError";
    this.status = status;
    this.code = code || "request_failed";
  }
}

export class AstrosferumAPI {
  constructor() {
    this.csrfToken = "";
  }

  async profile(signal) {
    const response = await fetch("/api/v1/me", requestOptions(signal));
    if (response.status === 401) {
      return validateProfile({ authenticated: false, language: "en" });
    }
    const profile = validateProfile(await decode(response, SMALL_RESPONSE_LIMIT));
    this.csrfToken = profile.authenticated ? profile.csrfToken : "";
    return profile;
  }

  async points(signal) {
    return validatePoints(await getJSON("/api/v1/points", signal, SMALL_RESPONSE_LIMIT));
  }

  async setLanguage(language, signal) {
    if (language !== "ru" && language !== "en") {
      throw new APIError(400, "invalid_language", "Unsupported language");
    }
    const response = await fetch("/api/v1/preferences/language", requestOptions(signal, {
      method: "PUT",
      headers: this.unsafeHeaders(),
      body: JSON.stringify({ language }),
    }));
    if (response.status !== 204) {
      throw await apiError(response);
    }
  }

  async availability(signal) {
    return validateAvailability(await getJSON("/api/v1/astrodome/availability", signal, SMALL_RESPONSE_LIMIT));
  }

  async visualizations(signal) {
    return validateVisualizations(await getJSON("/api/v1/astrodome/visualizations", signal, SMALL_RESPONSE_LIMIT));
  }

  async createJob(request, signal) {
    const response = await fetch("/api/v1/astrodome/jobs", requestOptions(signal, {
      method: "POST",
      headers: this.unsafeHeaders(),
      body: JSON.stringify(request),
    }));
    if (response.status !== 202) {
      throw await apiError(response);
    }
    return validateJob(await decode(response, SMALL_RESPONSE_LIMIT));
  }

  async job(id, signal) {
    return validateJob(await getJSON(`/api/v1/astrodome/jobs/${encodeURIComponent(id)}`, signal, SMALL_RESPONSE_LIMIT));
  }

  async result(id, signal) {
    const jobID = safeJobID(id);
    return validateDataset(await getJSON(`/api/v1/astrodome/jobs/${encodeURIComponent(jobID)}/dataset`, signal, DATASET_RESPONSE_LIMIT));
  }

  async savedResult(id, signal) {
    if (typeof id !== "string" || !/^viz_[0-9a-f]{32}$/.test(id)) {
      throw new APIError(500, "invalid_visualization_id", "Visualization identifier violates the client contract");
    }
    const payload = await getJSON(
      `/api/v1/astrodome/visualizations/${encodeURIComponent(id)}/dataset`, signal, DATASET_RESPONSE_LIMIT,
    );
    return validateArchivedDataset(payload);
  }

  async cancelJob(id, signal) {
    const response = await fetch(`/api/v1/astrodome/jobs/${encodeURIComponent(id)}`, requestOptions(signal, {
      method: "DELETE",
      headers: this.unsafeHeaders(false),
    }));
    if (response.status !== 204) {
      throw await apiError(response);
    }
  }

  async logout(signal) {
    const response = await fetch("/api/v1/logout", requestOptions(signal, {
      method: "POST",
      headers: this.unsafeHeaders(false),
    }));
    if (response.status !== 204) {
      throw await apiError(response);
    }
    this.csrfToken = "";
  }

  unsafeHeaders(json = true) {
    if (!this.csrfToken) {
      throw new APIError(401, "missing_csrf", "Authenticated CSRF token is unavailable");
    }
    const headers = {
      "X-Astrosferum-CSRF": this.csrfToken,
    };
    if (json) {
      headers["Content-Type"] = "application/json";
    }
    return headers;
  }
}

async function getJSON(path, signal, maximumBytes) {
  const response = await fetch(path, requestOptions(signal));
  return decode(response, maximumBytes);
}

function requestOptions(signal, overrides = {}) {
  const { headers = {}, ...rest } = overrides;
  return {
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
    signal,
    ...rest,
    headers: { Accept: "application/json", ...headers },
  };
}

async function decode(response, maximumBytes) {
  if (!response.ok) {
    throw await apiError(response);
  }
  const mediaType = (response.headers.get("Content-Type") || "").split(";", 1)[0].trim().toLowerCase();
  if (mediaType !== "application/json") {
    throw new APIError(response.status, "invalid_content_type", "Expected application/json");
  }
  const declared = Number(response.headers.get("Content-Length"));
  if (Number.isFinite(declared) && declared > maximumBytes) {
    throw new APIError(response.status, "response_too_large", "Response exceeds the client limit");
  }
  const bytes = await response.arrayBuffer();
  if (bytes.byteLength > maximumBytes) {
    throw new APIError(response.status, "response_too_large", "Response exceeds the client limit");
  }
  try {
    return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
  } catch (error) {
    throw new APIError(response.status, "invalid_json", `Invalid UTF-8 JSON: ${error.message}`);
  }
}

async function apiError(response) {
  let code = "request_failed";
  let message = `HTTP ${response.status}`;
  try {
    const bytes = await response.arrayBuffer();
    if (bytes.byteLength <= SMALL_RESPONSE_LIMIT) {
      const body = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
      if (body && typeof body === "object") {
        if (typeof body.error === "string") {
          code = body.error;
        } else if (typeof body.code === "string") {
          code = body.code;
        }
        if (typeof body.message === "string") {
          message = body.message;
        }
      }
    }
  } catch {
    // The status and generic code remain sufficient; never copy an HTML body.
  }
  return new APIError(response.status, code, message);
}

function safeJobID(value) {
  if (typeof value !== "string" || !/^[A-Za-z0-9_-]{20,128}$/.test(value)) {
    throw new APIError(500, "invalid_job_id", "Job identifier violates the client contract");
  }
  return value;
}
