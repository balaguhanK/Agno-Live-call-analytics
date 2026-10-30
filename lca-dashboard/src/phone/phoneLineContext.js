import { createContext, useContext } from 'react';

// The agent's phone line, shared by every page so a call can ring anywhere
export const PhoneLineContext = createContext(null);

/**
 * { config, configError, status, statusDetail, phase, caller, muted, elapsed,
 *   call, partial, error, warning, language, busy, onCall,
 *   setLanguage, goOnline, goOffline, answer, decline, hangup, toggleMute,
 *   dismiss, clearError, clearWarning }
 * phase is idle | ringing | active | ending | ended.
 */
export const usePhoneLine = () => useContext(PhoneLineContext);

export const PHONE_STATUS_LABELS = {
  offline: 'Offline',
  connecting: 'Connecting…',
  online: 'Online, waiting for calls',
  failed: 'Sign-in to the phone system failed',
};
