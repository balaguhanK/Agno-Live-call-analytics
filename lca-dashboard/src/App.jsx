import React, { useState } from 'react';
import CallDetail, { StatusBadge } from './components/CallDetail';
import PcaApp from './pca/PcaApp';
import LoginPage from './components/LoginPage';
import UsersPage from './components/UsersPage';
import StreamAudioPage from './components/StreamAudioPage';
import PhonePage from './components/PhonePage';
import { IncomingCallBanner, LiveCallView, PhoneLineStrip, PhoneMessages } from './components/PhoneCallUI';
import PhoneProvider from './phone/PhoneProvider';
import { usePhoneLine } from './phone/phoneLineContext';
import { getSession, signOut as endSession } from './auth/authService';
import { listCalls, downloadCalls, subscribeToCalls } from './api/calls';
import { UNAUTHORIZED_EVENT } from './api/client';
import { openInPca } from './api/pca';
import useLiveCall from './hooks/useLiveCall';
import useCategoryAlert from './hooks/useCategoryAlert';
import CategoryPill from './components/CategoryPill';
import { getDisplayCategories } from './pca/metrics';

// ── Icons ──────────────────────────────────────────────────
const IconUser = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
    <path d="M8 8a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm0 1c-2.7 0-8 1.34-8 4v1h16v-1c0-2.66-5.3-4-8-4z"/>
  </svg>
);
const IconSearch = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="#687078">
    <path d="M11.742 10.344a6.5 6.5 0 1 0-1.397 1.398l3.85 3.85a1 1 0 0 0 1.415-1.415l-3.868-3.833zM12 6.5a5.5 5.5 0 1 1-11 0 5.5 5.5 0 0 1 11 0z"/>
  </svg>
);
const IconRefresh = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
    <path d="M8 3a5 5 0 1 0 4.546 2.914.5.5 0 0 1 .908-.417A6 6 0 1 1 8 2v1z"/>
    <path d="M8 4.466V.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384L8.41 4.658A.25.25 0 0 1 8 4.466z"/>
  </svg>
);
const IconChevronLeft = () => (
  <svg width="10" height="10" viewBox="0 0 10 10" fill="currentColor">
    <path d="M6.5 1.5 L2.5 5 L6.5 8.5" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
  </svg>
);
const IconChevronRight = () => (
  <svg width="10" height="10" viewBox="0 0 10 10" fill="currentColor">
    <path d="M3.5 1.5 L7.5 5 L3.5 8.5" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
  </svg>
);
const IconMenu = () => (
  <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
    <path d="M3 5h14M3 10h14M3 15h14" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round"/>
  </svg>
);
const IconInfo = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="#687078">
    <path d="M8 15A7 7 0 1 1 8 1a7 7 0 0 1 0 14zm0 1A8 8 0 1 0 8 0a8 8 0 0 0 0 16z"/>
    <path d="M8.93 6.588l-2.29.287-.082.38.45.083c.294.07.352.176.288.469l-.738 3.468c-.194.897.105 1.319.808 1.319.545 0 1.178-.252 1.465-.598l.088-.416c-.2.176-.492.246-.686.246-.275 0-.375-.193-.304-.533L8.93 6.588zM9 4.5a1 1 0 1 1-2 0 1 1 0 0 1 2 0z"/>
  </svg>
);

// ── Utility Components ──────────────────────────────────────
// Characters of the summary shown in the list before the popover
const SUMMARY_PREVIEW_CHARS = 28;

// Plain text of a Markdown summary, without its section headings
const summaryText = (md = '') => md
  .replace(/\*\*(SUMMARY|DETAILS|ACTIONS)\*\*/gi, ' ')
  .replace(/_Generated automatically from the transcript\._/g, '')
  .replace(/[*_#>`]/g, '')
  .replace(/\s+/g, ' ')
  .trim();

// Fixed position just below an element, so a scrolling table can't clip it
const POPUP_GAP = 6;
const below = (el, alignRight = false) => {
  const r = el.getBoundingClientRect();
  return alignRight
    ? { top: r.bottom + POPUP_GAP, right: window.innerWidth - r.right }
    : { top: r.bottom + POPUP_GAP, left: r.left };
};

// Closes a popup when the page or the table scrolls under it
function useCloseOnScroll(open, close) {
  React.useEffect(() => {
    if (!open) return undefined;
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
    };
  }, [open, close]);
}

// The call's summary, cut short, with the full text in a popover on hover or focus
function SummaryCell({ call }) {
  const [pos, setPos] = useState(null);
  const open = pos !== null;
  const close = React.useCallback(() => setPos(null), []);
  useCloseOnScroll(open, close);
  const show = (e) => setPos(below(e.currentTarget.closest('.summary-cell')));
  const text = summaryText(call.summary?.text);
  if (!text) return <span className="sentiment-trend-flat">Not available</span>;
  const id = `summary-${call.id}`;
  return (
    <span className="summary-cell" onMouseEnter={show} onMouseLeave={close}>
      <button
        type="button"
        className="summary-preview"
        aria-describedby={open ? id : undefined}
        onFocus={show}
        onBlur={close}
        onClick={e => { e.stopPropagation(); if (open) close(); else show(e); }}
        onKeyDown={e => { if (e.key === 'Escape') close(); }}
      >
        {text.length > SUMMARY_PREVIEW_CHARS ? `${text.slice(0, SUMMARY_PREVIEW_CHARS)}…` : text}
      </button>
      {open && <span role="tooltip" id={id} className="summary-popover" style={pos}>{text}</span>}
    </span>
  );
}

// Per-row menu, as in the LCA demo: open the call in Post-Call Analytics
function RowActions({ call }) {
  const [pos, setPos] = useState(null);
  const open = pos !== null;
  const setOpen = React.useCallback((next) => { if (!next) setPos(null); }, []);
  const close = React.useCallback(() => setPos(null), []);
  useCloseOnScroll(open, close);
  const done = call.status === 'Done';
  return (
    <span className="row-actions" onClick={e => e.stopPropagation()}>
      <button
        type="button"
        className="row-actions-btn"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Actions for call ${call.id}`}
        onClick={e => { if (open) close(); else setPos(below(e.currentTarget, true)); }}
        onBlur={e => { if (!e.currentTarget.parentElement.contains(e.relatedTarget)) setOpen(false); }}
      >
        ☰ <span aria-hidden="true">{open ? '▴' : '▾'}</span>
      </button>
      {open && (
        <span role="menu" className="row-actions-menu" style={pos}>
          <button
            type="button"
            role="menuitem"
            disabled={!done}
            title={done ? undefined : 'Available when the call ends'}
            onClick={() => { setOpen(false); openInPca(call.id); }}
            onBlur={e => { if (!e.currentTarget.parentElement.parentElement.contains(e.relatedTarget)) setOpen(false); }}
          >
            Open in PCA <span aria-hidden="true">↗</span>
          </button>
        </span>
      )}
    </span>
  );
}

// Sentiment is post-call analysis, so live calls don't have it yet
function SentimentCell({ sentiment, status }) {
  if (status === 'In Progress') return <span className="sentiment-trend-flat">Pending</span>;
  return (
    <span>{sentiment}</span>
  );
}

function TrendCell({ trend, status }) {
  if (status === 'In Progress') return <span className="sentiment-trend-flat">Pending</span>;
  if (trend === 'Down') return <span className="sentiment-trend-down">↘ Down</span>;
  if (trend === 'Up') return <span className="sentiment-trend-up">↗ Up</span>;
  return <span className="sentiment-trend-flat">→ Flat</span>;
}

function formatTimestamp(ts) {
  return ts.replace('T', ' ').replace('Z', '');
}

const shortId = (id) => (id.length > 16 ? `${id.slice(0, 16)}...` : id);

// ── Calls list settings ─────────────────────────────────────
const PAGE_SIZE = 25;
const SEARCH_DEBOUNCE_MS = 300;
// New calls arrive in bursts; refetch at most this often
const LIVE_REFRESH_MS = 1000;
const LOAD_WINDOWS = [
  { hours: 2, label: 'Load: 2 hrs' },
  { hours: 4, label: 'Load: 4 hrs' },
  { hours: 8, label: 'Load: 8 hrs' },
  { hours: 24, label: 'Load: 24 hrs' },
  { hours: 168, label: 'Load: 7 days' },
  { hours: 0, label: 'Load: all' },
];
const DEFAULT_SORT = { key: 'initiationTimestamp', order: 'desc' };
// sortKey must match the backend's store.SortKeys; null means not sortable
const COLUMNS = [
  { sortKey: 'id', label: 'Call ID' },
  { sortKey: 'alertCount', label: '', title: 'Alerts', narrow: true },
  { sortKey: 'agent', label: 'Agent' },
  { sortKey: 'initiationTimestamp', label: 'Initiation Timestamp' },
  { sortKey: null, label: 'Summary' },
  { sortKey: 'callerPhoneNumber', label: 'Caller Phone ...', title: 'Caller Phone Number' },
  { sortKey: 'status', label: 'Status' },
  { sortKey: 'callerSentiment', label: 'Caller Sen...', title: 'Caller Sentiment' },
  { sortKey: 'callerSentimentTrend', label: 'Caller Sen...', title: 'Caller Sentiment Trend' },
  { sortKey: 'duration', label: 'Duration' },
  { sortKey: null, label: 'Categories' },
  { sortKey: null, label: '', title: 'Actions', narrow: true },
];

function useDebounced(value, ms) {
  const [debounced, setDebounced] = useState(value);
  React.useEffect(() => {
    const t = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return debounced;
}

function SkeletonRows({ count }) {
  return Array.from({ length: count }, (_, i) => (
    <tr key={i} className="skeleton-row" aria-hidden="true">
      <td><span className="skeleton-bar" /></td>
      {COLUMNS.map((_, j) => <td key={j}><span className="skeleton-bar" /></td>)}
    </tr>
  ));
}

function Pager({ page, pages, onChange }) {
  return (
    <div className="cs-page-btn">
      <button disabled={page <= 1} onClick={() => onChange(page - 1)} aria-label="Previous page"><IconChevronLeft /></button>
      <div className="cs-page-current" aria-current="page" title={`Page ${page} of ${pages}`}>{page}</div>
      <button disabled={page >= pages} onClick={() => onChange(page + 1)} aria-label="Next page"><IconChevronRight /></button>
    </div>
  );
}

// ── Calls List View ─────────────────────────────────────────
// Search, time window, sorting and paging all run on the backend
function CallsListView({ onSelectCall }) {
  const alertPattern = useCategoryAlert();
  const [search, setSearch] = useState('');
  const query = useDebounced(search.trim(), SEARCH_DEBOUNCE_MS);
  const [hours, setHours] = useState(LOAD_WINDOWS[0].hours);
  const [sort, setSort] = useState(DEFAULT_SORT);
  const [page, setPage] = useState(1);
  const [reloadKey, setReloadKey] = useState(0);
  // key records which request produced the data, so loading is derived from it
  const [data, setData] = useState({ key: null, items: [], total: 0 });
  const [failedKey, setFailedKey] = useState(null);
  const [error, setError] = useState('');
  const [selectedIds, setSelectedIds] = useState([]);
  const [downloading, setDownloading] = useState(false);
  const itemsRef = React.useRef([]);

  React.useEffect(() => { itemsRef.current = data.items; }, [data.items]);

  const reload = () => setReloadKey(k => k + 1);

  const requestKey = [query, hours, sort.key, sort.order, page, reloadKey].join('|');
  const status = data.key === requestKey ? 'ready' : failedKey === requestKey ? 'error' : 'loading';

  React.useEffect(() => {
    let ignore = false;
    listCalls({ hours, q: query, sort: sort.key, order: sort.order, page, pageSize: PAGE_SIZE })
      .then(res => {
        if (ignore) return;
        setData({ key: requestKey, items: res.items, total: res.total });
        setError('');
        // Calls that dropped out of the window may leave the page past the end
        const pages = Math.max(1, Math.ceil(res.total / PAGE_SIZE));
        if (page > pages) setPage(pages);
      })
      .catch(err => {
        if (ignore) return;
        setError(err.message);
        setFailedKey(requestKey);
      });
    return () => { ignore = true; };
  }, [requestKey, query, hours, sort.key, sort.order, page]);

  // Update visible rows in place; refetch when a call we don't show yet arrives
  React.useEffect(() => {
    let timer;
    const unsubscribe = subscribeToCalls(event => {
      if (event.type !== 'call') return;
      if (itemsRef.current.some(c => c.id === event.call.id)) {
        setData(d => ({ ...d, items: d.items.map(c => c.id === event.call.id ? event.call : c) }));
      } else if (!timer) {
        timer = setTimeout(() => { timer = undefined; setReloadKey(k => k + 1); }, LIVE_REFRESH_MS);
      }
    });
    return () => { clearTimeout(timer); unsubscribe(); };
  }, []);

  // Any filter change starts again from page 1
  const changeSearch = (value) => { setSearch(value); setPage(1); };
  const changeHours = (value) => { setHours(Number(value)); setPage(1); setSelectedIds([]); };
  const changeSort = (key) => {
    setSort(s => s.key === key ? { key, order: s.order === 'asc' ? 'desc' : 'asc' } : { key, order: 'asc' });
    setPage(1);
  };

  const download = async () => {
    setDownloading(true);
    try {
      await downloadCalls(selectedIds.length > 0
        ? { ids: selectedIds.join(','), sort: sort.key, order: sort.order }
        : { hours, q: query, sort: sort.key, order: sort.order });
    } catch (err) {
      setError(err.message);
    } finally {
      setDownloading(false);
    }
  };

  const toggleSelect = (id) => {
    setSelectedIds(prev =>
      prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id]
    );
  };

  const { items, total } = data;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const allChecked = items.length > 0 && items.every(c => selectedIds.includes(c.id));
  const windowLabel = LOAD_WINDOWS.find(w => w.hours === hours)?.label.replace('Load: ', '') || '';
  const firstLoad = status === 'loading' && items.length === 0;

  return (
    <>
      {/* Breadcrumb */}
      <div className="breadcrumb">
        <a>Call Analytics</a>
        <span className="breadcrumb-sep">›</span>
        <span>Calls</span>
      </div>

      <div style={{ margin: '0 24px 16px' }}>
        <div className="cs-table-wrapper">
          {/* Toolbar */}
          <div className="cs-table-toolbar">
            <div className="cs-search-input">
              <IconSearch />
              <input
                placeholder="Find calls"
                aria-label="Find calls by ID, agent, phone number, status, sentiment or category"
                value={search}
                onChange={e => changeSearch(e.target.value)}
              />
            </div>

            <div style={{ flex: 1 }} />

            {/* Right toolbar */}
            <div className="toolbar-right">
              <select className="load-dropdown" aria-label="Time window" value={hours} onChange={e => changeHours(e.target.value)}>
                {LOAD_WINDOWS.map(w => <option key={w.hours} value={w.hours}>{w.label}</option>)}
              </select>
              <button className="btn-icon" title="Refresh" aria-label="Refresh calls" onClick={reload} disabled={status === 'loading'}>
                <IconRefresh />
              </button>
              <button
                className="btn-icon"
                title={selectedIds.length > 0 ? `Download ${selectedIds.length} selected as CSV` : 'Download as CSV'}
                aria-label="Download calls as CSV"
                onClick={download}
                disabled={downloading || total === 0}
              >
                <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
                  <path d="M.5 9.9a.5.5 0 0 1 .5.5v2.5a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-2.5a.5.5 0 0 1 1 0v2.5a2 2 0 0 1-2 2H2a2 2 0 0 1-2-2v-2.5a.5.5 0 0 1 .5-.5z"/>
                  <path d="M7.646 11.854a.5.5 0 0 0 .708 0l3-3a.5.5 0 0 0-.708-.708L8.5 10.293V1.5a.5.5 0 0 0-1 0v8.793L5.354 8.146a.5.5 0 1 0-.708.708l3 3z"/>
                </svg>
              </button>
            </div>
          </div>

          {error && (
            <div className="users-message error" role="alert" style={{ margin: '8px 16px' }}>
              {error}
              <span style={{ flex: 1 }} />
              <button onClick={reload}>Retry</button>
              <button onClick={() => setError('')} aria-label="Dismiss">✕</button>
            </div>
          )}

          {/* Count + pagination header */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '8px 16px', borderBottom: '1px solid #eaeded', background: '#fff' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <span style={{ fontSize: '18px', fontWeight: '700' }}>Calls</span>
              <span style={{ fontSize: '14px', color: '#687078' }} aria-live="polite">({total})</span>
            </div>
            <Pager page={page} pages={pages} onChange={setPage} />
          </div>

          {/* Table */}
          <table className="cs-table" aria-busy={status === 'loading'}>
            <thead>
              <tr>
                <th style={{ width: '36px', padding: '10px 12px' }}>
                  <input
                    type="checkbox"
                    className="cs-checkbox"
                    aria-label="Select all calls on this page"
                    checked={allChecked}
                    onChange={() => {
                      const pageIds = items.map(c => c.id);
                      if (allChecked) setSelectedIds(prev => prev.filter(id => !pageIds.includes(id)));
                      else setSelectedIds(prev => [...new Set([...prev, ...pageIds])]);
                    }}
                  />
                </th>
                {COLUMNS.map(col => {
                  const name = col.title || col.label;
                  const active = col.sortKey && sort.key === col.sortKey;
                  const ariaSort = active ? (sort.order === 'asc' ? 'ascending' : 'descending') : undefined;
                  return (
                    <th
                      key={name}
                      className={col.sortKey ? 'sortable' : undefined}
                      style={col.narrow ? { width: '28px', padding: '10px 4px' } : undefined}
                      aria-sort={ariaSort}
                      title={name}
                    >
                      {col.sortKey ? (
                        <button className="th-sort" onClick={() => changeSort(col.sortKey)} aria-label={`Sort by ${name}`}>
                          {col.label} {active ? (sort.order === 'asc' ? '▲' : '▼') : '▽'}
                        </button>
                      ) : col.label}
                    </th>
                  );
                })}
              </tr>
            </thead>
            <tbody style={status === 'loading' && !firstLoad ? { opacity: 0.6 } : undefined}>
              {firstLoad && <SkeletonRows count={3} />}
              {status !== 'loading' && items.length === 0 && (
                <tr className="empty-row">
                  <td colSpan={COLUMNS.length + 1}>
                    <div className="empty-state">
                      <strong>{query ? `No calls match “${query}”` : 'No calls yet'}</strong>
                      <span>
                        {query
                          ? 'Try a different search or a longer time window.'
                          : hours > 0
                            ? `No calls in the last ${windowLabel}. Calls appear here as soon as they start.`
                            : 'Calls appear here as soon as they start.'}
                      </span>
                    </div>
                  </td>
                </tr>
              )}
              {items.map(call => (
                <tr
                  key={call.id}
                  className={selectedIds.includes(call.id) ? 'selected' : ''}
                  onClick={() => onSelectCall(call)}
                >
                  <td style={{ padding: '10px 12px' }} onClick={e => { e.stopPropagation(); toggleSelect(call.id); }}>
                    <input type="checkbox" className="cs-checkbox" aria-label={`Select call ${call.id}`} checked={selectedIds.includes(call.id)} onChange={() => {}} />
                  </td>
                  <td>
                    <button className="btn-link" onClick={e => { e.stopPropagation(); onSelectCall(call); }}>
                      {shortId(call.id)}
                    </button>
                  </td>
                  <td style={{ padding: '10px 4px' }}>
                    {call.alertCount > 0 && <span className="alert-badge" title={`${call.alertCount} alert${call.alertCount > 1 ? 's' : ''}`}>{call.alertCount}</span>}
                  </td>
                  <td>{call.agent}</td>
                  <td style={{ whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>
                    {formatTimestamp(call.initiationTimestamp)}
                  </td>
                  <td><SummaryCell call={call} /></td>
                  <td style={{ color: '#0073bb' }}>{call.callerPhoneNumber}</td>
                  <td><StatusBadge status={call.status} /></td>
                  <td><SentimentCell sentiment={call.callerSentiment} status={call.status} /></td>
                  <td><TrendCell trend={call.callerSentimentTrend} status={call.status} /></td>
                  <td style={{ fontVariantNumeric: 'tabular-nums' }}>{call.duration}</td>
                  <td className="category-cell">
                    {getDisplayCategories(call).map(cat => (
                      <CategoryPill key={cat} name={cat} alert={alertPattern.test(cat)} />
                    ))}
                  </td>
                  <td style={{ padding: '10px 8px' }}><RowActions call={call} /></td>
                </tr>
              ))}
            </tbody>
          </table>

          {/* Footer */}
          <div className="cs-table-footer">
            <span>{selectedIds.length > 0 ? `${selectedIds.length} call${selectedIds.length > 1 ? 's' : ''} selected` : `${total} call${total === 1 ? '' : 's'}`}</span>
            <Pager page={page} pages={pages} onChange={setPage} />
          </div>
        </div>
      </div>
    </>
  );
}

// ── Call Detail View ────────────────────────────────────────
function CallDetailView({ callId, onBack, onError }) {
  const { call, error } = useLiveCall(callId);
  React.useEffect(() => { if (error) onError(error); }, [error, onError]);
  return (
    <>
      <div className="breadcrumb">
        <a onClick={onBack} style={{ cursor: 'pointer' }}>Call Analytics</a>
        <span className="breadcrumb-sep">›</span>
        <a onClick={onBack} style={{ cursor: 'pointer' }}>Calls</a>
        <span className="breadcrumb-sep">›</span>
        <span style={{ color: '#16191f' }}>{callId}</span>
      </div>
      {call ? <CallDetail call={call} /> : (
        <div className="empty-state" role="status" style={{ padding: '48px 24px' }}>
          <span className="skeleton-bar" style={{ width: '240px' }} />
          <span>Loading call…</span>
        </div>
      )}
    </>
  );
}

// ── Live Call View ──────────────────────────────────────────
// The call the agent answered, shown in place of the calls list
function LiveCallPage({ onShowList }) {
  const phone = usePhoneLine();
  const backToList = () => { phone.dismiss(); onShowList(); };
  return (
    <>
      <div className="breadcrumb">
        <a>Call Analytics</a>
        <span className="breadcrumb-sep">›</span>
        <a onClick={onShowList} style={{ cursor: 'pointer' }}>Calls</a>
        <span className="breadcrumb-sep">›</span>
        <span style={{ color: '#16191f' }}>{phone.call?.id || 'Live call'}</span>
      </div>
      <div style={{ margin: '0 24px' }}><PhoneMessages /></div>
      <LiveCallView endedActions={<button className="btn btn-normal" onClick={backToList}>Back to calls</button>} />
    </>
  );
}

// Calls page: the live call once one is answered, otherwise the list
function CallsPage({ liveOpen, setLiveOpen, onSelectCall }) {
  const phone = usePhoneLine();
  if (liveOpen && phone.onCall) return <LiveCallPage onShowList={() => setLiveOpen(false)} />;
  return (
    <>
      <PhoneLineStrip onReturnToCall={() => setLiveOpen(true)} />
      <CallsListView onSelectCall={onSelectCall} />
    </>
  );
}

// ── Main App ────────────────────────────────────────────────
export default function App() {
  const [user, setUser] = useState(null);
  const [checkingSession, setCheckingSession] = useState(true);
  const [selectedCallId, setSelectedCallId] = useState(null);
  const [loadError, setLoadError] = useState('');
  // Post-Call Analytics runs in its own tab at #/pca
  const [hash, setHash] = useState(window.location.hash);
  const [activeSidebarItem, setActiveSidebarItem] = useState('Calls');
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [bannerOpen, setBannerOpen] = useState(true);
  // Whether the Calls page shows the answered phone call instead of the list
  const [liveCallOpen, setLiveCallOpen] = useState(false);

  const closeCall = () => setSelectedCallId(null);

  React.useEffect(() => {
    const onHash = () => setHash(window.location.hash);
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  const failCall = React.useCallback((message) => {
    setLoadError(message);
    setSelectedCallId(null);
  }, []);

  const resetToSignIn = () => {
    closeCall();
    setLoadError('');
    setUser(null);
  };

  // Restore the session from the cookie on first load
  React.useEffect(() => {
    getSession()
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setCheckingSession(false));
  }, []);

  // Return to sign-in whenever the API rejects the session
  React.useEffect(() => {
    window.addEventListener(UNAUTHORIZED_EVENT, resetToSignIn);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, resetToSignIn);
  }, []);

  const openCall = (call) => {
    setLoadError('');
    setSelectedCallId(call.id);
  };

  const signIn = (session) => {
    setActiveSidebarItem('Calls');
    setUser(session);
  };

  const signOut = () => {
    endSession().catch(() => { /* server unreachable; local state is reset anyway */ });
    resetToSignIn();
  };

  // Sign out automatically when the session expires. Poll rather than one long
  // setTimeout: delays over ~24.8 days overflow and fire immediately.
  React.useEffect(() => {
    if (!user) return;
    const timer = setInterval(() => {
      if (Date.now() >= user.expiresAt) signOut();
    }, 60 * 1000);
    return () => clearInterval(timer);
  }, [user]);

  if (checkingSession) return null;
  if (!user) return <LoginPage onSignIn={signIn} />;
  if (hash.startsWith('#/pca')) return <PcaApp user={user} hash={hash} onSignOut={signOut} />;

  const isAdmin = user.role === 'admin';
  const navItems = isAdmin ? ['Calls', 'Phone', 'Stream Audio', 'Users'] : ['Calls', 'Phone', 'Stream Audio'];
  const pcaHref = `${window.location.pathname}#/pca`;

  // An answered call opens on the Calls page, unless it was answered on Phone
  const showAnsweredCall = () => {
    if (activeSidebarItem === 'Phone') return;
    setActiveSidebarItem('Calls');
    closeCall();
    setLiveCallOpen(true);
  };

  return (
    <PhoneProvider>
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
      {/* ── Top Navigation ── */}
      <nav className="top-nav">
        <div style={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
          <button style={{ background: 'none', border: 'none', color: '#d5dbdb', cursor: 'pointer', display: 'flex' }}
            onClick={() => setSidebarOpen(s => !s)}>
            <IconMenu />
          </button>
          <span className="top-nav-brand">Live Call Analytics with Agent Assist</span>
        </div>
        <div className="top-nav-user">
          <IconUser />
          <span title={user.email}>{user.name}</span>
          <span className="role-pill">{user.role === 'admin' ? 'Admin' : 'Agent'}</span>
          <button className="sign-out-btn" onClick={signOut}>Sign out</button>
        </div>
      </nav>

      {/* ── App Shell ── */}
      <div className="app-shell">
        {/* ── Sidebar ── */}
        {sidebarOpen && (
          <aside className="sidebar">
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 12px 12px 20px' }}>
              <span style={{ fontWeight: '700', fontSize: '15px', color: '#16191f' }}>Call Analytics</span>
              <button className="sidebar-close" onClick={() => setSidebarOpen(false)}>✕</button>
            </div>

            {/* Nav */}
            {navItems.map(label => (
              <div
                key={label}
                className={`sidebar-nav-item ${activeSidebarItem === label ? 'active' : ''}`}
                onClick={() => {
                  setActiveSidebarItem(label);
                  closeCall();
                  setLiveCallOpen(label === 'Calls');
                }}
              >
                {label}
              </div>
            ))}

            <div className="sidebar-section-title">Post-call analytics</div>
            <a className="sidebar-nav-item sidebar-link" href={pcaHref} target="_blank" rel="noopener noreferrer">
              Open PCA <span aria-hidden="true">↗</span><span className="visually-hidden"> (opens in a new tab)</span>
            </a>
          </aside>
        )}

        {/* ── Main Content ── */}
        <div className="main-content">
          <IncomingCallBanner onAnswer={showAnsweredCall} />

          {/* Info Banner (shown once) */}
          {bannerOpen && !selectedCallId && activeSidebarItem === 'Calls' && (
            <div className="info-banner">
              <IconInfo />
              Welcome, {user.name}. {isAdmin ? 'You are viewing all calls.' : 'You are viewing the calls you handled.'}
              <div style={{ flex: 1 }} />
              <button style={{ background: 'none', border: 'none', color: '#fff', cursor: 'pointer', fontSize: '16px' }} onClick={() => setBannerOpen(false)} aria-label="Dismiss">✕</button>
            </div>
          )}

          {loadError && (
            <div className="users-message error" style={{ margin: '16px 24px 0' }}>
              {loadError}
              <button onClick={() => setLoadError('')}>✕</button>
            </div>
          )}

          {activeSidebarItem === 'Users' && isAdmin ? (
            <UsersPage currentUser={user} />
          ) : activeSidebarItem === 'Phone' ? (
            <PhonePage onOpenCall={(id) => { setActiveSidebarItem('Calls'); openCall({ id }); }} />
          ) : activeSidebarItem === 'Stream Audio' ? (
            <StreamAudioPage
              user={user}
              onOpenCall={(id) => { setActiveSidebarItem('Calls'); openCall({ id }); }}
            />
          ) : selectedCallId ? (
            <CallDetailView key={selectedCallId} callId={selectedCallId} onBack={closeCall} onError={failCall} />
          ) : (
            <CallsPage liveOpen={liveCallOpen} setLiveOpen={setLiveCallOpen} onSelectCall={openCall} />
          )}
        </div>
      </div>
    </div>
    </PhoneProvider>
  );
}
