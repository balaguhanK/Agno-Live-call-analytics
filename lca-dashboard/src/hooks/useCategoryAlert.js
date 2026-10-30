import { useEffect, useState } from 'react';
import { getSettings } from '../api/settings';

// Until the server's setting loads (or if it can't), its default: a manager
// escalation is the only alert, as in LCA's sample
const DEFAULT_ALERT = /ManagerEscalation/;

const toRegExp = (pattern) => {
  try {
    return new RegExp(pattern);
  } catch {
    return DEFAULT_ALERT;
  }
};

/** The pattern that marks a call category as an alert, as the server counts them. */
export default function useCategoryAlert() {
  const [alert, setAlert] = useState(DEFAULT_ALERT);
  useEffect(() => {
    let live = true;
    getSettings()
      .then(s => { if (live && s?.categoryAlertRegex) setAlert(toRegExp(s.categoryAlertRegex)); })
      .catch(() => {});
    return () => { live = false; };
  }, []);
  return alert;
}
