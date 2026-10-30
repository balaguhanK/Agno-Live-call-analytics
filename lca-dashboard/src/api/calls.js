import { api, apiDownload } from './client';

// Builds "?a=1&b=2", skipping empty values
const toQuery = (params = {}) => {
  const sp = new URLSearchParams();
  Object.entries(params).forEach(([k, v]) => {
    if (v !== undefined && v !== null && v !== '') sp.set(k, v);
  });
  const s = sp.toString();
  return s ? `?${s}` : '';
};

// One page of calls: { items, total, page, pageSize }.
// params: hours (0 = all time), q, sort, order ('asc' | 'desc'), page, pageSize
export const listCalls = (params) => api(`/calls${toQuery(params)}`);

export const getCall = (id) => api(`/calls/${encodeURIComponent(id)}`);

// (Re)runs the post-call analysis of an ended call; the model's result
// arrives later as a 'call' event
export const analyseCall = (id) => api(`/calls/${encodeURIComponent(id)}/analysis`, { method: 'POST', body: {} });

// Downloads matching calls as CSV. Pass ids (comma-separated) to export only those.
export async function downloadCalls(params) {
  const { blob, filename } = await apiDownload(`/calls/export${toQuery(params)}`);
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

// Live call events: { type: 'call', call }, { type: 'segment', callId, segment },
// { type: 'assist', callId, assist }, and { type: 'open' } each time the socket
// (re)connects, so pages can catch up on anything sent while it was down.
// Every subscriber shares one WebSocket, which reconnects with backoff and
// closes a little after the last subscriber leaves: moving between pages
// keeps it open, so no event is lost in between. Returns an unsubscribe function.
const SOCKET_LINGER_MS = 5000;
const listeners = new Set();
let socket;
let retryTimer;
let closeTimer;
let delay = 1000;

const connect = () => {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
  socket = new WebSocket(`${scheme}://${window.location.host}/ws`);
  socket.onopen = () => {
    delay = 1000;
    listeners.forEach((fn) => fn({ type: 'open' }));
  };
  socket.onmessage = (msg) => {
    let event;
    try { event = JSON.parse(msg.data); } catch { return; /* ignore malformed event */ }
    listeners.forEach((fn) => fn(event));
  };
  socket.onclose = () => {
    socket = null;
    if (listeners.size === 0) return;
    retryTimer = setTimeout(connect, delay);
    delay = Math.min(delay * 2, 30000);
  };
};

export function subscribeToCalls(onEvent) {
  listeners.add(onEvent);
  clearTimeout(closeTimer);
  closeTimer = undefined;
  if (!socket && !retryTimer) connect();
  return () => {
    listeners.delete(onEvent);
    if (listeners.size > 0) return;
    clearTimeout(closeTimer);
    closeTimer = setTimeout(() => {
      closeTimer = undefined;
      if (listeners.size > 0) return;
      clearTimeout(retryTimer);
      retryTimer = undefined;
      socket?.close();
    }, SOCKET_LINGER_MS);
  };
}
