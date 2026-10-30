// Streams the microphone to the backend, which relays it to Deepgram and
// sends back live text and saved lines. The Deepgram key stays on the server.
//
// Audio is sent as raw 16-bit mono PCM, the format Deepgram recommends for
// live streams, so nothing depends on the browser's compressed recorder.

const PCM_SAMPLE_RATE = 16000;
// Send audio in frames of this length
const FRAME_MS = 100;
const MAX_RECONNECTS = 3;
const RECONNECT_BASE_MS = 500;
// How long to wait for the last words after Stop
const STOP_TIMEOUT_MS = 6000;
// The backend closes with this code when Deepgram refuses the stream
const CLOSE_SERVER_ERROR = 1011;
// Scales RMS loudness into a 0..1 meter value
const LEVEL_GAIN = 4;
// Quiet microphones are boosted toward this RMS loudness (about -26 dBFS,
// normal speech level), by at most MAX_BOOST. Louder would clip and distort.
const TARGET_RMS = 0.05;
const MAX_BOOST = 10;
// Samples above this level are rounded off smoothly instead of clipped
const LIMIT_KNEE = 0.5;
// Ignore frames quieter than this when adjusting the boost (background hiss)
const SPEECH_RMS_FLOOR = 0.003;
// How quickly the boost follows the voice (0..1 per frame)
const BOOST_SMOOTHING = 0.2;

// Used only by the microphone test, which plays a recording back
const MIME_TYPES = ['audio/webm;codecs=opus', 'audio/ogg;codecs=opus', 'audio/webm'];
const TEST_CHUNK_MS = 250;

// Copies each block of microphone samples to the main thread
const CAPTURE_WORKLET = `
class PcmCapture extends AudioWorkletProcessor {
  process(inputs) {
    const channel = inputs[0] && inputs[0][0];
    if (channel) this.port.postMessage(channel.slice(0));
    return true;
  }
}
registerProcessor('pcm-capture', PcmCapture);
`;

export const deepgramSupported = () =>
  typeof window !== 'undefined' && !!navigator.mediaDevices?.getUserMedia && typeof AudioWorkletNode !== 'undefined';

const MIC_ERRORS = {
  NotAllowedError: 'Microphone access was blocked. Allow the microphone for this site in the browser address bar, then start again.',
  NotFoundError: 'No microphone was found. Connect one and start again.',
  NotReadableError: 'The microphone is in use by another app. Close it and start again.',
};

// Opens the microphone with the settings used for streaming
async function openMic(deviceId) {
  try {
    return await navigator.mediaDevices.getUserMedia({
      audio: {
        deviceId: deviceId ? { exact: deviceId } : undefined,
        channelCount: 1,
        // The browser's filters can strip speech on some laptop mics, and
        // its gain control stacked with ours overdrives the audio;
        // Deepgram handles noise itself and capturePcm sets the level
        echoCancellation: false,
        noiseSuppression: false,
        autoGainControl: false,
      },
    });
  } catch (err) {
    throw new Error(MIC_ERRORS[err.name] || `Couldn't open the microphone: ${err.message}`);
  }
}

// e.g. "Microphone Array (Realtek) · 1 channel · 48000 Hz"
export function describeMic(stream) {
  const track = stream.getAudioTracks()[0];
  if (!track) return 'No microphone';
  const { channelCount, sampleRate } = track.getSettings();
  const parts = [track.label || 'Microphone'];
  if (channelCount) parts.push(`${channelCount} channel${channelCount > 1 ? 's' : ''}`);
  if (sampleRate) parts.push(`${sampleRate} Hz`);
  return parts.join(' · ');
}

// Builds the audio graph, at 16 kHz where the browser allows resampling
// (Firefox may not, so fall back to the device rate).
async function createCaptureContext(mic) {
  for (const options of [{ sampleRate: PCM_SAMPLE_RATE }, {}]) {
    const ctx = new AudioContext(options);
    try {
      const source = ctx.createMediaStreamSource(mic);
      return { ctx, source };
    } catch {
      await ctx.close();
    }
  }
  throw new Error("This browser can't process microphone audio.");
}

// Passes quiet samples unchanged and bends louder ones smoothly toward ±1,
// so peaks never hard-clip (hard clipping garbles words)
function softLimit(x) {
  const a = Math.abs(x);
  if (a <= LIMIT_KNEE) return x;
  const room = 1 - LIMIT_KNEE;
  return Math.sign(x) * (LIMIT_KNEE + room * Math.tanh((a - LIMIT_KNEE) / room));
}

/**
 * Captures the microphone as 16-bit PCM frames. Calls onFrame(ArrayBuffer)
 * every FRAME_MS and onLevel(0..1). Resolves to { sampleRate, stop }.
 */
async function capturePcm(mic, onFrame, onLevel) {
  const { ctx, source } = await createCaptureContext(mic);
  const workletUrl = URL.createObjectURL(new Blob([CAPTURE_WORKLET], { type: 'application/javascript' }));
  await ctx.audioWorklet.addModule(workletUrl);
  URL.revokeObjectURL(workletUrl);

  const node = new AudioWorkletNode(ctx, 'pcm-capture');
  // A silent output keeps the node running without playing the mic aloud
  const mute = ctx.createGain();
  mute.gain.value = 0;
  source.connect(node);
  node.connect(mute).connect(ctx.destination);

  const frameSamples = Math.round(ctx.sampleRate * FRAME_MS / 1000);
  let blocks = [];
  let count = 0;
  let boost = 1;
  node.port.onmessage = ({ data }) => {
    blocks.push(data);
    count += data.length;
    if (count < frameSamples) return;

    const samples = new Float32Array(count);
    let o = 0;
    let sumSquares = 0;
    for (const block of blocks) {
      for (let i = 0; i < block.length; i++) {
        samples[o++] = block[i];
        sumSquares += block[i] * block[i];
      }
    }
    blocks = [];
    count = 0;

    // Raise quiet speech toward TARGET_RMS; leave silence and loud audio alone
    const rms = Math.sqrt(sumSquares / samples.length);
    if (rms > SPEECH_RMS_FLOOR) {
      const wanted = Math.max(1, Math.min(MAX_BOOST, TARGET_RMS / rms));
      boost += (wanted - boost) * BOOST_SMOOTHING;
    }
    const pcm = new Int16Array(samples.length);
    for (let i = 0; i < samples.length; i++) {
      const s = softLimit(samples[i] * boost);
      pcm[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
    }
    onLevel?.(Math.min(1, rms * boost * LEVEL_GAIN));
    onFrame(pcm.buffer);
  };

  return {
    sampleRate: ctx.sampleRate,
    stop() {
      node.port.onmessage = null;
      source.disconnect();
      node.disconnect();
      ctx.close();
    },
  };
}

/**
 * Records a few seconds from the microphone for the user to play back.
 * Resolves to { blob, url, description }.
 */
export async function testMicrophone({ deviceId, seconds }) {
  const mic = await openMic(deviceId);
  const description = describeMic(mic);
  const mimeType = MIME_TYPES.find((t) => MediaRecorder.isTypeSupported(t));
  const chunks = [];
  const recorder = new MediaRecorder(mic, { mimeType });
  recorder.ondataavailable = (e) => { if (e.data.size > 0) chunks.push(e.data); };
  await new Promise((resolve) => {
    recorder.onstop = resolve;
    recorder.start(TEST_CHUNK_MS);
    setTimeout(() => recorder.stop(), seconds * 1000);
  });
  mic.getTracks().forEach((t) => t.stop());
  const blob = new Blob(chunks, { type: mimeType });
  return { blob, url: URL.createObjectURL(blob), description };
}

/**
 * Starts streaming. Resolves to { setSpeaker, stop, micLabel } once audio is flowing.
 * Pass mediaStream to transcribe audio you already have (e.g. a phone call's
 * remote side); it is left running on stop. Otherwise the microphone is opened.
 * Callbacks: onInterim(text, speaker), onSegments({ segments, call }), onLevel(0..1),
 * onWarning(message), onFatal(message) — after onFatal the caller should stop().
 */
export async function startDeepgram({ callId, language, speaker, deviceId, mediaStream, secondsSinceStart, onInterim, onSegments, onLevel, onWarning, onFatal }) {
  const ownsMic = !mediaStream;
  const mic = mediaStream || await openMic(deviceId);
  const releaseMic = () => { if (ownsMic) mic.getTracks().forEach((t) => t.stop()); };
  let ws;
  let currentSpeaker = speaker;
  let stopping = false;
  let reconnects = 0;
  let lastError = '';
  let onClosed = () => {};

  // Raw PCM has no header, so frames simply go to whichever socket is open
  let capture;
  try {
    capture = await capturePcm(mic, (frame) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(frame);
    }, onLevel);
  } catch (err) {
    releaseMic();
    throw err;
  }

  const connect = () => {
    const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const params = new URLSearchParams({
      lang: language,
      speaker: currentSpeaker,
      offset: secondsSinceStart().toFixed(2),
      encoding: 'linear16',
      sampleRate: String(capture.sampleRate),
    });
    const sock = new WebSocket(`${scheme}://${window.location.host}/ws/stream/${encodeURIComponent(callId)}?${params}`);
    sock.binaryType = 'arraybuffer';
    ws = sock;

    sock.onmessage = (msg) => {
      let m;
      try { m = JSON.parse(msg.data); } catch { return; }
      if (m.type === 'interim') onInterim(m.text, m.speaker);
      else if (m.type === 'segments') { reconnects = 0; onInterim('', m.speaker); onSegments(m.result); }
      else if (m.type === 'error') { lastError = m.message; onWarning(m.message); }
      else if (m.type === 'closed') onClosed();
    };

    sock.onclose = (event) => {
      onInterim('');
      if (stopping) { onClosed(); return; }
      if (event.code === CLOSE_SERVER_ERROR) {
        onFatal(lastError || 'The speech-to-text service refused the stream.');
        return;
      }
      if (reconnects >= MAX_RECONNECTS) {
        onFatal('Lost the connection to the speech-to-text service.');
        return;
      }
      reconnects += 1;
      onWarning('Connection to speech-to-text dropped. Reconnecting…');
      setTimeout(() => { if (!stopping) connect(); }, RECONNECT_BASE_MS * reconnects);
    };
  };

  connect();

  return {
    micLabel: describeMic(mic),

    setSpeaker(value) {
      currentSpeaker = value;
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'speaker', value }));
    },

    // Waits for the final words, then releases the microphone
    async stop() {
      stopping = true;
      capture.stop();
      const closed = new Promise((resolve) => { onClosed = resolve; });
      const sock = ws;
      if (sock?.readyState === WebSocket.OPEN) {
        sock.send(JSON.stringify({ type: 'stop' }));
        await Promise.race([closed, new Promise((r) => setTimeout(r, STOP_TIMEOUT_MS))]);
      }
      sock?.close();
      releaseMic();
    },
  };
}
