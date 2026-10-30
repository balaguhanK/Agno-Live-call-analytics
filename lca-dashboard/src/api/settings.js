import { api } from './client';

// { categoryAlertRegex }: call categories matching it are alerts
let settings;
export const getSettings = () => {
  settings ??= api('/settings').catch((err) => {
    settings = undefined; // try again next time
    throw err;
  });
  return settings;
};
