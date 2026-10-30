import React from 'react';
import CallDetail from './CallDetail';
import { usePhoneLine, PHONE_STATUS_LABELS } from '../phone/phoneLineContext';

const formatElapsed = (sec) => {
  const s = Math.floor(sec);
  return `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
};

// A customer call ringing the agent, on whichever page they are on
export function IncomingCallBanner({ onAnswer }) {
  const phone = usePhoneLine();
  if (phone?.phase !== 'ringing') return null;
  return (
    <div className="incoming-call-banner">
      <div className="phone-ringing ringing" role="alert" aria-live="assertive">
        <span className="ring-icon" aria-hidden="true">📞</span>
        <div>
          <strong>Incoming call</strong>
          <span className="phone-caller">{phone.caller}</span>
        </div>
        <button className="btn btn-primary phone-answer" onClick={() => { phone.answer(); onAnswer?.(); }} autoFocus>Answer</button>
        <button className="btn btn-normal" onClick={phone.decline}>Decline</button>
      </div>
    </div>
  );
}

// Errors and notices about the current or last call
export function PhoneMessages() {
  const phone = usePhoneLine();
  return (
    <>
      {phone.error && (
        <div className="users-message error" role="alert">
          {phone.error}
          <button onClick={phone.clearError} aria-label="Dismiss">✕</button>
        </div>
      )}
      {phone.warning && (
        <div className="users-message error" role="alert">
          {phone.warning}
          <button onClick={phone.clearWarning} aria-label="Dismiss">✕</button>
        </div>
      )}
    </>
  );
}

// The answered call: hang-up bar, live transcript and AI guidance.
// endedActions are the buttons shown once the call is over.
export function LiveCallView({ endedActions }) {
  const phone = usePhoneLine();
  const { phase, call } = phone;
  const ended = phase === 'ended';

  const callBar = (
    <div className={`cs-container phone-call-bar${ended ? ' ended' : ''}`} role="status">
      <div>
        <strong>{ended ? 'Call ended with' : 'On a call with'}</strong>
        <span className="phone-caller">{phone.caller}</span>
      </div>
      {!ended && <span className="stream-status"><span className="live-dot" aria-hidden="true" /> {formatElapsed(phone.elapsed)}</span>}
      <span style={{ flex: 1 }} />
      {ended ? endedActions : (
        <>
          <button className="btn btn-normal" onClick={phone.toggleMute} disabled={phase !== 'active'} aria-pressed={phone.muted}>
            {phone.muted ? 'Unmute' : 'Mute'}
          </button>
          <button className="btn btn-primary stream-stop" onClick={phone.hangup} disabled={phase !== 'active'}>
            {phase === 'ending' ? 'Ending…' : 'End call'}
          </button>
        </>
      )}
    </div>
  );

  if (call) {
    return (
      <CallDetail
        call={call}
        partialText={phase === 'active' ? phone.partial.text : ''}
        partialSpeaker={phone.partial.speaker}
        controls={callBar}
      />
    );
  }
  return (
    <>
      {callBar}
      <div className="cs-container">
        <div className="empty-state" role="status">
          <span className="skeleton-bar" style={{ width: '240px' }} />
          <span>Starting the call page…</span>
        </div>
      </div>
    </>
  );
}

// One line above the calls list: is the agent's phone ready to ring, and a
// way back to the call they are on
export function PhoneLineStrip({ onReturnToCall }) {
  const phone = usePhoneLine();
  if (!phone?.config?.enabled) return null;
  const online = phone.status === 'online';
  return (
    <div className="phone-line-strip" role="status">
      <span className={`engine-badge ${online ? 'phone-online' : ''}`}>
        <span aria-hidden="true">{online ? '● ' : '○ '}</span>
        Phone: {PHONE_STATUS_LABELS[phone.status]}
      </span>
      <span className="muted">Line {phone.config.sipUser} · customers dial {phone.config.queueNumber}</span>
      {phone.statusDetail && <span className="muted">{phone.statusDetail}</span>}
      <span style={{ flex: 1 }} />
      {phone.onCall && (
        <button className="btn btn-primary" onClick={onReturnToCall}>
          {phone.phase === 'ended' ? 'View last call' : `Return to call with ${phone.caller}`}
        </button>
      )}
      {phone.status === 'offline' || phone.status === 'failed' ? (
        <button className="btn btn-normal" onClick={phone.goOnline}>Go online</button>
      ) : (
        <button className="btn btn-normal" onClick={phone.goOffline} disabled={phone.busy}>Go offline</button>
      )}
    </div>
  );
}
