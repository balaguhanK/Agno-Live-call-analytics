import React, { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import TranscriptPanel from './TranscriptTimeline';
import PostCallAnalysis from './PostCallAnalysis';
import AgentAssistBot from './AgentAssistBot';
import { askAssistant } from '../api/assist';
import { openInPca, recordingUrl } from '../api/pca';
import useCategoryAlert from '../hooks/useCategoryAlert';
import CategoryPill from './CategoryPill';
import { getDisplayCategories } from '../pca/metrics';

const formatTimestamp = (ts = '') => ts.replace('T', ' ').replace('Z', '');

export function StatusBadge({ status }) {
  if (status === 'In Progress') {
    return <span className="status-badge"><span className="status-dot blue" aria-hidden="true" />In progress</span>;
  }
  return <span className="status-badge status-done"><span aria-hidden="true">✔</span> Done</span>;
}

function Attr({ label, children, wide }) {
  return (
    <div className={`call-attr-col${wide ? ' wide' : ''}`}>
      <div className="call-attr-label">{label}</div>
      <div className="call-attr-value">{children}</div>
    </div>
  );
}

// "00:02:21" → "2:21", as an audio player shows it
const playerTime = (duration = '') => {
  const [h = 0, m = 0, s = 0] = String(duration).split(':').map(Number);
  const mins = (h || 0) * 60 + (m || 0);
  return `${mins}:${String(s || 0).padStart(2, '0')}`;
};

// Looks like the browser's audio player, for calls with no recording:
// calls aren't recorded, so there is nothing to play
function RecordingPlaceholder({ duration }) {
  return (
    <div className="call-recording player-placeholder" role="group" aria-label="Recording player: this call wasn't recorded" title="Calls aren't recorded">
      <span className="pp-play" aria-hidden="true">▶</span>
      <span className="pp-time">0:00 / {playerTime(duration)}</span>
      <span className="pp-track" aria-hidden="true"><span className="pp-thumb" /></span>
      <span className="pp-icon" aria-hidden="true">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M2 6h3l4-3v10l-4-3H2z" /><path d="M11 5.5a3.5 3.5 0 0 1 0 5M12.5 3.5a6 6 0 0 1 0 9" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /></svg>
      </span>
      <span className="pp-icon" aria-hidden="true">⋮</span>
    </div>
  );
}

function CallAttributes({ call }) {
  const done = call.status === 'Done';
  return (
    <section className="cs-container" aria-label="Call attributes">
      <div className="cs-container-header">
        <h2 className="cs-container-title">Call attributes</h2>
      </div>
      <div className="call-attrs-grid">
        <Attr label="Call ID"><span style={{ fontSize: '13px', wordBreak: 'break-all' }}>{call.id}</span></Attr>
        <Attr label="Agent">{call.agent || '—'}</Attr>
        <Attr label="Initiation timestamp"><span style={{ fontSize: '13px' }}>{formatTimestamp(call.initiationTimestamp)}</span></Attr>
        <Attr label="Last update timestamp"><span style={{ fontSize: '13px' }}>{formatTimestamp(call.updatedAt || call.initiationTimestamp)}</span></Attr>
        <Attr label="Duration"><span style={{ fontVariantNumeric: 'tabular-nums' }}>{call.duration}</span></Attr>
        <Attr label="Caller phone number"><span style={{ color: '#0073bb' }}>{call.callerPhoneNumber || '—'}</span></Attr>
        <Attr label="Status"><StatusBadge status={call.status} /></Attr>
        {done && (
          <Attr label="Post call analytics" wide>
            <button type="button" className="btn btn-normal pca-open-btn" onClick={() => openInPca(call.id)}>
              Open in Post Call Analytics <span aria-hidden="true">↗</span>
              <span className="visually-hidden"> (opens in a new tab)</span>
            </button>
          </Attr>
        )}
        {done && (
          <Attr label="Recording audio" wide>
            {call.hasRecording
              ? <audio controls preload="metadata" src={recordingUrl(call.id)} aria-label="Call recording" className="call-recording" />
              : <RecordingPlaceholder duration={call.duration} />}
          </Attr>
        )}
      </div>
    </section>
  );
}

// Every issue the caller raised: their short names after the call
// ("Reservation fee", "Lost card"); while it's live, each line they raised
// one in
function issueNames(call) {
  if (call.analysis?.issues?.length) return call.analysis.issues.filter(i => !/inquir|enquir|personal loan/i.test(i));
  if (call.status !== 'In Progress') return [];
  return (call.transcript || []).filter(s => s.insight === 'issue' && !/inquir|enquir|personal loan/i.test(s.text)).map(s => s.text);
}

function CallSummary({ call }) {
  const issues = issueNames(call);
  const writing = call.status === 'Done' && call.analysis?.pending;
  return (
    <section className="cs-container summary-card" aria-label="Call summary">
      <div className="cs-container-header">
        <div>
          <h2 className="cs-container-title">Call Summary</h2>
          <a className="info-link" href="#" onClick={e => e.preventDefault()}>Info</a>
        </div>
      </div>
      <div className="summary-columns">
        <div>
          <div className="summary-tabs" role="presentation"><span className="summary-tab active">Transcript summary</span></div>
          <div className="summary-content" aria-live="polite">
            {writing && (
              <p className="insights-pending" role="status">
                <span className="typing-dots" aria-hidden="true"><span /><span /><span /></span> AI is writing the summary…
              </p>
            )}
            {call.summary?.text
              ? <ReactMarkdown>{call.summary.text}</ReactMarkdown>
              : <p className="muted">{call.status === 'In Progress' ? 'Not available yet. The summary is written when the call ends.' : 'Not available'}</p>}
          </div>
        </div>
        <div>
          <div className="summary-tabs" role="presentation"><span className="summary-tab active">Issues</span></div>
          <div className="summary-content" aria-live="polite">
            {issues.length === 0
              ? <p className="issue-text">No issue detected</p>
              : (
                <ul className="issue-list">
                  {issues.map(name => <li key={name}><span className="issue-text">{name}</span></li>)}
                </ul>
              )}
          </div>
        </div>
      </div>
    </section>
  );
}

// Matched while the call goes on, as in LCA; alert categories in red
function CallCategories({ call }) {
  const alertPattern = useCategoryAlert();
  const cats = getDisplayCategories(call);
  return (
    <section className="cs-container" aria-label="Call categories">
      <div className="cs-container-header">
        <div>
          <h2 className="cs-container-title">Call Categories</h2>
          <a className="info-link" href="#" onClick={e => e.preventDefault()}>Info</a>
        </div>
        {call.alertCount > 0 && (
          <span className="alert-pill" title="Alerts">
            <span aria-hidden="true">⚠</span> {call.alertCount} alert{call.alertCount > 1 ? 's' : ''}
          </span>
        )}
      </div>
      <div className="cs-container-body">
        {cats.length === 0
          ? <p className="muted">{call.status === 'In Progress' ? 'No categories matched yet. They appear here as the call goes on.' : 'No categories matched.'}</p>
          : (
            <ul className="category-pills" aria-label="Call categories" aria-live="polite">
              {cats.map(c => <li key={c}><CategoryPill name={c} alert={alertPattern.test(c)} /></li>)}
            </ul>
          )}
      </div>
    </section>
  );
}

function SentimentSection({ call }) {
  const [open, setOpen] = useState(call.status === 'Done');
  return (
    <section className="cs-container" aria-label="Call sentiment analysis">
      <button type="button" className="collapse-header" aria-expanded={open} onClick={() => setOpen(o => !o)}>
        <span className="cs-container-title">Call sentiment analysis</span>
        <svg className={`collapse-chevron${open ? ' open' : ''}`} width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
          <path d="M3 6l5 5 5-5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {open && <PostCallAnalysis call={call} embedded />}
    </section>
  );
}

/**
 * The LCA call page: attributes, summary and issues, categories, sentiment,
 * the live transcript with inline AI guidance, and the Agent Assist Bot.
 * controls (optional) is shown first, e.g. the phone's hang-up bar.
 */
export default function CallDetail({ call, partialText, partialSpeaker, controls }) {
  const live = call.status === 'In Progress';
  return (
    <div className="call-page">
      {live && (
        <div className="sim-bar" role="status">
          <span className="live-dot" aria-hidden="true" />
          <span style={{ color: '#1d8348', fontWeight: 600 }}>Live</span>
          <span style={{ color: '#687078' }}>The transcript and AI guidance update as the call happens.</span>
        </div>
      )}
      {controls}
      <CallAttributes call={call} />
      <div className="call-row">
        <CallSummary call={call} />
        <CallCategories call={call} />
      </div>
      <SentimentSection key={call.status} call={call} />
      <div className="call-row call-row-transcript">
        <TranscriptPanel
          callId={call.id}
          transcript={call.transcript || []}
          isLive={live}
          partialText={partialText}
          partialSpeaker={partialSpeaker}
        />
        <AgentAssistBot
          callId={call.id}
          onAsk={(question, { callId, action }) => askAssistant({ action, question, callId })}
        />
      </div>
    </div>
  );
}
