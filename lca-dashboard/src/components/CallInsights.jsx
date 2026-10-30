import React, { useState } from 'react';
import { analyseCall } from '../api/calls';
import SentimentCharts from './SentimentChart';

// Outcome labels from the backend, with an icon so colour isn't the only cue
const OUTCOMES = {
  Resolved: { cls: 'good', icon: '✓' },
  'Partly resolved': { cls: 'mixed', icon: '◐' },
  'Not resolved': { cls: 'bad', icon: '✕' },
};
const MOODS = { Positive: 'good', Neutral: 'neutral', Negative: 'bad' };
const TRENDS = { Up: { icon: '↗', cls: 'good' }, Down: { icon: '↘', cls: 'bad' }, Flat: { icon: '→', cls: 'neutral' } };

function MoodChip({ label }) {
  if (!label) return null;
  return <span className={`insight-chip ${MOODS[label] || 'neutral'}`}>{label}</span>;
}

// Average score (-5..+5) with its trend, e.g. "+0.70 ↗ Up"
function Average({ score = 0, trend = 'Flat' }) {
  const t = TRENDS[trend] || TRENDS.Flat;
  return (
    <div className="tile-foot">
      Avg {score > 0 ? '+' : ''}{score.toFixed(2)} <span className={`tile-trend ${t.cls}`}>{t.icon} {trend}</span>
    </div>
  );
}

// Post-call analysis at a glance: the sentiment chart and talk time, then
// the caller and their query.
export default function CallInsights({ call }) {
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState('');
  const a = call.analysis;

  const reanalyse = async () => {
    setStarting(true);
    setError('');
    try {
      await analyseCall(call.id); // the result arrives as a call event
    } catch (err) {
      setError(err.message);
    } finally {
      setStarting(false);
    }
  };

  const chart = (
    <div className="insight-tile insights-chart">
      <SentimentCharts sentimentHistory={call.sentimentHistory} />
    </div>
  );

  if (!a) {
    return (
      <>
        <div className="insights-head">
          <span>This call ended before the caller and query analysis existed.</span>
          <button type="button" className="insights-rerun" onClick={reanalyse} disabled={starting}>
            {starting ? 'Starting…' : 'Analyse this call'}
          </button>
        </div>
        {error && <div className="insight-error" role="alert">{error}</div>}
        {chart}
      </>
    );
  }

  const { stats, caller, query } = a;
  const outcome = OUTCOMES[query.outcome];

  return (
    <section className="insights" aria-label="Post-call analysis at a glance">
      <div className="insights-head">
        <span role="status" aria-live="polite">
          {a.pending && (
            <span className="insights-pending"><span className="typing-dots" aria-hidden="true"><span /><span /><span /></span> Writing the analysis…</span>
          )}
        </span>
        <button type="button" className="insights-rerun" onClick={reanalyse} disabled={starting || a.pending}>Analyse again</button>
      </div>
      {error && <div className="insight-error" role="alert">{error}</div>}

      <div className="insights-row insights-row-chart">
        {chart}
        <div className="insight-tile">
          <div className="tile-label">Talk time</div>
          <div className="talk-share-bar" role="img" aria-label={`Agent ${stats.agentTalkPct}%, caller ${stats.callerTalkPct}%`}>
            <span className="talk-share-agent" style={{ width: `${stats.agentTalkPct}%` }} />
            <span className="talk-share-caller" style={{ width: `${stats.callerTalkPct}%` }} />
          </div>
          <div className="talk-share-figures">
            <div>
              <div className="talk-share-pct agent">{stats.agentTalkPct}%</div>
              <div className="talk-share-who"><i className="talk-share-dot agent" aria-hidden="true" />Agent · {stats.agentLines} lines</div>
            </div>
            <div>
              <div className="talk-share-pct caller">{stats.callerTalkPct}%</div>
              <div className="talk-share-who"><i className="talk-share-dot caller" aria-hidden="true" />Caller · {stats.callerLines} lines</div>
            </div>
          </div>
          <div className="tile-foot">Share of the words spoken on the call</div>
        </div>
      </div>

      <div className="insights-row">
        <div className="insight-tile">
          <div className="tile-label">Caller</div>
          <div className="tile-value">{caller.mood}</div>
          {stats.callerLines > 0 && (
            <div className="insight-journey" aria-label={`Mood at start ${stats.callerStart}, at end ${stats.callerEnd}`}>
              <MoodChip label={stats.callerStart} /> <span aria-hidden="true">→</span> <MoodChip label={stats.callerEnd} />
            </div>
          )}
          <Average score={call.callerAvgSentiment} trend={call.callerSentimentTrend} />
        </div>

        <div className="insight-tile">
          <div className="tile-label">Query</div>
          <div className="tile-value tile-issue" title={query.issue}>{query.issue || 'Not captured'}</div>
          {outcome && <span className={`insight-chip ${outcome.cls}`}><span aria-hidden="true">{outcome.icon} </span>{query.outcome}</span>}
          {query.detail && <p className="insight-note">{query.detail}</p>}
        </div>
      </div>
    </section>
  );
}
