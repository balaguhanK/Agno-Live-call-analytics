// Fallback when the backend has no Deepgram key: the browser's own speech
// recognition (Chrome uses Google's service, Edge uses Microsoft's). Each
// finished sentence is posted to the backend.
import { sendSegment } from '../api/stream';

// Chrome ends a recognition session after pauses or about a minute; a fresh
// one is started after this delay. Give up only if sessions keep ending
// without hearing anything.
const RESTART_DELAY_MS = 250;
const MAX_EMPTY_RESTARTS = 8;

const SpeechRecognition = typeof window !== 'undefined'
  ? window.SpeechRecognition || window.webkitSpeechRecognition
  : undefined;

export const browserSupported = () => !!SpeechRecognition;

// Errors that end the call; 'no-speech' and 'aborted' just restart listening
const FATAL_ERRORS = {
  'not-allowed': 'Microphone access was blocked. Allow the microphone for this site in the browser address bar, then start again.',
  'service-not-allowed': 'The browser blocked its speech service. Allow the microphone for this site, then start again.',
  'audio-capture': 'No microphone was found. Connect one and start again.',
  network: "The browser's speech service can't be reached. Check your internet connection.",
  'language-not-supported': 'This browser cannot transcribe the selected language.',
};

/** Same contract as startDeepgram: resolves to { setSpeaker, stop }. */
export async function startBrowser({ callId, language, speaker, secondsSinceStart, onInterim, onSegments, onWarning, onFatal }) {
  let currentSpeaker = speaker;
  let live = true;
  let recognition = null;
  let restartTimer;
  let emptyRestarts = 0;
  let utteranceStart = null;
  let queue = Promise.resolve();

  // Lines are sent one at a time so they're saved in the order spoken
  const queueLine = (text, startSec, endSec, who) => {
    queue = queue.then(async () => {
      try {
        onSegments(await sendSegment(callId, { speaker: who, text, startSec, endSec }));
      } catch (err) {
        onWarning(`A line couldn't be saved: ${err.message}`);
      }
    });
  };

  const listen = () => {
    const rec = new SpeechRecognition();
    rec.lang = language;
    rec.continuous = true;
    rec.interimResults = true;

    rec.onresult = (event) => {
      emptyRestarts = 0;
      let interim = '';
      for (let i = event.resultIndex; i < event.results.length; i++) {
        const result = event.results[i];
        const text = result[0].transcript.trim();
        if (utteranceStart === null) utteranceStart = secondsSinceStart();
        if (result.isFinal) {
          if (text) queueLine(text, utteranceStart, secondsSinceStart(), currentSpeaker);
          utteranceStart = null;
        } else {
          interim += `${text} `;
        }
      }
      onInterim(interim.trim(), currentSpeaker);
    };

    rec.onerror = (event) => {
      if (event.error === 'no-speech' || event.error === 'aborted') return;
      const message = FATAL_ERRORS[event.error];
      if (message) {
        live = false;
        onFatal(message);
      } else {
        onWarning(`Speech recognition hiccup (${event.error}). Still listening.`);
      }
    };

    rec.onend = () => {
      onInterim('');
      utteranceStart = null;
      if (!live || recognition !== rec) return;
      emptyRestarts += 1;
      if (emptyRestarts > MAX_EMPTY_RESTARTS) {
        live = false;
        onFatal('Speech recognition keeps stopping without hearing anything. Check the microphone and start again.');
        return;
      }
      restartTimer = setTimeout(() => {
        if (!live) return;
        try {
          listen();
        } catch (err) {
          live = false;
          onFatal(`Speech recognition couldn't restart: ${err.message}`);
        }
      }, RESTART_DELAY_MS);
    };

    recognition = rec;
    rec.start();
  };

  listen();

  return {
    setSpeaker(value) { currentSpeaker = value; },
    async stop() {
      live = false;
      clearTimeout(restartTimer);
      recognition?.stop();
      await queue; // let lines already spoken finish saving
    },
  };
}
