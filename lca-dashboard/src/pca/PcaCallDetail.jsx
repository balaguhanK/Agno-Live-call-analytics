import React, { useEffect, useMemo, useRef, useState } from 'react';
import useLiveCall from '../hooks/useLiveCall';
import { analyseCall } from '../api/calls';
import { askAboutCall, recordingUrl } from '../api/pca';
import { SentimentFace } from '../components/TranscriptTimeline';
import { QuarterSentimentChart, SpeakerTimeChart, LoudnessChart, SpeakerBarChart } from './charts';
import {
  callSeconds, quarterSentiment, speakerTime, interruptions, loudnessPerSecond, parseClock, highlightEntities,
} from './metrics';

const ENTITY_TYPES = ['ORGANIZATION', 'PERSON', 'PRODUCT', 'QUANTITY', 'DATE', 'REFERENCE'];
const TIMELINE_ROWS = [
  { speaker: 'AGENT', label: 'Agent', cls: 'agent' },
  { speaker: 'CUSTOMER', label: 'Customer', cls: 'caller' },
];
// Narrowest segment drawn on the timeline, as a share of the call (%)
const MIN_SEGMENT_PCT = 0.6;

// 75 → "1:15"
const minSec = (sec) => `${Math.floor(sec / 60)}:${String(Math.round(sec % 60)).padStart(2, '0')}`;
const wordCount = (text = '') => text.split(/\s+/).filter(Boolean).length;
const TREND_ICONS = { Up: '↗', Down: '↘', Flat: '→' };

// Same thresholds as the backend's line labels
const POSITIVE_SCORE = 1;
const NEGATIVE_SCORE = -1;
const moodOf = (score = 0) => (score >= POSITIVE_SCORE ? 'Positive' : score <= NEGATIVE_SCORE ? 'Negative' : 'Neutral');

const formatTimestamp = (ts = '') => ts.replace('T', ' ').replace('Z', '').slice(0, 19);

function Card({ title, children, actions, className = '' }) {
  return (
    <section className={`pca-card ${className}`} aria-label={title}>
      <div className="pca-card-head">
        <h2>{title}</h2>
        {actions}
      </div>
      {children}
    </section>
  );
}

function Field({ label, children }) {
  return (
    <div className="pca-field">
      <dt>{label}</dt>
      <dd>{children === null || children === undefined || children === '' ? '—' : children}</dd>
    </div>
  );
}

function Sentiment({ label, score, trend }) {
  return (
    <span className="pca-sentiment">
      <SentimentFace sentiment={(label || 'Neutral').toLowerCase()} /> {label}
      {typeof score === 'number' && <> · {score > 0 ? '+' : ''}{score.toFixed(2)}</>}
      {trend && <> · <span aria-hidden="true">{TREND_ICONS[trend]}</span> {trend}</>}
    </span>
  );
}

// Decodes the recording in the browser and measures its loudness per second.
// Returns { status: 'none' | 'loading' | 'ready' | 'error', data, error }.
function useLoudness(call) {
  const id = call?.id;
  const has = Boolean(call?.hasRecording);
  const transcript = call?.transcript;
  const [decoded, setDecoded] = useState({ id: null });

  useEffect(() => {
    if (!id || !has) return undefined;
    let ignore = false;
    (async () => {
      try {
        const res = await fetch(recordingUrl(id), { credentials: 'same-origin' });
        if (!res.ok) throw new Error(`download failed (${res.status})`);
        const ctx = new AudioContext();
        const buffer = await ctx.decodeAudioData(await res.arrayBuffer());
        ctx.close().catch(() => {});
        const channels = Array.from({ length: Math.min(2, buffer.numberOfChannels) }, (_, i) => buffer.getChannelData(i));
        if (!ignore) setDecoded({ id, channels, sampleRate: buffer.sampleRate });
      } catch (err) {
        if (!ignore) setDecoded({ id, error: err.message });
      }
    })();
    return () => { ignore = true; };
  }, [id, has]);

  return useMemo(() => {
    if (!id || !has) return { status: 'none', data: [] };
    if (decoded.id !== id) return { status: 'loading', data: [] };
    if (decoded.error) return { status: 'error', data: [], error: decoded.error };
    return { status: 'ready', data: loudnessPerSecond(decoded.channels, decoded.sampleRate, transcript || []) };
  }, [id, has, decoded, transcript]);
}

function GenAIQuery({ callId }) {
  const [question, setQuestion] = useState('');
  const [history, setHistory] = useState([]);
  const [asking, setAsking] = useState('');
  const busy = asking !== '';
  const submit = async (e) => {
    e.preventDefault();
    const q = question.trim();
    if (!q || busy) return;
    setAsking(q);
    setQuestion('');
    try {
      const res = await askAboutCall(callId, q);
      setHistory(h => [...h, { q, a: res.answer, model: res.model }]);
    } catch (err) {
      setHistory(h => [...h, { q, a: `Sorry, that didn't work: ${err.message}` }]);
    } finally {
      setAsking('');
    }
  };
  return (
    <div className="pca-query">
      <div className="pca-query-log" aria-live="polite">
        {history.length === 0 && !busy && <p className="muted">Type a question about this call below and press Enter or Submit.</p>}
        {history.map((h, i) => (
          <div key={i} className="pca-query-item">
            <p className="pca-query-q">{h.q}</p>
            <p>{h.a}</p>
            {h.model && <p className="muted small">Answered by AI from the transcript</p>}
          </div>
        ))}
        {busy && (
          <div className="pca-query-item" role="status">
            <p className="pca-query-q">{asking}</p>
            <p className="muted">Thinking…</p>
          </div>
        )}
      </div>
      <form onSubmit={submit} className="pca-query-form">
        <input value={question} onChange={e => setQuestion(e.target.value)} placeholder="Enter a question about the call" aria-label="Question about the call" maxLength={500} />
        <button className="btn btn-normal" type="submit" disabled={busy || !question.trim()}>Submit</button>
      </form>
    </div>
  );
}

// What each entity tab is called when it has nothing to show
const ENTITY_NOUNS = {
  ORGANIZATION: 'organisations', PERSON: 'names', PRODUCT: 'products or services', QUANTITY: 'amounts or numbers', DATE: 'dates', REFERENCE: 'order or reference numbers',
};

// Every entity type is a tab, as in the call analytics view; the first one
// with entities is open to start with
function Entities({ entities = {} }) {
  const [tab, setTab] = useState(() => ENTITY_TYPES.find(t => entities[t]?.length) || ENTITY_TYPES[0]);
  const values = entities[tab] || [];
  // Arrow keys move between tabs, as for any tab list
  const onKeyDown = (e) => {
    const step = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
    if (!step) return;
    e.preventDefault();
    const next = ENTITY_TYPES[(ENTITY_TYPES.indexOf(tab) + step + ENTITY_TYPES.length) % ENTITY_TYPES.length];
    setTab(next);
    e.currentTarget.parentElement.querySelector(`[data-type="${next}"]`)?.focus();
  };
  return (
    <>
      <div className="pca-tabs entity-tabs" role="tablist" aria-label="Entity types">
        {ENTITY_TYPES.map(t => (
          <button
            key={t}
            data-type={t}
            role="tab"
            type="button"
            id={`entity-tab-${t}`}
            aria-selected={tab === t}
            aria-controls="entity-panel"
            tabIndex={tab === t ? 0 : -1}
            className={tab === t ? 'active' : ''}
            onClick={() => setTab(t)}
            onKeyDown={onKeyDown}
          >
            {t}
          </button>
        ))}
      </div>
      <div id="entity-panel" role="tabpanel" aria-labelledby={`entity-tab-${tab}`}>
        {values.length
          ? (
            <ul className="pca-chips entity-chips">
              {values.map(v => {
                // People come as "Bob Smith (Agent)": the name, then their role
                const m = tab === 'PERSON' && /^(.*) \((Agent|Customer)\)$/.exec(v);
                return (
                  <li key={v} className={`entity ${tab.toLowerCase()}`}>
                    {m ? <>{m[1]} <span className={`entity-role ${m[2].toLowerCase()}`}>{m[2]}</span></> : v}
                  </li>
                );
              })}
            </ul>
          )
          : <p className="muted small">No {ENTITY_NOUNS[tab]} mentioned on this call.</p>}
        {tab === 'PERSON' && !values.some(v => v.endsWith('(Customer)')) && (
          <p className="muted small">The customer didn't say their name on the call.</p>
        )}
      </div>
    </>
  );
}

// How issue, action and outcome sentences are labelled in the transcript
const HIGHLIGHT_LABELS = { issue: 'Issue', action: 'Action', outcome: 'Outcome' };

// Entities marked inside a piece of text; people's role labels ("Bob Smith
// (Agent)") are dropped so their names match the words said
function EntityText({ text, entities }) {
  return highlightEntities(text, entities).map((part, i) => (part.entity
    ? <mark key={i} className={`entity ${part.entity.toLowerCase()}`} title={part.entity}>{part.text}</mark>
    : <React.Fragment key={i}>{part.text}</React.Fragment>));
}

/**
 * The call's transcript, as on the call analytics page: who spoke and when,
 * each line's sentiment, the category its topic starts, the sentence that is
 * an issue, action or outcome, and the entities mentioned. With a recording,
 * a line's time plays the call from there.
 */
function PcaTranscript({ call, audioRef, currentSec }) {
  const a = call.analysis || {};
  const entities = useMemo(() => {
    const out = {};
    for (const [type, values] of Object.entries(a.entities || {})) {
      out[type] = (values || []).map(v => v.replace(/ \((Agent|Customer)\)$/, ''));
      // "Bob Smith" is "Bob" on the call: mark first names too
      if (type === 'PERSON') out[type] = [...new Set(out[type].flatMap(n => [n, n.split(' ')[0]]))];
    }
    return out;
  }, [a.entities]);
  // Every highlighted sentence of each line, in the order they were said
  const highlights = new Map();
  for (const h of a.highlights || []) highlights.set(h.line, [...(highlights.get(h.line) || []), h]);
  // A backend that doesn't send highlights still labels lines during the
  // call: highlight those lines whole
  if (!a.highlights) {
    for (const s of call.transcript || []) {
      if (s.insight && HIGHLIGHT_LABELS[s.insight]) highlights.set(s.id, [{ line: s.id, kind: s.insight, text: s.text }]);
    }
  }
  const tagsByLine = {};
  for (const [category, line] of Object.entries(a.categoryLines || {})) {
    (tagsByLine[line] = tagsByLine[line] || []).push(category);
  }
  const lines = (call.transcript || []).filter(s => s.speaker === 'AGENT' || s.speaker === 'CUSTOMER');
  if (lines.length === 0) return <p className="muted">Nothing was transcribed on this call.</p>;

  const seek = (s) => {
    const el = audioRef.current;
    if (!el) return;
    el.currentTime = parseClock(s.timeStart);
    el.play().catch(() => {});
  };

  return (
    <ol className="pca-transcript">
      {lines.map(s => {
        const who = s.speaker === 'AGENT' ? 'Agent' : 'Customer';
        const at = (s.timeStart || '').split('.')[0];
        const start = parseClock(s.timeStart);
        const playing = currentSec !== null && currentSec >= start && currentSec < parseClock(s.timeEnd) + 0.3;
        // The line split into plain text and highlighted sentences
        const parts = [];
        let from = 0;
        for (const h of highlights.get(s.id) || []) {
          const at = s.text.indexOf(h.text, from);
          if (at < 0) continue;
          if (at > from) parts.push({ text: s.text.slice(from, at) });
          parts.push({ text: h.text, kind: h.kind });
          from = at + h.text.length;
        }
        if (from < s.text.length) parts.push({ text: s.text.slice(from) });
        return (
          <li key={s.id} className={playing ? 'playing' : undefined}>
            <SentimentFace sentiment={s.sentiment} />
            <div>
              <div className="pca-line-head">
                {call.hasRecording
                  ? <button type="button" className="pca-line-time" onClick={() => seek(s)} aria-label={`Play from ${at}, ${who}`}>{who} - {at}</button>
                  : <span className="pca-line-time">{who} - {at}</span>}
                {(tagsByLine[s.id] || []).map(c => <span key={c} className="pca-line-tag">{c}</span>)}
              </div>
              <p className="pca-line-text">
                {parts.map((part, i) => (part.kind
                  ? (
                    <mark key={i} className={`pca-insight ${part.kind}`}>
                      [{HIGHLIGHT_LABELS[part.kind] || part.kind}]: <EntityText text={part.text} entities={entities} />
                    </mark>
                  )
                  : <EntityText key={i} text={part.text} entities={entities} />))}
              </p>
            </div>
          </li>
        );
      })}
    </ol>
  );
}

// Who spoke when, from the transcript: for calls without a recording to measure
function SpeakerTimeline({ transcript, time }) {
  const [view, setView] = useState('bar');
  const lines = transcript.filter(s => s.speaker === 'AGENT' || s.speaker === 'CUSTOMER');
  if (lines.length === 0) return <p className="muted">Nobody spoke on this call.</p>;
  const span = time.totalSec > 0 ? time.totalSec : 1;
  const statsFor = (speaker) => {
    const mine = lines.filter(s => s.speaker === speaker);
    return {
      lines: mine.length,
      words: mine.reduce((n, s) => n + wordCount(s.text), 0),
      longest: Math.max(0, ...mine.map(s => parseClock(s.timeEnd) - parseClock(s.timeStart))),
    };
  };
  const talk = { AGENT: time.agentSec, CUSTOMER: time.callerSec };
  const stats = {
    AGENT: statsFor('AGENT'),
    CUSTOMER: statsFor('CUSTOMER'),
  };

  return (
    <>
      <div className="speaker-view-header">
        <p className="muted small">
          {view === 'bar'
            ? "This call wasn't recorded, so loudness can't be measured. Talk time per speaker, from the transcript:"
            : "This call wasn't recorded, so loudness can't be measured. Who spoke when, from the transcript:"}
        </p>
        <div className="speaker-toggle" role="group" aria-label="Speaker chart view">
          <button
            type="button"
            className={view === 'bar' ? 'active' : ''}
            onClick={() => setView('bar')}
          >
            Bar chart
          </button>
          <button
            type="button"
            className={view === 'timeline' ? 'active' : ''}
            onClick={() => setView('timeline')}
          >
            Timeline
          </button>
        </div>
      </div>

      {view === 'bar' ? (
        <div className="speaker-barchart-container">
          <div className="speaker-barchart-chart">
            <SpeakerBarChart time={time} stats={stats} />
          </div>
          <div className="speaker-barchart-table">
            <table className="speaker-stats">
              <thead>
                <tr>
                  <th scope="col">Speaker</th>
                  <th scope="col">Talk time</th>
                  <th scope="col">Lines</th>
                  <th scope="col">Words</th>
                  <th scope="col">Longest turn</th>
                </tr>
              </thead>
              <tbody>
                {TIMELINE_ROWS.map(row => {
                  const st = stats[row.speaker];
                  return (
                    <tr key={row.speaker}>
                      <th scope="row"><i className={`speaker-dot ${row.cls}`} aria-hidden="true" />{row.label}</th>
                      <td><strong>{minSec(talk[row.speaker])}</strong> <span className="muted small">({row.speaker === 'AGENT' ? time.agentPct : time.callerPct}%)</span></td>
                      <td>{st.lines}</td>
                      <td>{st.words}</td>
                      <td>{minSec(st.longest)}</td>
                    </tr>
                  );
                })}
                <tr>
                  <th scope="row"><i className="speaker-dot silence" aria-hidden="true" />Silence</th>
                  <td><strong>{minSec(time.silenceSec)}</strong> <span className="muted small">({time.silencePct}%)</span></td>
                  <td colSpan={3} className="muted">—</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="speaker-timeline-wrap">
          <div className="speaker-timeline">
            {TIMELINE_ROWS.map(row => {
              const mine = lines.filter(s => s.speaker === row.speaker);
              return (
                <div className="speaker-row" key={row.speaker}>
                  <span className="speaker-row-label">{row.label}</span>
                  <div className="speaker-track" role="img" aria-label={`${row.label} spoke ${mine.length} times`}>
                    {mine.map(s => {
                      const start = parseClock(s.timeStart);
                      const end = Math.max(start, parseClock(s.timeEnd));
                      return (
                        <span
                          key={s.id}
                          className={`speaker-seg ${row.cls}`}
                          style={{ left: `${(start / span) * 100}%`, width: `${Math.max(MIN_SEGMENT_PCT, ((end - start) / span) * 100)}%` }}
                          title={`${s.timeStart.split('.')[0]} ${s.text}`}
                        />
                      );
                    })}
                  </div>
                </div>
              );
            })}
            <div className="speaker-axis" aria-hidden="true"><span>0:00</span><span>{minSec(span)}</span></div>
          </div>
          <table className="speaker-stats">
            <thead>
              <tr><th scope="col">Speaker</th><th scope="col">Talk time</th><th scope="col">Lines</th><th scope="col">Words</th><th scope="col">Longest turn</th></tr>
            </thead>
            <tbody>
              {TIMELINE_ROWS.map(row => {
                const st = stats[row.speaker];
                return (
                  <tr key={row.speaker}>
                    <th scope="row"><i className={`speaker-dot ${row.cls}`} aria-hidden="true" />{row.label}</th>
                    <td>{minSec(talk[row.speaker])}</td>
                    <td>{st.lines}</td>
                    <td>{st.words}</td>
                    <td>{minSec(st.longest)}</td>
                  </tr>
                );
              })}
              <tr><th scope="row"><i className="speaker-dot silence" aria-hidden="true" />Silence</th><td>{minSec(time.silenceSec)}</td><td colSpan={3} /></tr>
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// The transcript line an AI item is based on, exactly as it was said
function Evidence({ quote, who }) {
  if (!quote) return null;
  return (
    <blockquote className="pca-evidence">
      <span className="pca-evidence-label">{who ? `${who} at ` : 'At '}{quote.time}:</span> “{quote.text}”
    </blockquote>
  );
}

// Items flagged on transcript lines, or the fallback from the analysis
function Highlights({ lines, kind, fallback, empty }) {
  if (lines.length) return lines.map(s => <p key={s.id} className={`pca-hl ${kind}`}>{s.text}</p>);
  if (fallback.length) return fallback;
  return <p className="muted">{empty}</p>;
}

/** Post-Call Analytics: one call in detail. */
export default function PcaCallDetail({ callId }) {
  const { call, error } = useLiveCall(callId);
  const loudness = useLoudness(call);
  const audioRef = useRef(null);
  const [currentSec, setCurrentSec] = useState(null);
  const [rerun, setRerun] = useState({ busy: false, error: '' });

  const metrics = useMemo(() => {
    if (!call) return null;
    const total = callSeconds(call);
    const transcript = call.transcript || [];
    return {
      total,
      quarters: quarterSentiment(transcript, total),
      time: speakerTime(transcript, total),
      interruptions: interruptions(transcript),
    };
  }, [call]);

  if (error) {
    return <div className="pca-page"><div className="users-message error" role="alert">{error}</div></div>;
  }
  if (!call) {
    return (
      <div className="pca-page">
        <div className="empty-state" role="status"><span className="skeleton-bar" style={{ width: '240px' }} /><span>Loading call…</span></div>
      </div>
    );
  }
  if (call.status !== 'Done') {
    return (
      <div className="pca-page">
        <div className="empty-state" role="status">
          <strong>This call is still in progress</strong>
          <span>Post-call analytics are ready as soon as it ends. This page updates by itself.</span>
        </div>
      </div>
    );
  }

  const a = call.analysis || {};
  const g = a.genAI || {};
  const entities = a.entities || {};
  const query = a.query || {};
  const transcript = call.transcript || [];
  const linesOf = (kind) => transcript.filter(s => s.insight === kind);

  const reanalyse = async () => {
    setRerun({ busy: true, error: '' });
    try { await analyseCall(call.id); setRerun({ busy: false, error: '' }); } catch (err) { setRerun({ busy: false, error: err.message }); }
  };

  return (
    <div className="pca-page">
      <div className="pca-grid three">
        <Card title="Call metadata">
          <dl className="pca-fields">
            <Field label="Timestamp">{formatTimestamp(call.initiationTimestamp)}</Field>
            <Field label="GUID"><span className="mono">{call.id}</span></Field>
            <Field label="Agent">{call.agent}</Field>
            <Field label="Customer">{call.callerPhoneNumber}</Field>
            <Field label="Call duration">{call.duration}</Field>
            <Field label="Agent sentiment"><Sentiment label={moodOf(call.agentAvgSentiment)} score={call.agentAvgSentiment} trend={call.agentSentimentTrend} /></Field>
            <Field label="Customer sentiment"><Sentiment label={call.callerSentiment} score={call.callerAvgSentiment} trend={call.callerSentimentTrend} /></Field>
          </dl>
        </Card>
        <Card title="Transcription details">
          <dl className="pca-fields">
            <Field label="Type">Live call transcription</Field>
            <Field label="Source">{call.systemPhoneNumber}</Field>
            <Field label="Personal data redaction">Enabled (cards, emails, phone numbers, SSNs)</Field>
            <Field label="Recording">{call.hasRecording ? 'Recorded: play it from the transcript below' : 'Not recorded'}</Field>
            <Field label="Lines">{transcript.filter(s => s.speaker === 'AGENT' || s.speaker === 'CUSTOMER').length} spoken, {transcript.filter(s => s.speaker === 'AGENT_ASSISTANT').length} from the assistant</Field>
            <Field label="Interruptions">{metrics.interruptions}</Field>
          </dl>
        </Card>
        <Card title="Sentiment">
          <QuarterSentimentChart data={metrics.quarters} />
          <h3 className="pca-subhead">Speaker time</h3>
          <SpeakerTimeChart time={metrics.time} />
        </Card>
      </div>

      <Card title={call.hasRecording ? 'Loudness and speakers' : 'Speakers'}>
        {loudness.status === 'none' && <SpeakerTimeline transcript={transcript} time={metrics.time} />}
        {loudness.status === 'loading' && <div className="empty-state" role="status"><span className="skeleton-bar" style={{ width: '60%' }} /><span>Measuring the recording…</span></div>}
        {loudness.status === 'error' && <p className="insight-error" role="alert">The recording couldn't be analysed: {loudness.error}</p>}
        {loudness.status === 'ready' && <LoudnessChart data={loudness.data} />}
      </Card>

      <div className="pca-grid two">
        <Card title="Entities"><Entities entities={entities} /></Card>
        <Card title="Categories">
          {call.categories?.filter(c => c !== 'HowCanIHelp').length
            ? <ul className="pca-chips">{call.categories.filter(c => c !== 'HowCanIHelp').map(c => <li key={c}>{c}</li>)}</ul>
            : <p className="muted">No categories matched: this was a general enquiry.</p>}
        </Card>
      </div>

      <div className="pca-grid three">
        <Card
          title="Generative AI insights"
          actions={(
            <button type="button" className="btn-icon" onClick={reanalyse} disabled={rerun.busy || a.pending} aria-label="Write the insights again" title="Write the insights again">↻</button>
          )}
        >
          {g.model && (
            <p className="muted small pca-checked">
              Anything naming a number, date or reference that wasn't said on the call is replaced with what was said.
            </p>
          )}
          {a.pending && <p className="insights-pending" role="status"><span className="typing-dots" aria-hidden="true"><span /><span /><span /></span> AI is writing the insights…</p>}
          {rerun.error && <p className="insight-error" role="alert">{rerun.error}</p>}
          <dl className="pca-fields">
            <Field label="Actions">{g.actions}</Field>
            <Field label="Politeness">{g.politeness}</Field>
            <Field label="Product">{g.product}</Field>
            <Field label="Summary">{g.summary}</Field>
            <Field label="Topic">{g.topic}</Field>
          </dl>
        </Card>
        <Card title="Generative AI query"><GenAIQuery callId={call.id} /></Card>
        <Card title="Call analytics summary">
          <h3 className="pca-subhead">Issue</h3>
          {a.issues?.filter(n => !/inquir|enquir|personal loan/i.test(n)).length > 0 && (
            <ul className="pca-chips pca-issue-names">
              {a.issues.filter(n => !/inquir|enquir|personal loan/i.test(n)).map(n => <li key={n}>{n}</li>)}
            </ul>
          )}
          <Highlights
            lines={linesOf('issue')}
            kind="issue"
            fallback={a.issues?.length || !query.issue ? [] : [<p key="q" className="pca-hl issue">{query.issue}</p>]}
            empty="No issue was raised."
          />
          <h3 className="pca-subhead">Action items</h3>
          <Highlights
            lines={linesOf('action')}
            kind="action"
            fallback={(a.actions || []).map((act, i) => (
              <div key={i} className="pca-hl action">
                <strong>{act.title}</strong>
                {act.detail && <div>{act.detail}</div>}
                <div className="muted small">Owner: {act.owner || 'TBD'} · Due: {act.due || 'TBD'}</div>
                <Evidence quote={act.evidence} who="Agent" />
              </div>
            ))}
            empty="The agent didn't take or promise any action."
          />
          <h3 className="pca-subhead">Outcomes</h3>
          <Highlights
            lines={linesOf('outcome')}
            kind="outcome"
            fallback={query.outcome || query.detail ? [
              <div key="o" className="pca-hl outcome">
                {query.outcome && <strong>{query.outcome}</strong>}
                {query.detail && <div>{query.detail}</div>}
                <Evidence quote={query.evidence} />
              </div>,
            ] : []}
            empty="No outcome was recorded for this call."
          />
        </Card>
      </div>

      <Card
        title="Transcript"
        className="pca-transcript-card"
        actions={call.hasRecording && (
          <audio
            ref={audioRef}
            controls
            preload="metadata"
            src={recordingUrl(call.id)}
            aria-label="Call recording"
            className="pca-recording"
            onTimeUpdate={e => setCurrentSec(e.currentTarget.currentTime)}
            onPause={e => setCurrentSec(e.currentTarget.currentTime)}
          />
        )}
      >
        <PcaTranscript call={call} audioRef={audioRef} currentSec={currentSec} />
      </Card>
    </div>
  );
}
