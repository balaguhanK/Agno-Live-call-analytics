import React from 'react';
import PcaCallList from './PcaCallList';
import PcaCallDetail from './PcaCallDetail';
import PcaDashboard from './PcaDashboard';

// Reads #/pca, #/pca/dashboard or #/pca/calls/<id>
function parseRoute(hash) {
  const path = hash.replace(/^#\/pca\/?/, '');
  if (path.startsWith('calls/')) return { page: 'call', callId: decodeURIComponent(path.slice('calls/'.length)) };
  if (path === 'dashboard') return { page: 'dashboard' };
  return { page: 'list' };
}

/**
 * Post-Call Analytics: a separate app, opened in its own browser tab from
 * the live dashboard, for reviewing finished calls in depth.
 */
export default function PcaApp({ user, hash, onSignOut }) {
  const route = parseRoute(hash);
  const liveHref = `${window.location.pathname}#/`;

  React.useEffect(() => {
    const previous = document.title;
    document.title = 'Post-Call Analytics';
    return () => { document.title = previous; };
  }, []);

  const crumbs = [{ label: 'Home', href: '#/pca' }];
  if (route.page === 'list') crumbs.push({ label: 'Call list' });
  if (route.page === 'call') crumbs.push({ label: 'Call list', href: '#/pca' }, { label: 'Call details' });
  if (route.page === 'dashboard') crumbs.push({ label: 'Dashboard' });
  const title = { list: 'Call list', call: 'Call details', dashboard: 'Dashboard' }[route.page];

  return (
    <div className="pca-app">
      <header className="pca-top">
        <span className="pca-brand">Post-Call Analytics</span>
        <nav aria-label="Post-call analytics">
          <a href="#/pca" aria-current={route.page === 'list' ? 'page' : undefined}>Call list</a>
          <a href="#/pca/dashboard" aria-current={route.page === 'dashboard' ? 'page' : undefined}>Dashboard</a>
          <a href={liveHref}>Live Call Analytics</a>
        </nav>
        <span className="pca-user">
          <span title={user.email}>{user.name}</span>
          <button type="button" className="sign-out-btn" onClick={onSignOut}>Sign out</button>
        </span>
      </header>
      <div className="pca-hero">
        <nav className="pca-crumbs" aria-label="Breadcrumb">
          {crumbs.map((c, i) => (
            <React.Fragment key={c.label}>
              {i > 0 && <span aria-hidden="true">›</span>}
              {c.href ? <a href={c.href}>{c.label}</a> : <span aria-current="page">{c.label}</span>}
            </React.Fragment>
          ))}
        </nav>
        <h1>{title}</h1>
      </div>
      <main>
        {route.page === 'call' && <PcaCallDetail key={route.callId} callId={route.callId} />}
        {route.page === 'dashboard' && <PcaDashboard />}
        {route.page === 'list' && <PcaCallList />}
      </main>
    </div>
  );
}
