export class ApiError extends Error {
  constructor(message, status, code, body) {
    super(message);
    this.status = status;
    this.code = code;
    // The parsed body, when it carried {error, code, field}: err() translates it by code.
    this.body = body;
  }
}

let csrfToken = "";

export function setCSRF(token) {
  csrfToken = token;
}

export async function api(path, options = {}) {
  const headers = { ...(options.headers || {}) };
  if (options.body && !(options.body instanceof FormData)) {
    headers["Content-Type"] = "application/json";
  }
  if (options.method && options.method !== "GET" && options.method !== "HEAD") {
    headers["X-CSRF-Token"] = csrfToken;
  }
  const response = await fetch(path, { credentials: "same-origin", ...options, headers });
  const text = await response.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }
  if (response.status === 401) {
    throw new ApiError(data?.error || "Sign in required.", 401, data?.code || "unauthorized", data);
  }
  if (!response.ok) {
    throw new ApiError(data?.error || "request failed", response.status, data?.code || "unknown", data);
  }
  return data;
}
