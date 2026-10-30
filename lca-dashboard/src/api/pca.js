import { api, ApiError } from './client';

// Live translation of transcript lines. language is a code from
// TRANSLATION_LANGUAGES. Resolves to { translations: [...], model }.
export const translateLines = (lines, language) =>
  api('/translate', { method: 'POST', body: { lines, language } });

// Languages the backend can translate into, by code
export const TRANSLATION_LANGUAGES = [
  { code: 'fr', label: 'French' },
  { code: 'es', label: 'Spanish' },
  { code: 'de', label: 'German' },
  { code: 'it', label: 'Italian' },
  { code: 'pt', label: 'Portuguese' },
  { code: 'hi', label: 'Hindi (approximate)' },
  { code: 'ta', label: 'Tamil (approximate)' },
  { code: 'ar', label: 'Arabic' },
  { code: 'zh', label: 'Chinese (Simplified)' },
  { code: 'ja', label: 'Japanese' },
  { code: 'ko', label: 'Korean' },
  { code: 'nl', label: 'Dutch' },
];

// A question about a call, answered from its transcript ("Generative AI query").
// Resolves to { answer, model }.
export const askAboutCall = (callId, question) =>
  api('/assist', { method: 'POST', body: { action: 'query', callId, question } });

// Where the call's recording is served from (same origin, session cookie)
export const recordingUrl = (callId) =>
  `${import.meta.env.VITE_API_BASE_URL || '/api'}/calls/${encodeURIComponent(callId)}/recording`;

// Uploads the browser phone's recording of a call once it has ended.
// Writes are JSON (the backend's CSRF check), so the audio goes as base64.
export async function uploadRecording(callId, blob) {
  const mimeType = blob.type.split(';')[0];
  const dataUrl = await new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(new ApiError("Couldn't read the recording.", 0));
    reader.readAsDataURL(blob);
  });
  const audio = String(dataUrl).split(',')[1] || '';
  return api(`/stream/calls/${encodeURIComponent(callId)}/recording`, {
    method: 'POST',
    body: { audio, mimeType },
  });
}

// Opens a call in the Post-Call Analytics app, in its own browser tab
export function openInPca(callId) {
  const url = `${window.location.origin}${window.location.pathname}#/pca/calls/${encodeURIComponent(callId)}`;
  window.open(url, '_blank', 'noopener');
}
