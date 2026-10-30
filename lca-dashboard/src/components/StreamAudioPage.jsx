import React, { useEffect, useRef, useState } from 'react';
import PostCallAnalysis from './PostCallAnalysis';
import TranscriptPanel from './TranscriptTimeline';
import SummaryPanel from './AgentAssistPanel';
import { getStreamConfig, startStream, endStream, transcribeTest } from '../api/stream';
import { getCall } from '../api/calls';
import { startDeepgram, deepgramSupported, testMicrophone } from '../asr/deepgramEngine';
import { startBrowser, browserSupported } from '../asr/browserEngine';
import { LANGUAGES } from '../asr/languages';

const ENGINES = {
  deepgram: {
    label: 'Deepgram',
    start: startDeepgram,
    supported: deepgramSupported,
    hint: 'Audio is streamed through the server to Deepgram. Card numbers, emails and phone numbers are removed before anything is saved.',
  },
  browser: {
    label: 'Browser speech recognition',
    start: startBrowser,
    supported: browserSupported,
    hint: "Deepgram isn't configured on the server, so the browser's speech service is used. Card numbers, emails and phone numbers are removed before anything is saved.",
  },
};

// Only the agent's microphone is transcribed for now; customer audio comes later
const SPEAKER = 'AGENT';

// Below this level (0 to 1) the microphone counts as silent
const SILENCE_LEVEL = 0.02;
// Warn after this long without hearing anything
const SILENCE_WARN_MS = 6000;
const MIC_TEST_SECONDS = 4;

// Monotonic clock for call timing, unaffected by system clock changes
const nowMs = () => performance.now();

const formatElapsed = (sec) => {
  const s = Math.floor(sec);
  return `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
};

// Stream Audio: the agent's microphone becomes a live call. The backend
// redacts personal data, scores sentiment, adds live-assist tips and saves it.
export default function StreamAudioPage({ user, onOpenCall }) {
  const [engineName, setEngineName] = useState(null); // null while loading
  const [configError, setConfigError] = useState('');
  const [language, setLanguage] = useState(LANGUAGES[0].browser);
  const [callerPhone, setCallerPhone] = useState('');
  const [phase, setPhase] = useState('idle'); // idle | starting | live | ending | ended
  const [call, setCall] = useState(null);
  const [transcript, setTranscript] = useState([]);
  const [partial, setPartial] = useState('');
  const [elapsed, setElapsed] = useState(0);
  const [error, setError] = useState('');
  const [warning, setWarning] = useState('');
  const [mics, setMics] = useState([]);
  const [deviceId, setDeviceId] = useState('');
  const [micLabel, setMicLabel] = useState('');
  const [level, setLevel] = useState(0);
  const [micTest, setMicTest] = useState({ state: 'idle' }); // idle | recording | ready | failed

  const sessionRef = useRef(null);
  const callIdRef = useRef(null);
  const startedAtRef = useRef(0);
  const liveRef = useRef(false);
  const lastSoundRef = useRef(0);
  const silenceWarnedRef = useRef(false);

  const engine = engineName ? ENGINES[engineName] : null;
  const secondsSinceStart = () => (nowMs() - startedAtRef.current) / 1000;

  // Ask the backend which engine to use
  useEffect(() => {
    getStreamConfig()
      .then(cfg => setEngineName(cfg.engine))
      .catch(err => setConfigError(err.message));
  }, []);

  // Microphones to choose from; names appear once mic access is granted
  const loadMics = () => {
    navigator.mediaDevices?.enumerateDevices()
      .then(devices => setMics(devices.filter(d => d.kind === 'audioinput')))
      .catch(() => setMics([]));
  };
  useEffect(() => {
    loadMics();
    navigator.mediaDevices?.addEventListener('devicechange', loadMics);
    return () => navigator.mediaDevices?.removeEventListener('devicechange', loadMics);
  }, []);

  const onLevel = (value) => {
    setLevel(value);
    if (value >= SILENCE_LEVEL) {
      lastSoundRef.current = nowMs();
      if (silenceWarnedRef.current) {
        silenceWarnedRef.current = false;
        setWarning('');
      }
    }
  };

  // Elapsed timer while live; also notices a silent microphone
  useEffect(() => {
    if (phase !== 'live') return;
    const timer = setInterval(() => {
      setElapsed(secondsSinceStart());
      if (engineName === 'deepgram' && !silenceWarnedRef.current && nowMs() - lastSoundRef.current > SILENCE_WARN_MS) {
        silenceWarnedRef.current = true;
        setWarning(`We aren't hearing anything from "${micLabel}". Check it isn't muted and the volume is up, or choose another microphone.`);
      }
    }, 1000);
    return () => clearInterval(timer);
  }, [phase, engineName, micLabel]);

  // Leaving the page mid-call stops listening and ends the call
  useEffect(() => () => {
    if (!liveRef.current) return;
    liveRef.current = false;
    sessionRef.current?.stop().catch(() => {});
    endStream(callIdRef.current, secondsSinceStart()).catch(() => { /* best effort */ });
  }, []);

  const runMicTest = async () => {
    if (micTest.url) URL.revokeObjectURL(micTest.url);
    setMicTest({ state: 'recording' });
    try {
      const { blob, url, description } = await testMicrophone({ deviceId, seconds: MIC_TEST_SECONDS });
      setMicTest({ state: 'ready', url, description, heard: { state: 'loading' } });
      loadMics();
      const lang = LANGUAGES.find(l => l.browser === language);
      try {
        const res = await transcribeTest(blob, lang.deepgram);
        setMicTest(t => ({ ...t, heard: { state: 'done', ...res } }));
      } catch (err) {
        setMicTest(t => ({ ...t, heard: { state: 'failed', message: err.message } }));
      }
    } catch (err) {
      setMicTest({ state: 'failed', message: err.message });
    }
  };

  const stop = async () => {
    if (!liveRef.current) return;
    liveRef.current = false;
    setPhase('ending');
    const duration = secondsSinceStart();
    try {
      await sessionRef.current?.stop(); // waits for the last words to be saved
      await endStream(callIdRef.current, duration);
      // Post-call analysis has now scored every line; reload them
      const analysed = await getCall(callIdRef.current);
      setCall(analysed);
      setTranscript(analysed.transcript);
    } catch (err) {
      setError(`The call couldn't be ended: ${err.message}`);
    }
    sessionRef.current = null;
    setPartial('');
    setLevel(0);
    setPhase('ended');
  };

  const start = async () => {
    setError('');
    setWarning('');
    setTranscript([]);
    setCall(null);
    setElapsed(0);
    setPhase('starting');
    let created;
    try {
      created = await startStream({ callerPhoneNumber: callerPhone.trim() });
      callIdRef.current = created.id;
      setCall(created);
      startedAtRef.current = nowMs();
      lastSoundRef.current = nowMs();
      silenceWarnedRef.current = false;
      liveRef.current = true;
      const lang = LANGUAGES.find(l => l.browser === language);
      sessionRef.current = await engine.start({
        callId: created.id,
        language: engineName === 'deepgram' ? lang.deepgram : lang.browser,
        speaker: SPEAKER,
        deviceId,
        secondsSinceStart,
        onLevel,
        onInterim: (text) => setPartial(text),
        onSegments: (res) => {
          setTranscript(t => [...t, ...res.segments]);
          setCall(res.call);
          setWarning('');
        },
        onWarning: setWarning,
        onFatal: (message) => { setError(message); stop(); },
      });
      setMicLabel(sessionRef.current.micLabel || '');
      loadMics(); // device names are available now
      setPhase('live');
    } catch (err) {
      setError(err.message);
      liveRef.current = false;
      // The call was created but listening never started; close it
      if (created) endStream(created.id, 0).catch(() => {});
      setPhase('idle');
    }
  };

  const live = phase === 'live';
  const busy = phase === 'starting' || phase === 'ending';

  const header = (
    <div className="breadcrumb">
      <a>Call Analytics</a>
      <span className="breadcrumb-sep">›</span>
      <span>Stream Audio</span>
    </div>
  );

  const notice = (title, text) => (
    <>
      {header}
      <div className="cs-container">
        <div className="empty-state" role="alert">
          <strong>{title}</strong>
          <span>{text}</span>
        </div>
      </div>
    </>
  );

  if (configError) return notice("Couldn't load transcription settings", configError);
  if (!engine) {
    return (
      <>
        {header}
        <div className="cs-container">
          <div className="empty-state" role="status">
            <span className="skeleton-bar" style={{ width: '240px' }} />
            <span>Loading…</span>
          </div>
        </div>
      </>
    );
  }
  if (!engine.supported()) {
    return notice(
      "Live transcription isn't available in this browser",
      engineName === 'deepgram'
        ? "This browser can't record microphone audio. Use a recent Chrome, Edge or Firefox."
        : 'Open the dashboard in Google Chrome or Microsoft Edge, or ask an admin to configure Deepgram.',
    );
  }
  if (!user.agentName) {
    return notice(
      'An agent ID is needed to stream calls',
      'Calls are saved under your agent ID. Ask an admin to set one on the Users page, or sign in as an agent.',
    );
  }

  return (
    <>
      {header}

      {/* Setup and controls */}
      <div className="cs-container">
        <div className="cs-container-header">
          <span className="cs-container-title">Live transcription</span>
          <span className="engine-badge" title="Speech-to-text engine">{engine.label}</span>
        </div>
        <div className="cs-container-body">
          <div className="stream-form">
            <label>
              Language
              <select value={language} onChange={e => setLanguage(e.target.value)} disabled={live || busy}>
                {LANGUAGES.map(l => <option key={l.browser} value={l.browser}>{l.label}</option>)}
              </select>
            </label>
            <label>
              Caller phone number (optional)
              <input
                value={callerPhone}
                onChange={e => setCallerPhone(e.target.value)}
                placeholder="+1 703 555 0100"
                maxLength={32}
                disabled={live || busy}
              />
            </label>
            {engineName === 'deepgram' && (
              <label>
                Microphone
                <select value={deviceId} onChange={e => setDeviceId(e.target.value)} disabled={live || busy}>
                  <option value="">System default</option>
                  {mics.filter(m => m.deviceId && m.deviceId !== 'default').map((m, i) => (
                    <option key={m.deviceId} value={m.deviceId}>{m.label || `Microphone ${i + 1}`}</option>
                  ))}
                </select>
              </label>
            )}
          </div>

          <div className="stream-actions">
            {live || phase === 'ending' ? (
              <button className="btn btn-primary stream-stop" onClick={stop} disabled={busy}>
                {phase === 'ending' ? 'Ending call…' : '■ Stop and end call'}
              </button>
            ) : (
              <button className="btn btn-primary" onClick={start} disabled={busy}>
                {phase === 'starting' ? 'Starting…' : '● Start transcribing'}
              </button>
            )}
            {live && (
              <span className="stream-status" role="status">
                <span className="live-dot" /> Listening · {formatElapsed(elapsed)}
              </span>
            )}
            {live && engineName === 'deepgram' && (
              <span className="mic-level" title={micLabel}>
                <span
                  className="mic-meter"
                  role="meter"
                  aria-label={`Input level for ${micLabel}`}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={Math.round(level * 100)}
                >
                  <span style={{ width: `${Math.round(level * 100)}%` }} />
                </span>
                <span className="mic-name">{micLabel}</span>
              </span>
            )}
            {phase === 'ended' && call && (
              <button className="btn btn-normal" onClick={() => onOpenCall(call.id)}>Open in Calls</button>
            )}
            {engineName === 'deepgram' && !live && (
              <button className="btn btn-normal" onClick={runMicTest} disabled={busy || micTest.state === 'recording'}>
                {micTest.state === 'recording' ? `Recording ${MIC_TEST_SECONDS} s… speak now` : 'Test microphone'}
              </button>
            )}
          </div>

          {micTest.state === 'ready' && (
            <div className="mic-test" role="status">
              <span>Play this back. It's exactly what is sent for transcription:</span>
              <audio controls src={micTest.url} />
              <span className="mic-name">{micTest.description}</span>
              {micTest.heard?.state === 'loading' && <span>Asking Deepgram what it hears…</span>}
              {micTest.heard?.state === 'done' && (
                <span>
                  <strong>Deepgram heard:</strong>{' '}
                  {micTest.heard.transcript ? `“${micTest.heard.transcript}”` : '(nothing)'}{' '}
                  <span className="mic-name">
                    · decoded {micTest.heard.duration?.toFixed(1)} s, {micTest.heard.channels} channel(s)
                    {micTest.heard.transcript ? `, confidence ${Math.round(micTest.heard.confidence * 100)}%` : ''}
                  </span>
                </span>
              )}
              {micTest.heard?.state === 'failed' && <span>Deepgram test failed: {micTest.heard.message}</span>}
            </div>
          )}
          {micTest.state === 'failed' && (
            <div className="users-message error" role="alert">{micTest.message}</div>
          )}
          <p className="stream-hint">
            {engine.hint} Sentiment and suggestions work best in English.
          </p>

          {error && (
            <div className="users-message error" role="alert">
              {error}
              <button onClick={() => setError('')} aria-label="Dismiss">✕</button>
            </div>
          )}
          {warning && (
            <div className="users-message error" role="alert">
              {warning}
              <button onClick={() => setWarning('')} aria-label="Dismiss">✕</button>
            </div>
          )}
        </div>
      </div>

      {call && (
        <>
          <div aria-live="polite">
            <TranscriptPanel
              transcript={transcript}
              isLive={live}
              partialText={partial}
              partialSpeaker={SPEAKER}
            />
          </div>

          {phase === 'ended' && <PostCallAnalysis call={call} />}
          {phase === 'ended' && <SummaryPanel summary={call.summary} />}
        </>
      )}
    </>
  );
}
