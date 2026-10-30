import React, { useEffect, useRef, useState } from 'react';
import { getPhoneConfig } from '../api/phone';
import { getStreamConfig, startStream, endStream } from '../api/stream';
import { startDeepgram, deepgramSupported } from '../asr/deepgramEngine';
import { LANGUAGES } from '../asr/languages';
import { startPhone, startRingtone, unlockAudio } from './sipPhone';
import { PhoneLineContext } from './phoneLineContext';
import useLiveCall from '../hooks/useLiveCall';

// Monotonic clock for call timing
const nowMs = () => performance.now();

const NO_PARTIAL = { text: '', speaker: 'CUSTOMER' };

// Owns the agent's browser phone for the whole signed-in session: it goes
// online as soon as the agent signs in, so FreeSWITCH can ring them on any
// page (the Calls page shows the incoming call). Once answered, both voices
// are transcribed live into a new call; when it ends, post-call analysis runs.
export default function PhoneProvider({ children }) {
  const [config, setConfig] = useState(null); // null while loading
  const [configError, setConfigError] = useState('');
  const [status, setStatus] = useState('offline');
  const [statusDetail, setStatusDetail] = useState('');
  const [language, setLanguage] = useState(LANGUAGES[0].browser);
  const [phase, setPhase] = useState('idle');
  const [caller, setCaller] = useState('');
  const [muted, setMuted] = useState(false);
  const [callId, setCallId] = useState(null);
  const [partial, setPartial] = useState(NO_PARTIAL);
  const [elapsed, setElapsed] = useState(0);
  const [error, setError] = useState('');
  const [warning, setWarning] = useState('');
  const { call, addSegments, reload } = useLiveCall(callId);

  // SIP handlers are registered when the phone goes online and live for the
  // whole session, so they read everything that changes through refs
  const reloadRef = useRef(reload);
  const addSegmentsRef = useRef(addSegments);
  const languageRef = useRef(language);
  const deepgramRef = useRef(false);
  useEffect(() => { reloadRef.current = reload; }, [reload]);
  useEffect(() => { addSegmentsRef.current = addSegments; }, [addSegments]);
  useEffect(() => { languageRef.current = language; }, [language]);
  // The first click or key press lets the ring be heard
  useEffect(() => unlockAudio(), []);

  const phoneRef = useRef(null);
  const activeRef = useRef(null); // the call being handled
  const enginesRef = useRef([]);
  const callIdRef = useRef(null);
  const startedAtRef = useRef(0);
  const ringRef = useRef(null);
  const remoteAudioRef = useRef(null);

  const secondsSinceStart = () => (nowMs() - startedAtRef.current) / 1000;

  const stopRing = () => {
    ringRef.current?.();
    ringRef.current = null;
  };

  const finishCall = async (reason) => {
    stopRing();
    if (remoteAudioRef.current) remoteAudioRef.current.srcObject = null;
    const engines = enginesRef.current;
    enginesRef.current = [];
    activeRef.current = null;
    const id = callIdRef.current;
    if (!id) {
      setPhase('idle');
      if (reason) setWarning(`Missed call: ${reason}`);
      return;
    }
    setPhase('ending');
    const duration = secondsSinceStart();
    await Promise.all(engines.map(e => e.stop().catch(() => {})));
    try {
      // Post-call analysis scores every line; the summary and the model's
      // insights follow as live updates
      await endStream(id, duration);
      await reloadRef.current();
    } catch (err) {
      setError(`The call couldn't be closed: ${err.message}`);
    }
    callIdRef.current = null;
    setPartial(NO_PARTIAL);
    setPhase('ended');
    if (reason) setWarning(reason);
  };

  // Starts live transcription of both sides once audio is flowing
  const startTranscription = async (local, remote, from) => {
    const created = await startStream({ callerPhoneNumber: from });
    callIdRef.current = created.id;
    startedAtRef.current = nowMs();
    setCallId(created.id);
    if (!deepgramRef.current) {
      setWarning("Live transcription needs Deepgram on the server; this call isn't being transcribed.");
      return;
    }
    const lang = LANGUAGES.find(l => l.browser === languageRef.current).deepgram;
    const common = {
      callId: created.id,
      language: lang,
      secondsSinceStart,
      onSegments: (res) => addSegmentsRef.current(res.segments),
      onWarning: setWarning,
      onFatal: (message) => setWarning(`Transcription stopped: ${message}`),
    };
    const sides = [
      { stream: remote, speaker: 'CUSTOMER' },
      { stream: local, speaker: 'AGENT' },
    ].filter(s => s.stream);
    enginesRef.current = await Promise.all(sides.map(s => startDeepgram({
      ...common,
      speaker: s.speaker,
      mediaStream: s.stream,
      onInterim: (text) => setPartial(p => (text || p.speaker === s.speaker ? { text, speaker: s.speaker } : p)),
    })));
  };

  const handleIncoming = (incomingCall) => {
    if (activeRef.current) {
      incomingCall.decline(); // already on a call
      return;
    }
    activeRef.current = incomingCall;
    setCaller(incomingCall.caller);
    setPhase('ringing');
    setError('');
    setWarning('');
    ringRef.current = startRingtone();

    incomingCall.onConnected((local, remote) => {
      stopRing();
      if (remoteAudioRef.current && remote) remoteAudioRef.current.srcObject = remote;
      setCallId(null);
      setMuted(false);
      setElapsed(0);
      setPhase('active');
      startTranscription(local, remote, incomingCall.callerNumber)
        .catch(err => setWarning(`Transcription couldn't start: ${err.message}`));
    });
    incomingCall.onEnded((reason) => finishCall(reason));
  };

  const goOnline = (cfg = config) => {
    if (!cfg?.enabled || phoneRef.current) return;
    setError('');
    setStatus('connecting');
    phoneRef.current = startPhone({
      ...cfg,
      onStatus: (s, detail = '') => { setStatus(s); setStatusDetail(detail); },
      onIncoming: handleIncoming,
    });
  };

  const goOffline = () => {
    phoneRef.current?.stop();
    phoneRef.current = null;
    setStatus('offline');
    setStatusDetail('');
  };

  // Load the agent's line and go online straight away
  useEffect(() => {
    let ignore = false;
    Promise.all([getPhoneConfig(), getStreamConfig()])
      .then(([phone, stream]) => {
        if (ignore) return;
        deepgramRef.current = stream.engine === 'deepgram' && deepgramSupported();
        setConfig(phone);
        goOnline(phone);
      })
      .catch(err => { if (!ignore) setConfigError(err.message); });
    // goOnline only reads refs and setters, so this runs once per sign-in
    return () => { ignore = true; };
  }, []);

  // Signing out hangs up and signs the phone out
  useEffect(() => () => {
    stopRing();
    activeRef.current?.hangup();
    phoneRef.current?.stop();
    phoneRef.current = null;
  }, []);

  useEffect(() => {
    if (phase !== 'active') return undefined;
    const timer = setInterval(() => setElapsed(secondsSinceStart()), 1000);
    return () => clearInterval(timer);
  }, [phase]);

  // Show who is calling in the tab title while the phone rings
  useEffect(() => {
    if (phase !== 'ringing') return undefined;
    const previous = document.title;
    document.title = `📞 Incoming call: ${caller}`;
    return () => { document.title = previous; };
  }, [phase, caller]);

  const answer = () => {
    stopRing();
    try {
      activeRef.current?.answer();
    } catch (err) {
      setError(`Couldn't answer: ${err.message}`);
    }
  };

  const toggleMute = () => {
    activeRef.current?.setMuted(!muted);
    setMuted(m => !m);
  };

  // Clears a finished call so the line shows as free again
  const dismiss = () => {
    if (phase !== 'ended') return;
    setPhase('idle');
    setCallId(null);
    setWarning('');
  };

  const value = {
    config, configError, status, statusDetail, phase, caller, muted, elapsed,
    call, partial, error, warning, language,
    busy: phase === 'ringing' || phase === 'active' || phase === 'ending',
    onCall: phase === 'active' || phase === 'ending' || phase === 'ended',
    setLanguage,
    goOnline: () => goOnline(),
    goOffline,
    answer,
    decline: () => activeRef.current?.decline(),
    hangup: () => activeRef.current?.hangup(),
    toggleMute,
    dismiss,
    clearError: () => setError(''),
    clearWarning: () => setWarning(''),
  };

  return (
    <PhoneLineContext.Provider value={value}>
      <audio ref={remoteAudioRef} autoPlay aria-hidden="true" />
      {children}
    </PhoneLineContext.Provider>
  );
}
