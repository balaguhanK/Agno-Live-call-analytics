import React, { useEffect, useId, useRef, useState } from 'react';
import { translateLines, TRANSLATION_LANGUAGES } from '../api/pca';

const SENTIMENT_LABELS = { positive: 'Positive', neutral: 'Neutral', negative: 'Negative' };
// How each speaker is labelled on screen
const SPEAKER_LABELS = { CUSTOMER: 'CALLER', AGENT: 'AGENT', AGENT_ASSISTANT: 'AGENT_ASSISTANT' };
// A line the model skips is retried this many times
const TRANSLATE_RETRIES = 1;
// Only what people said is translated, not the assistant's guidance
const SPOKEN = new Set(['AGENT', 'CUSTOMER']);

// "mm:ss.s" → seconds
const clockSeconds = (t = '') => {
  const [m, sec] = String(t).split(':');
  const n = Number(m) * 60 + Number(sec);
  return Number.isFinite(n) ? n : 0;
};

// Outlined face beside each spoken line, coloured by its sentiment and labelled
export function SentimentFace({ sentiment }) {
  const label = SENTIMENT_LABELS[sentiment] || SENTIMENT_LABELS.neutral;
  const mouth = sentiment === 'positive'
    ? <path d="M6.5 12 Q10 15.5 13.5 12" fill="none" />
    : sentiment === 'negative'
      ? <path d="M6.5 14 Q10 10.5 13.5 14" fill="none" />
      : <line x1="7" y1="12.8" x2="13" y2="12.8" />;
  return (
    <span className={`sentiment-icon-wrap ${SENTIMENT_LABELS[sentiment] ? sentiment : 'neutral'}`} role="img" aria-label={`${label} sentiment`} title={label}>
      <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" aria-hidden="true">
        <circle cx="10" cy="10" r="8.3" />
        <circle cx="7" cy="8" r="0.6" fill="currentColor" />
        <circle cx="13" cy="8" r="0.6" fill="currentColor" />
        {mouth}
      </svg>
    </span>
  );
}

function PiiTags({ pii = [] }) {
  return pii.map((p, i) => <span key={i} className="pii-tag">{p}</span>);
}

const sourceName = (s) => `${s.title}${s.section ? ` › ${s.section}` : ''}`;

// Knowledge-base sources as links; each opens the policy passage it is
// based on, under the links. note says how the advice was written.
export function SourceLinks({ sources = [], note }) {
  const [open, setOpen] = useState(null);
  const passageId = useId();
  if (sources.length === 0) return note ? <div className="assist-sources">{note}</div> : null;
  const shown = open === null ? null : sources[open];
  return (
    <div className="assist-sources">
      Source{sources.length > 1 ? 's' : ''}:{' '}
      {sources.map((s, i) => (
        <React.Fragment key={i}>
          {i > 0 && ', '}
          <a
            href={`#${passageId}`}
            className="source-link"
            aria-expanded={open === i}
            aria-controls={passageId}
            onClick={(e) => { e.preventDefault(); setOpen(open === i ? null : i); }}
          >{sourceName(s)}</a>
        </React.Fragment>
      ))}
      {note && <span> · {note}</span>}
      {shown && (
        <blockquote id={passageId} className="source-passage">
          {shown.excerpt || "The knowledge base passage isn't available for this source."}
        </blockquote>
      )}
    </div>
  );
}

// Knowledge-base guidance shown inline, right under what the customer said
function AssistAnswer({ item }) {
  const { assist } = item;
  if (!assist) return <div className="transcript-text agent-assist">{item.text}</div>;
  // Only the best-matching source: the line above already shows what the customer said
  const sources = (assist.sources || []).slice(0, 1);
  const steps = assist.steps || [];
  return (
    <div className="transcript-text agent-assist lca-assist">
      <p className="lca-assist-say">
        {assist.say ? <strong>Suggested response: {assist.say}</strong> : item.text}
      </p>
      {steps.length > 0 && (
        <>
          <p className="lca-assist-steps-label">Steps to take:</p>
          <ol className="lca-assist-steps">{steps.map((st, i) => <li key={i}>{st}</li>)}</ol>
        </>
      )}
      {assist.pending && (
        <p className="guidance-pending" role="status">
          <span className="typing-dots" aria-hidden="true"><span /><span /><span /></span> AI is tailoring this reply to the call…
        </p>
      )}
      <SourceLinks sources={sources} />
    </div>
  );
}

// Underlines only the issue itself: "Hi, I'm calling about my rewards
// card." → "Hi, " + underlined "I'm calling about my rewards card" + "."
function IssueText({ text, phrase }) {
  const at = phrase ? text.indexOf(phrase) : -1;
  if (at < 0) return <span className="issue-text">{text}</span>;
  return (
    <>
      {text.slice(0, at)}
      <span className="issue-text">{phrase}</span>
      {text.slice(at + phrase.length)}
    </>
  );
}

function downloadTranscript(callId, transcript) {
  const text = transcript
    .map(s => `${SPEAKER_LABELS[s.speaker] || s.speaker} ${s.timeStart || ''} - ${s.timeEnd || ''}\n${s.text}\n`)
    .join('\n');
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = `transcript-${callId || 'call'}.txt`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

// Translates the spoken lines one at a time, newest first, as they arrive
// and whenever the language changes. A line the model skips is retried;
// one it still can't translate is left as it is.
function useTranslations(transcript, enabled, language) {
  const [cache, setCache] = useState({}); // "fr:12" → text, or null when given up on
  const [pending, setPending] = useState(() => new Set());
  const [error, setError] = useState('');
  const tries = useRef({}); // key → requests that came back without it
  const busy = useRef(false);

  useEffect(() => {
    if (!enabled || busy.current) return;
    const key = (s) => `${language}:${s.id}`;
    const lines = transcript.filter(s => SPOKEN.has(s.speaker) && s.text && cache[key(s)] === undefined);
    // One line at a time, the newest first: what was just said is translated
    // in a second or two instead of waiting behind a batch of older lines
    const next = lines[lines.length - 1];
    if (!next) return;
    const todo = [next];

    busy.current = true;
    Promise.resolve()
      .then(() => {
        setPending(new Set(todo.map(key)));
        return translateLines(todo.map(s => s.text), language);
      })
      .then(res => {
        setError('');
        setCache(c => {
          const next = { ...c };
          todo.forEach((s, i) => {
            const text = res.translations[i];
            if (text) next[key(s)] = text;
            else if ((tries.current[key(s)] = (tries.current[key(s)] || 0) + 1) > TRANSLATE_RETRIES) next[key(s)] = null;
          });
          return next;
        });
      })
      .catch(err => {
        setError(err.message);
        // Stop until Retry, rather than hammering a model that is down
        setCache(c => {
          const next = { ...c };
          todo.forEach(s => { next[key(s)] = null; });
          return next;
        });
      })
      .finally(() => {
        busy.current = false;
        setPending(new Set());
      });
    // pending changes when a batch finishes, which starts the next one
  }, [transcript, enabled, language, cache, pending]);

  const translationFor = (s) => (enabled ? cache[`${language}:${s.id}`] || undefined : undefined);
  const isTranslating = (s) => enabled && pending.has(`${language}:${s.id}`);
  const retry = () => {
    tries.current = {};
    setError('');
    // Forget the lines given up on in this language so they're asked for again
    setCache(c => Object.fromEntries(Object.entries(c).filter(([k, v]) => v !== null || !k.startsWith(`${language}:`))));
  };
  return { translationFor, isTranslating, error, retry };
}

function Toggle({ label, checked, onChange }) {
  return (
    <label className="autoscroll-toggle">
      <span className="toggle-switch">
        <input type="checkbox" role="switch" checked={checked} onChange={e => onChange(e.target.checked)} />
        <span className="toggle-slider" />
      </span>
      {label}
    </label>
  );
}

/**
 * Live call transcript. Lines with insight "issue" are flagged, assistant
 * answers show inline with their sources, and lines can be translated.
 */
export default function TranscriptPanel({ callId, transcript, isLive, partialText, partialSpeaker = 'CUSTOMER' }) {
  const [autoScroll, setAutoScroll] = useState(true);
  const [showAgent, setShowAgent] = useState(true);
  const [translate, setTranslate] = useState(false);
  const [language, setLanguage] = useState(TRANSLATION_LANGUAGES[0].code);
  const listRef = useRef(null);
  const { translationFor, isTranslating, error: translateError, retry } = useTranslations(transcript, translate, language);

  // The list starts at its first line. It follows the call down only for lines
  // that arrive while it is open, and scrolls the list itself, not the page,
  // so the agent keeps their place
  const openedWith = useRef(transcript.length);
  useEffect(() => {
    const el = listRef.current;
    const newLines = transcript.length > openedWith.current || Boolean(partialText);
    if (autoScroll && isLive && newLines && el) el.scrollTop = el.scrollHeight;
  }, [transcript, partialText, autoScroll, isLive]);

  // In time order, so an AI answer that took a while still sits right under
  // the line that prompted it
  const visible = transcript
    // Categories show in the Call Categories box, not in the transcript
    .filter(s => s.speaker !== 'CATEGORY_MATCH' && (showAgent || s.speaker !== 'AGENT'))
    .map((s, i) => ({ s, i, at: clockSeconds(s.timeStart) }))
    .sort((a, b) => a.at - b.at || a.i - b.i)
    .map(x => x.s);
  const languageLabel = TRANSLATION_LANGUAGES.find(l => l.code === language)?.label;

  return (
    <div className="cs-container transcript-container">
      <div className="transcript-header">
        <div className="transcript-title-block">
          <div className="transcript-title">
            Call Transcript
            <button
              type="button"
              className="transcript-download-round"
            onClick={() => downloadTranscript(callId, transcript)}
            disabled={transcript.length === 0}
            aria-label="Download transcript"
            title="Download transcript"
          >
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <rect x="2.5" y="2.5" width="11" height="11" rx="1.5" />
                <path d="M8 4.5v5.5M5.5 7.5 8 10l2.5-2.5" />
              </svg>
            </button>
          </div>
          <a className="info-link transcript-info" href="#" onClick={e => e.preventDefault()}>Info</a>
        </div>
        <div className="transcript-toggles">
          <Toggle label="Auto Scroll" checked={autoScroll} onChange={setAutoScroll} />
          <Toggle label="Show Agent Transcripts?" checked={showAgent} onChange={setShowAgent} />
          <Toggle label="Enable Translation" checked={translate} onChange={setTranslate} />
          {translate && (
            <select className="load-dropdown" value={language} onChange={e => setLanguage(e.target.value)} aria-label="Translation language">
              {TRANSLATION_LANGUAGES.map(l => <option key={l.code} value={l.code}>{l.label}</option>)}
            </select>
          )}
        </div>
      </div>

      {translate && translateError && (
        <div className="users-message error" role="alert" style={{ margin: '8px 16px' }}>
          {translateError}
          <span style={{ flex: 1 }} />
          <button onClick={retry}>Retry</button>
        </div>
      )}

      <div className="transcript-list" ref={listRef} aria-live="polite" aria-relevant="additions" tabIndex={0} aria-label="Call transcript lines">
        {visible.length === 0 && !partialText && (
          <div className="empty-state" role="status">
            <strong>{isLive ? 'Waiting for the first words…' : 'No transcript'}</strong>
            <span>{isLive ? 'Lines appear here as each person speaks.' : 'Nothing was transcribed on this call.'}</span>
          </div>
        )}
        {visible.map((item) => {
          const isAssist = item.speaker === 'AGENT_ASSISTANT';
          const isIssue = item.insight === 'issue';
          const translated = translationFor(item);
          return (
            <div key={item.id} className={`transcript-item${isAssist ? ' assist' : ''}`}>
              {isAssist ? <span className="sentiment-icon-wrap" aria-hidden="true" /> : <SentimentFace sentiment={item.sentiment} />}
              <div className="transcript-body" style={{ flex: 1, minWidth: 0 }}>
                <div className="transcript-speaker-line">
                  <span className={`transcript-speaker ${isAssist ? 'agent-assist' : item.speaker === 'CUSTOMER' ? 'caller' : 'agent'}`}>
                    {SPEAKER_LABELS[item.speaker] || item.speaker}
                  </span>
                  {item.timeStart && <span className="transcript-timestamp">{item.timeStart} - {item.timeEnd}</span>}
                </div>
                {isAssist ? <AssistAnswer item={item} /> : (
                  <div className="transcript-text">
                    {isIssue ? <IssueText text={item.text} phrase={item.insightText} /> : item.text}
                    <PiiTags pii={item.pii} />
                  </div>
                )}
                {isIssue && <span className="issue-badge">Issue Detected</span>}
                {translated && (
                  <div className="transcript-translation" lang={language} aria-label={`${languageLabel} translation`}>{translated}</div>
                )}
                {!translated && isTranslating(item) && (
                  <div className="transcript-translation pending" role="status">
                    <span className="typing-dots" aria-hidden="true"><span /><span /><span /></span> Translating to {languageLabel}…
                  </div>
                )}
              </div>
            </div>
          );
        })}

        {isLive && partialText && (
          <div className="transcript-item partial">
            <span className="sentiment-icon-wrap" aria-hidden="true" />
            <div className="transcript-body">
              <div className="transcript-speaker-line">
                <span className={`transcript-speaker ${partialSpeaker === 'CUSTOMER' ? 'caller' : 'agent'}`}>{SPEAKER_LABELS[partialSpeaker]}</span>
                <span className="transcript-timestamp">live…</span>
              </div>
              <div className="transcript-text" style={{ color: '#687078', fontStyle: 'italic' }}>
                {partialText}
                <span className="typing-dots" aria-hidden="true"><span /><span /><span /></span>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
