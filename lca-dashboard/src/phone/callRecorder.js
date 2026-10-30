// Records both sides of a browser phone call into one stereo file: the
// agent on the left channel and the customer on the right, so post-call
// analytics can chart each speaker's loudness.

// Opus at this rate keeps a 2-minute call well under a megabyte
const AUDIO_BITS_PER_SECOND = 48000;
const PREFERRED_TYPES = ['audio/webm;codecs=opus', 'audio/ogg;codecs=opus', 'audio/webm', 'audio/mp4'];
export const AGENT_CHANNEL = 0;
export const CUSTOMER_CHANNEL = 1;

export function recordingSupported() {
  return typeof window !== 'undefined' && 'MediaRecorder' in window && 'AudioContext' in window;
}

/**
 * Starts recording. Either stream may be null (that channel stays silent).
 * Returns { stop(): Promise<Blob | null> }; null when nothing was recorded.
 */
export function startCallRecording({ agentStream, customerStream }) {
  const ctx = new AudioContext();
  const merger = ctx.createChannelMerger(2);
  const sources = [];
  [[agentStream, AGENT_CHANNEL], [customerStream, CUSTOMER_CHANNEL]].forEach(([stream, channel]) => {
    if (!stream || stream.getAudioTracks().length === 0) return;
    const src = ctx.createMediaStreamSource(stream);
    src.connect(merger, 0, channel);
    sources.push(src);
  });
  const dest = ctx.createMediaStreamDestination();
  dest.channelCount = 2;
  merger.connect(dest);

  const mimeType = PREFERRED_TYPES.find(t => MediaRecorder.isTypeSupported(t)) || '';
  const recorder = new MediaRecorder(dest.stream, { mimeType, audioBitsPerSecond: AUDIO_BITS_PER_SECOND });
  const chunks = [];
  recorder.ondataavailable = (e) => { if (e.data.size > 0) chunks.push(e.data); };
  recorder.start();

  return {
    stop() {
      return new Promise((resolve) => {
        const finish = () => {
          sources.forEach(s => s.disconnect());
          ctx.close().catch(() => {});
          resolve(chunks.length ? new Blob(chunks, { type: recorder.mimeType || mimeType || 'audio/webm' }) : null);
        };
        if (recorder.state === 'inactive') { finish(); return; }
        recorder.onstop = finish;
        recorder.stop();
      });
    },
  };
}
