// Auth and user management, backed by the Go API (lca-backend). The session
// lives in an HttpOnly cookie; nothing sensitive is kept in browser storage.
import { api } from '../api/client';

export const DEMO_ACCOUNTS = {
  agent: { email: 'agent@cxplatform.com', password: 'agent123' },
  admin: { email: 'admin@cxplatform.com', password: 'admin123' },
};

// Resolves to the signed-in user, or null when there is no valid session
export async function getSession() {
  try {
    return await api('/auth/me');
  } catch (err) {
    if (err.status === 401) return null;
    throw err;
  }
}

export const signIn = ({ email, password, role, remember }) =>
  api('/auth/login', { method: 'POST', body: { email, password, role, remember } });

export const signOut = () => api('/auth/logout', { method: 'POST' });

// ── User management (admin only) ───────────────────────────

const userPath = (email) => `/users/${encodeURIComponent(email)}`;

export const listUsers = () => api('/users');

export const createUser = ({ name, email, agentName, password, role = 'agent' }) =>
  api('/users', { method: 'POST', body: { name, email, agentName, password, role } });

export const setUserActive = (email, active) =>
  api(userPath(email), { method: 'PATCH', body: { active } });

export const resetPassword = (email, password) =>
  api(`${userPath(email)}/password`, { method: 'PUT', body: { password } });

export const deleteUser = (email) => api(userPath(email), { method: 'DELETE' });
