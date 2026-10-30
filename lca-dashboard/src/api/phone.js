import { api } from './client';

// The signed-in agent's FreeSWITCH account for the browser phone:
// { enabled, reason?, wsUrl, domain, sipUser, password, queueNumber }
export const getPhoneConfig = () => api('/phone/config');
