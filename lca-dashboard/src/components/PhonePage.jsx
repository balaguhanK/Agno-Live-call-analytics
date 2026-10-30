import React from 'react';
import { LANGUAGES } from '../asr/languages';
import { usePhoneLine, PHONE_STATUS_LABELS } from '../phone/phoneLineContext';
import { LiveCallView, PhoneMessages } from './PhoneCallUI';

// Phone: the agent's line settings. The line itself belongs to the whole
// session (see PhoneProvider), so calls also ring on the Calls page; a call
// answered here shows here, with live transcript and AI guidance.
export default function PhonePage({ onOpenCall }) {
  const phone = usePhoneLine();
  const { config, configError, status, phase } = phone;

  const header = (
    <div className="breadcrumb">
      <a>Call Analytics</a>
      <span className="breadcrumb-sep">›</span>
      <span>Phone</span>
    </div>
  );

  if (configError || (config && !config.enabled)) {
    return (
      <>
        {header}
        <div className="cs-container">
          <div className="empty-state" role="alert">
            <strong>The phone isn't available</strong>
            <span>{configError || config.reason}</span>
          </div>
        </div>
      </>
    );
  }
  if (!config) {
    return (
      <>
        {header}
        <div className="cs-container">
          <div className="empty-state" role="status">
            <span className="skeleton-bar" style={{ width: '240px' }} />
            <span>Loading…</span>
          </div>
        </div>
      </>
    );
  }

  const online = status === 'online';

  return (
    <>
      {header}

      <div className="cs-container">
        <div className="cs-container-header">
          <span className="cs-container-title">Phone</span>
          <span className={`engine-badge ${online ? 'phone-online' : ''}`} role="status">
            {PHONE_STATUS_LABELS[status]}
          </span>
        </div>
        <div className="cs-container-body">
          <div className="stream-form">
            <div className="stream-field">
              <span>Your line</span>
              <span className="phone-line">{config.sipUser}</span>
            </div>
            <div className="stream-field">
              <span>Customers dial</span>
              <span className="phone-line">{config.queueNumber}</span>
            </div>
            <label>
              Transcription language
              <select value={phone.language} onChange={e => phone.setLanguage(e.target.value)} disabled={phone.busy}>
                {LANGUAGES.map(l => <option key={l.browser} value={l.browser}>{l.label}</option>)}
              </select>
            </label>
          </div>

          <div className="stream-actions">
            {status === 'offline' || status === 'failed' ? (
              <button className="btn btn-primary" onClick={phone.goOnline}>● Go online</button>
            ) : (
              <button className="btn btn-normal" onClick={phone.goOffline} disabled={phone.busy}>Go offline</button>
            )}
            {phone.statusDetail && <span className="mic-name" role="status">{phone.statusDetail}</span>}
          </div>

          {phase === 'idle' && online && (
            <p className="muted" style={{ marginTop: '16px' }}>
              When a customer dials {config.queueNumber}, the call rings on the Calls page and here. Answer it to open the live call page with AI guidance.
            </p>
          )}

          <PhoneMessages />
        </div>
      </div>

      {phone.onCall && (
        <LiveCallView
          endedActions={phone.call && (
            <button className="btn btn-normal" onClick={() => { const id = phone.call.id; phone.dismiss(); onOpenCall(id); }}>Open in Calls</button>
          )}
        />
      )}
    </>
  );
}
