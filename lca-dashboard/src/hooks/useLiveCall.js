import { useCallback, useEffect, useState } from 'react';
import { getCall, subscribeToCalls } from '../api/calls';

// Adds new segments and replaces updated ones (e.g. an AI suggestion the
// model has finished), keeping the transcript in order.
// The API leaves out an empty transcript, so either side may be missing.
function mergeSegments(transcript = [], segments = []) {
  if (segments.length === 0) return transcript;
  const byId = new Map(transcript.map(s => [s.id, s]));
  segments.forEach(s => byId.set(s.id, s));
  return [...byId.values()].sort((a, b) => a.id - b.id);
}

/**
 * Loads a call with its transcript and keeps it current from live events.
 * Returns { call, error, addSegments, reload }. call is null while loading.
 */
export default function useLiveCall(callId) {
  // Tagged with the call it belongs to, so switching calls never shows the old one
  const [state, setState] = useState({ id: null, call: null, error: '' });

  // Applies f to the loaded call, or starts from the fetched one
  const update = useCallback((id, f) => {
    setState(s => (s.id === id ? { ...s, call: f(s.call), error: '' } : { id, call: f(null), error: '' }));
  }, []);

  const reload = useCallback(() => {
    if (!callId) return Promise.resolve();
    return getCall(callId)
      .then(full => update(callId, c => ({ ...full, transcript: mergeSegments(full.transcript, c?.transcript) })))
      .catch(err => setState({ id: callId, call: null, error: err.message }));
  }, [callId, update]);

  useEffect(() => {
    if (!callId) return undefined;
    let ignore = false;
    getCall(callId)
      .then(full => { if (!ignore) update(callId, c => ({ ...full, transcript: mergeSegments(full.transcript, c?.transcript) })); })
      .catch(err => { if (!ignore) setState({ id: callId, call: null, error: err.message }); });

    const unsubscribe = subscribeToCalls((event) => {
      if (ignore) return;
      if (event.type === 'open') {
        // (Re)connected: fetch whatever was said while the socket was down
        getCall(callId)
          .then(full => { if (!ignore) update(callId, c => ({ ...full, transcript: mergeSegments(full.transcript, c?.transcript) })); })
          .catch(() => {});
        return;
      }
      if (event.callId !== callId) return;
      if (event.type === 'call') {
        if (event.call.status === 'Done') {
          // Post-call analysis scored every line; reload them
          getCall(callId).then(full => { if (!ignore) update(callId, () => ({ ...full, transcript: full.transcript || [] })); }).catch(() => {});
        }
        // Call events don't carry the transcript; keep the one already loaded
        update(callId, c => c && { ...event.call, hasRecording: event.call.hasRecording || c.hasRecording, transcript: c.transcript });
      } else if (event.type === 'segment') {
        update(callId, c => c && { ...c, transcript: mergeSegments(c.transcript, [event.segment]) });
      }
    });
    return () => { ignore = true; unsubscribe(); };
  }, [callId, update]);

  // Lines the page already has (e.g. from the save response) show at once
  const addSegments = useCallback((segments) => {
    if (callId) update(callId, c => c && { ...c, transcript: mergeSegments(c.transcript, segments) });
  }, [callId, update]);

  const current = state.id === callId;
  return { call: current ? state.call : null, error: current ? state.error : '', addSegments, reload };
}
