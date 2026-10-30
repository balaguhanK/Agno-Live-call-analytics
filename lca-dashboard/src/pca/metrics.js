// Post-call analytics worked out in the browser from a call's transcript and
// recording: sentiment per quarter, speaker time, interruptions, loudness.

export const QUARTERS = 4;
// Below this level (dBFS) a second counts as silence
export const SILENCE_DBFS = -50;
// Levels are shown on a 0-100 scale: dBFS + this offset, clipped
const LEVEL_OFFSET = 100;
// A channel this many dB louder than the other is the one speaking
const DOMINANCE_DB = 6;

// "mm:ss.s" (segment times) → seconds
export function parseClock(t = '') {
  const [m, s] = String(t).split(':');
  const sec = Number(m) * 60 + Number(s);
  return Number.isFinite(sec) ? sec : 0;
}

// "hh:mm:ss" (call duration) → seconds
export function parseDuration(d = '') {
  const parts = String(d).split(':').map(Number);
  if (parts.some(n => !Number.isFinite(n))) return 0;
  return parts.reduce((total, n) => total * 60 + n, 0);
}

const spoken = (transcript) => transcript.filter(s => s.speaker === 'AGENT' || s.speaker === 'CUSTOMER');

// Length of the call: its recorded duration, or the end of the last line
export function callSeconds(call) {
  const lastEnd = Math.max(0, ...spoken(call.transcript || []).map(s => parseClock(s.timeEnd)));
  return Math.max(parseDuration(call.duration), lastEnd);
}

/**
 * Average sentiment of each speaker in each quarter of the call.
 * Returns [{ quarter: 'Q1', agent: number|null, caller: number|null }, ...];
 * null where the speaker said nothing scored in that quarter.
 */
export function quarterSentiment(transcript, totalSec) {
  const buckets = Array.from({ length: QUARTERS }, (_, i) => ({ quarter: `Q${i + 1}`, agent: [], caller: [] }));
  const span = totalSec > 0 ? totalSec : 1;
  spoken(transcript).forEach(s => {
    if (typeof s.sentimentScore !== 'number') return;
    const q = Math.min(QUARTERS - 1, Math.floor((parseClock(s.timeStart) / span) * QUARTERS));
    buckets[q][s.speaker === 'AGENT' ? 'agent' : 'caller'].push(s.sentimentScore);
  });
  const avg = (xs) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : null);
  return buckets.map(b => ({ quarter: b.quarter, agent: avg(b.agent), caller: avg(b.caller) }));
}

// Merges overlapping [start, end] ranges and returns their total length
function coveredSeconds(ranges) {
  const sorted = [...ranges].sort((a, b) => a[0] - b[0]);
  let total = 0;
  let cur = null;
  for (const [a, b] of sorted) {
    if (!cur || a > cur[1]) {
      if (cur) total += cur[1] - cur[0];
      cur = [a, b];
    } else {
      cur[1] = Math.max(cur[1], b);
    }
  }
  if (cur) total += cur[1] - cur[0];
  return total;
}

/**
 * Talk time per speaker and silence, in seconds and as shares of the call.
 * Returns { agentSec, callerSec, silenceSec, totalSec, agentPct, callerPct, silencePct }.
 */
export function speakerTime(transcript, totalSec) {
  const ranges = (who) => spoken(transcript)
    .filter(s => s.speaker === who)
    .map(s => [parseClock(s.timeStart), Math.max(parseClock(s.timeStart), parseClock(s.timeEnd))]);
  const agentSec = coveredSeconds(ranges('AGENT'));
  const callerSec = coveredSeconds(ranges('CUSTOMER'));
  const anyoneSec = coveredSeconds([...ranges('AGENT'), ...ranges('CUSTOMER')]);
  const total = Math.max(totalSec, anyoneSec);
  const silenceSec = Math.max(0, total - anyoneSec);
  const pct = (x) => (total > 0 ? Math.round((x / total) * 100) : 0);
  return { agentSec, callerSec, silenceSec, totalSec: total, agentPct: pct(agentSec), callerPct: pct(callerSec), silencePct: pct(silenceSec) };
}

// Times one speaker started while the other was still talking
export function interruptions(transcript) {
  const lines = spoken(transcript)
    .map(s => ({ who: s.speaker, start: parseClock(s.timeStart), end: parseClock(s.timeEnd) }))
    .sort((a, b) => a.start - b.start);
  let count = 0;
  for (let i = 1; i < lines.length; i++) {
    const prev = lines[i - 1];
    if (lines[i].who !== prev.who && lines[i].start < prev.end) count++;
  }
  return count;
}

const toLevel = (rms) => {
  if (rms <= 0) return 0;
  return Math.max(0, Math.min(LEVEL_OFFSET, 20 * Math.log10(rms) + LEVEL_OFFSET));
};

const SILENCE_LEVEL = SILENCE_DBFS + LEVEL_OFFSET;

/**
 * Loudness per second from a decoded recording. A stereo recording carries
 * the agent on the left channel and the customer on the right; for a mono
 * one, who spoke comes from the transcript.
 * Returns [{ second, level, kind: 'agent'|'caller'|'interruption'|'silence' }].
 */
export function loudnessPerSecond(channels, sampleRate, transcript = []) {
  const [left, right] = channels;
  const seconds = Math.floor(left.length / sampleRate);
  const rmsOf = (data, from, to) => {
    let sum = 0;
    for (let i = from; i < to; i++) sum += data[i] * data[i];
    return Math.sqrt(sum / Math.max(1, to - from));
  };
  const speakerAt = (sec) => {
    const s = spoken(transcript).find(x => parseClock(x.timeStart) <= sec + 0.5 && parseClock(x.timeEnd) >= sec);
    return s ? (s.speaker === 'AGENT' ? 'agent' : 'caller') : null;
  };
  const out = [];
  for (let sec = 0; sec < seconds; sec++) {
    const from = sec * sampleRate;
    const to = from + sampleRate;
    if (right) {
      const a = toLevel(rmsOf(left, from, to));
      const c = toLevel(rmsOf(right, from, to));
      let kind = 'silence';
      if (a >= SILENCE_LEVEL && c >= SILENCE_LEVEL) {
        kind = a - c >= DOMINANCE_DB ? 'agent' : c - a >= DOMINANCE_DB ? 'caller' : 'interruption';
      } else if (a >= SILENCE_LEVEL) kind = 'agent';
      else if (c >= SILENCE_LEVEL) kind = 'caller';
      out.push({ second: sec, level: Math.round(Math.max(a, c)), kind });
    } else {
      const level = toLevel(rmsOf(left, from, to));
      const kind = level < SILENCE_LEVEL ? 'silence' : speakerAt(sec) || 'agent';
      out.push({ second: sec, level: Math.round(level), kind });
    }
  }
  return out;
}

// Splits text into plain and entity parts for highlighting, longest match first
export function highlightEntities(text, entities = {}) {
  const terms = Object.entries(entities)
    .flatMap(([type, values]) => (values || []).map(v => ({ type, value: v })))
    .filter(t => t.value)
    .sort((a, b) => b.value.length - a.value.length);
  if (terms.length === 0) return [{ text }];
  const escape = (v) => v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = new RegExp(`(${terms.map(t => escape(t.value)).join('|')})`, 'gi');
  return text.split(re).filter(Boolean).map(part => {
    const hit = terms.find(t => t.value.toLowerCase() === part.toLowerCase());
    return hit ? { text: part, entity: hit.type } : { text: part };
  });
}

// Ensures the Category column and category tags are properly filled and never empty
export function getDisplayCategories(call) {
  const raw = call?.categories || [];
  const cats = raw.filter(c => c !== 'HowCanIHelp' && c !== 'AgentGreeting');
  if (cats.length > 0) return cats;

  // Derive from call analysis topic if available
  const topic = call?.analysis?.genAI?.topic || call?.analysis?.query?.topic || call?.topic;
  if (topic && !/general/i.test(topic)) {
    const formatted = topic
      .split(/[^a-zA-Z0-9]+/)
      .filter(Boolean)
      .map(w => w.charAt(0).toUpperCase() + w.slice(1).toLowerCase())
      .join('');
    if (formatted && formatted.length <= 25) return [formatted];
  }

  // Derive from issues if available
  const issues = (call?.analysis?.issues || []).filter(i => !/inquir/i.test(i));
  if (issues.length > 0) {
    const formattedIssue = issues[0]
      .split(/[^a-zA-Z0-9]+/)
      .filter(Boolean)
      .map(w => w.charAt(0).toUpperCase() + w.slice(1).toLowerCase())
      .join('');
    if (formattedIssue && formattedIssue.length <= 25) return [formattedIssue];
  }

  // Fallback so category is properly filled
  return ['GeneralEnquiry'];
}
