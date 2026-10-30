import React, { useEffect, useState } from 'react';
import { listCalls } from '../api/calls';
import { getDisplayCategories } from './metrics';

const PAGE_SIZE = 25;
const SEARCH_DEBOUNCE_MS = 300;
const WINDOWS = [
  { hours: 24, label: 'Last 24 hours' },
  { hours: 168, label: 'Last 7 days' },
  { hours: 720, label: 'Last 30 days' },
  { hours: 0, label: 'All time' },
];
const TREND_ICONS = { Up: '↗', Down: '↘', Flat: '→' };

const formatTimestamp = (ts = '') => ts.replace('T', ' ').replace('Z', '').slice(0, 19);

/** Post-Call Analytics call list: finished calls, newest first. */
export default function PcaCallList() {
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [hours, setHours] = useState(WINDOWS[1].hours);
  const [page, setPage] = useState(1);
  const [result, setResult] = useState({ key: null, items: [], total: 0, error: '' });
  const requestKey = `${hours}|${query}|${page}`;
  const state = result.key === requestKey
    ? { ...result, status: result.error ? 'error' : 'ready' }
    : { ...result, status: 'loading', error: '' };

  useEffect(() => {
    const t = setTimeout(() => { setQuery(search.trim()); setPage(1); }, SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [search]);

  useEffect(() => {
    let ignore = false;
    listCalls({ hours, q: query, sort: 'initiationTimestamp', order: 'desc', page, pageSize: PAGE_SIZE })
      .then(res => { if (!ignore) setResult({ key: requestKey, items: res.items, total: res.total, error: '' }); })
      .catch(err => { if (!ignore) setResult({ key: requestKey, items: [], total: 0, error: err.message }); });
    return () => { ignore = true; };
  }, [requestKey, hours, query, page]);

  const pages = Math.max(1, Math.ceil(state.total / PAGE_SIZE));

  return (
    <div className="pca-page">
      <section className="pca-card">
        <div className="pca-toolbar">
          <input
            className="pca-search"
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Search by call ID, agent, phone number or category"
            aria-label="Search calls"
          />
          <select className="load-dropdown" value={hours} onChange={e => { setHours(Number(e.target.value)); setPage(1); }} aria-label="Time window">
            {WINDOWS.map(w => <option key={w.hours} value={w.hours}>{w.label}</option>)}
          </select>
          <span className="muted" aria-live="polite">{state.total} call{state.total === 1 ? '' : 's'}</span>
        </div>

        {state.status === 'error' && <div className="users-message error" role="alert">{state.error}</div>}

        <div className="pca-table-wrap">
          <table className="cs-table pca-table" aria-busy={state.status === 'loading'}>
            <thead>
              <tr>
                <th>Call ID</th><th>Agent</th><th>Timestamp</th><th>Customer</th><th>Duration</th>
                <th>Customer sentiment</th><th>Agent sentiment</th><th>Categories</th>
              </tr>
            </thead>
            <tbody>
              {state.status === 'loading' && state.items.length === 0 && Array.from({ length: 3 }, (_, i) => (
                <tr key={i} className="skeleton-row" aria-hidden="true">{Array.from({ length: 8 }, (_, j) => <td key={j}><span className="skeleton-bar" /></td>)}</tr>
              ))}
              {state.status === 'ready' && state.items.length === 0 && (
                <tr className="empty-row"><td colSpan={8}>
                  <div className="empty-state"><strong>No calls found</strong><span>Try a longer time window or a different search.</span></div>
                </td></tr>
              )}
              {state.items.map(c => {
                const done = c.status === 'Done';
                return (
                  <tr key={c.id}>
                    <td>
                      {done
                        ? <a className="btn-link" href={`#/pca/calls/${encodeURIComponent(c.id)}`}>{c.id.slice(0, 18)}…</a>
                        : <span title="Available when the call ends">{c.id.slice(0, 18)}… <span className="muted">(in progress)</span></span>}
                    </td>
                    <td>{c.agent}</td>
                    <td className="nowrap">{formatTimestamp(c.initiationTimestamp)}</td>
                    <td>{c.callerPhoneNumber}</td>
                    <td className="tabular">{c.duration}</td>
                    <td>{done ? <>{c.callerSentiment} <span aria-hidden="true">{TREND_ICONS[c.callerSentimentTrend]}</span> <span className="tabular">{c.callerAvgSentiment.toFixed(2)}</span></> : '—'}</td>
                    <td className="tabular">{done ? <>{c.agentAvgSentiment.toFixed(2)} <span aria-hidden="true">{TREND_ICONS[c.agentSentimentTrend]}</span></> : '—'}</td>
                    <td>{getDisplayCategories(c).join(', ')}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        <div className="pca-pager">
          <button type="button" className="btn btn-normal" disabled={page <= 1} onClick={() => setPage(p => p - 1)}>Previous</button>
          <span aria-current="page">Page {page} of {pages}</span>
          <button type="button" className="btn btn-normal" disabled={page >= pages} onClick={() => setPage(p => p + 1)}>Next</button>
        </div>
      </section>
    </div>
  );
}
