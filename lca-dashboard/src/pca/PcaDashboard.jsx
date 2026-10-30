import React, { useEffect, useMemo, useState } from 'react';
import { listCalls } from '../api/calls';
import { parseDuration, getDisplayCategories } from './metrics';

// The backend's largest page; the dashboard reads at most MAX_PAGES of them
const FETCH_PAGE_SIZE = 100;
const MAX_PAGES = 5;
const WINDOWS = [
  { hours: 24, label: 'Last 24 hours' },
  { hours: 168, label: 'Last 7 days' },
  { hours: 720, label: 'Last 30 days' },
  { hours: 0, label: 'All time' },
];
const BAR_COLOR = '#0073bb';

const mean = (xs) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : 0);
const signed = (v) => `${v > 0 ? '+' : ''}${v.toFixed(2)}`;
const minutes = (sec) => `${Math.floor(sec / 60)}:${String(Math.round(sec % 60)).padStart(2, '0')}`;

async function fetchCalls(hours) {
  const all = [];
  for (let page = 1; page <= MAX_PAGES; page++) {
    const res = await listCalls({ hours, sort: 'initiationTimestamp', order: 'desc', page, pageSize: FETCH_PAGE_SIZE });
    all.push(...res.items);
    if (all.length >= res.total || res.items.length === 0) return { calls: all, total: res.total };
  }
  return { calls: all, total: all.length };
}

function Stat({ label, value, note }) {
  return (
    <div className="pca-stat">
      <div className="pca-stat-label">{label}</div>
      <div className="pca-stat-value">{value}</div>
      {note && <div className="muted small">{note}</div>}
    </div>
  );
}

/** Aggregate view across finished calls: per-agent volume and sentiment. */
export default function PcaDashboard() {
  const [hours, setHours] = useState(WINDOWS[1].hours);
  const [result, setResult] = useState({ hours: null, calls: [], total: 0, error: '' });
  const state = result.hours === hours
    ? { ...result, status: result.error ? 'error' : 'ready' }
    : { calls: [], total: 0, error: '', status: 'loading' };
  const [hover, setHover] = useState(null);

  useEffect(() => {
    let ignore = false;
    fetchCalls(hours)
      .then(({ calls, total }) => { if (!ignore) setResult({ hours, calls, total, error: '' }); })
      .catch(err => { if (!ignore) setResult({ hours, calls: [], total: 0, error: err.message }); });
    return () => { ignore = true; };
  }, [hours]);

  const done = useMemo(() => state.calls.filter(c => c.status === 'Done'), [state.calls]);
  const agents = useMemo(() => {
    const by = {};
    done.forEach(c => {
      const a = (by[c.agent || 'Unassigned'] ||= { agent: c.agent || 'Unassigned', calls: 0, customer: [], agentScores: [], seconds: [] });
      a.calls++;
      a.customer.push(c.callerAvgSentiment);
      a.agentScores.push(c.agentAvgSentiment);
      a.seconds.push(parseDuration(c.duration));
    });
    return Object.values(by)
      .map(a => ({ agent: a.agent, calls: a.calls, customer: mean(a.customer), agentAvg: mean(a.agentScores), avgSec: mean(a.seconds) }))
      .sort((x, y) => y.calls - x.calls);
  }, [done]);

  const maxCalls = Math.max(1, ...agents.map(a => a.calls));

  return (
    <div className="pca-page">
      <div className="pca-toolbar">
        <select className="load-dropdown" value={hours} onChange={e => setHours(Number(e.target.value))} aria-label="Time window">
          {WINDOWS.map(w => <option key={w.hours} value={w.hours}>{w.label}</option>)}
        </select>
        {state.total > state.calls.length && <span className="muted">Showing the latest {state.calls.length} of {state.total} calls.</span>}
      </div>
      {state.status === 'error' && <div className="users-message error" role="alert">{state.error}</div>}
      {state.status === 'loading' && <div className="empty-state" role="status"><span className="skeleton-bar" style={{ width: '50%' }} /><span>Loading calls…</span></div>}
      {state.status === 'ready' && done.length === 0 && (
        <div className="empty-state"><strong>No finished calls in this window</strong><span>Try a longer time window.</span></div>
      )}
      {state.status === 'ready' && done.length > 0 && (
        <>
          <div className="pca-stats">
            <Stat label="Finished calls" value={done.length} />
            <Stat label="Average customer sentiment" value={signed(mean(done.map(c => c.callerAvgSentiment)))} note="Scale −5 to +5" />
            <Stat label="Average agent sentiment" value={signed(mean(done.map(c => c.agentAvgSentiment)))} note="Scale −5 to +5" />
            <Stat label="Average call length" value={minutes(mean(done.map(c => parseDuration(c.duration))))} />
            <Stat label="Calls with alerts" value={done.filter(c => c.alertCount > 0).length} />
          </div>

          <div className="pca-grid two">
            <section className="pca-card" aria-label="Calls per agent">
              <div className="pca-card-head"><h2>Calls per agent</h2></div>
              <ul className="pca-bars">
                {agents.map(a => (
                  <li key={a.agent} onMouseEnter={() => setHover(a.agent)} onMouseLeave={() => setHover(null)}>
                    <span className="pca-bar-label">{a.agent}</span>
                    <span className="pca-bar-track">
                      <span className="pca-bar" style={{ width: `${(a.calls / maxCalls) * 100}%`, background: BAR_COLOR, opacity: hover && hover !== a.agent ? 0.55 : 1 }} />
                    </span>
                    <span className="pca-bar-value">{a.calls}</span>
                  </li>
                ))}
              </ul>
            </section>
            <section className="pca-card" aria-label="Sentiment by agent">
              <div className="pca-card-head"><h2>Sentiment by agent</h2></div>
              <table className="cs-table pca-table">
                <thead><tr><th>Agent</th><th>Customer sentiment</th><th>Agent sentiment</th><th>Average length</th></tr></thead>
                <tbody>
                  {agents.map(a => (
                    <tr key={a.agent}>
                      <td>{a.agent}</td>
                      <td className="tabular">{signed(a.customer)}</td>
                      <td className="tabular">{signed(a.agentAvg)}</td>
                      <td className="tabular">{minutes(a.avgSec)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>
          </div>

          <section className="pca-card" aria-label="Call records">
            <div className="pca-card-head"><h2>Call records</h2></div>
            <div className="pca-table-wrap">
              <table className="cs-table pca-table">
                <thead><tr><th>Call ID</th><th>Time</th><th>Agent</th><th>Agent sentiment</th><th>Customer</th><th>Customer sentiment</th><th>Duration</th><th>Categories</th></tr></thead>
                <tbody>
                  {done.map(c => (
                    <tr key={c.id}>
                      <td><a className="btn-link" href={`#/pca/calls/${encodeURIComponent(c.id)}`}>{c.id.slice(0, 18)}…</a></td>
                      <td className="nowrap">{c.initiationTimestamp.replace('T', ' ').slice(0, 16)}</td>
                      <td>{c.agent}</td>
                      <td className="tabular">{signed(c.agentAvgSentiment)}</td>
                      <td>{c.callerPhoneNumber}</td>
                      <td className="tabular">{signed(c.callerAvgSentiment)}</td>
                      <td className="tabular">{c.duration}</td>
                      <td>{getDisplayCategories(c).join(', ')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        </>
      )}
    </div>
  );
}
