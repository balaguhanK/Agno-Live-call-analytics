// The agent's browser phone: registers with FreeSWITCH over SIP/WebSocket
// (JsSIP) and handles incoming calls with WebRTC audio.
import JsSIP from 'jssip';

// LAN setup: host candidates are enough, and skipping STUN makes answering instant
const PC_CONFIG = { iceServers: [] };

/**
 * Connects and registers. Callbacks: onStatus('connecting' | 'online' |
 * 'offline' | 'failed', detail) and onIncoming(call). Returns { stop }.
 *
 * A call is { caller, answer(), decline(), hangup(), setMuted(bool),
 * onConnected(fn(localStream, remoteStream)), onEnded(fn(reason)) }.
 */
export function startPhone({ wsUrl, domain, sipUser, password, onStatus, onIncoming }) {
  const ua = new JsSIP.UA({
    sockets: [new JsSIP.WebSocketInterface(wsUrl)],
    uri: `sip:${sipUser}@${domain}`,
    password,
    display_name: sipUser,
    register: true,
    session_timers: false,
    user_agent: 'LCA Dashboard',
  });

  ua.on('connecting', () => onStatus('connecting'));
  ua.on('registered', () => onStatus('online'));
  ua.on('unregistered', () => onStatus('offline'));
  ua.on('disconnected', () => onStatus('connecting', "Can't reach FreeSWITCH; retrying…"));
  ua.on('registrationFailed', (e) => onStatus('failed', e.cause));

  ua.on('newRTCSession', ({ session, originator }) => {
    if (originator !== 'remote') return;
    onIncoming(wrapCall(session));
  });

  ua.start();
  return {
    stop() {
      ua.stop();
    },
  };
}

function trackStream(connection, kind) {
  const list = kind === 'local' ? connection.getSenders() : connection.getReceivers();
  const track = list.map((x) => x.track).find((t) => t && t.kind === 'audio');
  return track ? new MediaStream([track]) : null;
}

function wrapCall(session) {
  const id = session.remote_identity;
  const caller = id.display_name && id.display_name !== id.uri.user
    ? `${id.display_name} (${id.uri.user})`
    : id.uri.user;
  let connected = () => {};
  let ended = () => {};
  let done = false;
  const finish = (reason) => {
    if (done) return;
    done = true;
    ended(reason);
  };

  session.on('confirmed', () => {
    connected(trackStream(session.connection, 'local'), trackStream(session.connection, 'remote'));
  });
  session.on('ended', (e) => finish(e.originator === 'local' ? 'You hung up' : 'Caller hung up'));
  session.on('failed', (e) => finish(e.cause === JsSIP.C.causes.CANCELED ? 'Caller hung up before you answered' : e.cause));

  return {
    caller,
    callerNumber: id.uri.user,
    answer() {
      session.answer({ mediaConstraints: { audio: true, video: false }, pcConfig: PC_CONFIG });
    },
    decline() {
      session.terminate({ status_code: 486, reason_phrase: 'Busy Here' });
    },
    hangup() {
      if (!session.isEnded()) session.terminate();
    },
    setMuted(muted) {
      if (muted) session.mute({ audio: true });
      else session.unmute({ audio: true });
    },
    onConnected(fn) { connected = fn; },
    onEnded(fn) { ended = fn; },
  };
}

// Ring settings: a classic two-tone phone ring, two bursts then a pause
const RING_FREQUENCIES = [440, 480];
const RING_VOLUME = 0.3;
const RING_BURST_SECONDS = 0.8;
const RING_BURST_GAP_SECONDS = 0.4;
const RING_CYCLE_MS = 3000;

// Browsers keep a page silent until the person has interacted with it, so
// one shared audio context is unlocked by the first click or key press
let ringContext;
function audioContext() {
  if (!ringContext) ringContext = new AudioContext();
  return ringContext;
}

/** Call once at start-up: the first click or key press on the page lets the ring be heard. */
export function unlockAudio() {
  const unlock = () => {
    audioContext().resume().catch(() => {});
    window.removeEventListener('pointerdown', unlock);
    window.removeEventListener('keydown', unlock);
  };
  window.addEventListener('pointerdown', unlock);
  window.addEventListener('keydown', unlock);
  return () => {
    window.removeEventListener('pointerdown', unlock);
    window.removeEventListener('keydown', unlock);
  };
}

// A phone ring played while a call is waiting
export function startRingtone() {
  const ctx = audioContext();
  const burst = (offset) => {
    const gain = ctx.createGain();
    const start = ctx.currentTime + offset;
    gain.gain.setValueAtTime(0, start);
    gain.gain.linearRampToValueAtTime(RING_VOLUME, start + 0.03);
    gain.gain.setValueAtTime(RING_VOLUME, start + RING_BURST_SECONDS - 0.05);
    gain.gain.linearRampToValueAtTime(0, start + RING_BURST_SECONDS);
    gain.connect(ctx.destination);
    for (const freq of RING_FREQUENCIES) {
      const osc = ctx.createOscillator();
      osc.frequency.value = freq;
      osc.connect(gain);
      osc.start(start);
      osc.stop(start + RING_BURST_SECONDS);
    }
  };
  const ring = () => {
    burst(0);
    burst(RING_BURST_SECONDS + RING_BURST_GAP_SECONDS);
  };
  ctx.resume().then(ring).catch(() => {});
  const timer = setInterval(ring, RING_CYCLE_MS);
  return () => clearInterval(timer);
}
