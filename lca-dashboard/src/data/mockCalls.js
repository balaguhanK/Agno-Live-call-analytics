// Scripted turns for the dashboard's demo simulation. Real calls come from the
// Go backend (lca-backend), which seeds the former mock calls into Postgres.

// Live call turns for call 1 simulation
export const liveTurns = [
  { speaker: 'CUSTOMER', timeStart: '00:08.2', timeEnd: '00:11.5', text: "I've been waiting on hold for 20 minutes. This is unacceptable.", sentiment: 'negative', sentimentScore: -2.0, emoji: '😞' },
  { speaker: 'AGENT', timeStart: '00:12.0', timeEnd: '00:16.3', text: "I sincerely apologize for the wait time. I'll do my best to resolve your issue quickly.", sentiment: 'positive', sentimentScore: 1.8, emoji: '😊' },
  { speaker: 'AGENT_ASSISTANT', timeStart: '00:12.0', timeEnd: '00:16.3', text: 'Suggested response: Apologize for the wait and assure the customer you are prioritizing their issue. Offer compensation per retention policy KB-102 if wait exceeded 15 minutes.' },
  { speaker: 'CUSTOMER', timeStart: '00:18.0', timeEnd: '00:22.0', text: "Thank you. I just need to understand why I was charged twice for the same service.", sentiment: 'neutral', sentimentScore: -0.5, emoji: '😐' },
  { speaker: 'AGENT', timeStart: '00:23.0', timeEnd: '00:28.0', text: "I can see the duplicate charge on my screen. I'm going to initiate a reversal right now. You should see the credit within 2-3 business days.", sentiment: 'positive', sentimentScore: 3.0, emoji: '😊' },
  { speaker: 'CUSTOMER', timeStart: '00:30.0', timeEnd: '00:33.0', text: "That's great, thank you so much! I really appreciate your help.", sentiment: 'positive', sentimentScore: 4.0, emoji: '😊' },
];
