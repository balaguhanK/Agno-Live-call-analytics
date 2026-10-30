import { api } from './client';

// Live transcription: the browser recognizes speech, the backend redacts,
// analyzes and stores it as a call.

// { engine: 'deepgram' | 'browser' }: which speech-to-text the backend offers
export const getStreamConfig = () => api('/stream/config');

export const startStream = ({ callerPhoneNumber }) =>
  api('/stream/calls', { method: 'POST', body: { callerPhoneNumber } });

// Resolves to { segments, call }: the saved line (redacted) plus any
// live-assist suggestions, and the call with updated sentiment.
export const sendSegment = (callId, { speaker, text, startSec, endSec }) =>
  api(`/stream/calls/${encodeURIComponent(callId)}/segments`, {
    method: 'POST',
    body: { speaker, text, startSec, endSec },
  });

// Transcribes a short mic-test clip with Deepgram's file API.
// Resolves to { transcript, confidence, duration, channels }.
export async function transcribeTest(blob, language) {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let binary = '';
  bytes.forEach((b) => { binary += String.fromCharCode(b); });
  return api('/stream/test', {
    method: 'POST',
    body: { audio: btoa(binary), mimeType: blob.type.split(';')[0], language },
  });
}

// Ends the call; resolves to the call with its summary and categories.
export const endStream = (callId, durationSec) =>
  api(`/stream/calls/${encodeURIComponent(callId)}/end`, { method: 'POST', body: { durationSec } });
