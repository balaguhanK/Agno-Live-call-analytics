// Thin fetch wrapper for the Go backend. Requests go to the same origin
// (Vite proxies /api and /ws in dev), so the session cookie rides along.

const API_BASE = import.meta.env.VITE_API_BASE_URL || '/api';

// Fired when the server rejects the session, so the app can return to sign-in
export const UNAUTHORIZED_EVENT = 'lca:unauthorized';

export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

export async function api(path, { method = 'GET', body } = {}) {
  let res;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      method,
      credentials: 'same-origin',
      // Every write is JSON — the backend uses this as its CSRF check
      headers: method === 'GET' ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError('Cannot reach the server. Check that the backend is running.', 0);
  }

  const data = res.status === 204 ? null : await res.json().catch(() => null);
  if (!res.ok) {
    // A 5xx without our JSON error body comes from the dev proxy, not the backend
    if (res.status >= 500 && !data?.error) {
      throw new ApiError('Cannot reach the server. Check that the backend is running.', res.status);
    }
    if (res.status === 401 && !path.startsWith('/auth/')) {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    }
    throw new ApiError(data?.error || `Request failed (${res.status}).`, res.status);
  }
  return data;
}

// GET a file download. Resolves to { blob, filename }; errors match api().
export async function apiDownload(path) {
  let res;
  try {
    res = await fetch(`${API_BASE}${path}`, { credentials: 'same-origin' });
  } catch {
    throw new ApiError('Cannot reach the server. Check that the backend is running.', 0);
  }
  if (!res.ok) {
    const data = await res.json().catch(() => null);
    if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    throw new ApiError(data?.error || `Download failed (${res.status}).`, res.status);
  }
  const match = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') || '');
  return { blob: await res.blob(), filename: match ? match[1] : 'download' };
}
