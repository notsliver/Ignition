// Fictional sample data until the API is wired up.
// Real status must come from backend security checks, never from the UI.
export const status = {
  https: { valid: true, issuer: "Let's Encrypt", expires: 'Jan 12, 2027' },
  encryption: { algorithm: 'AES-256-GCM', keySource: 'IGNITION_SECRET_KEY', lastRotated: 'Mar 14, 2026' },
  oidc: { configured: false },
};

export const events = [
  { tone: 'danger', action: '14 failed sign-in attempts', detail: 'From 185.220.101.4 · IP locked for 15 min', time: '22m ago' },
  { tone: 'warning', action: 'MFA enforcement turned off', detail: 'Authentication policies · by admin', time: '2h ago' },
  { tone: 'neutral', action: 'Idle timeout changed', detail: 'Session policies · 7 days → 12 hours', time: 'Yesterday' },
  { tone: 'warning', action: 'Privileges changed', detail: 'maya promoted to Admin · by admin', time: 'Oct 21' },
  { tone: 'neutral', action: 'API token created', detail: 'Scope: deployments · by admin', time: 'Oct 20' },
  { tone: 'danger', action: 'Unusual sign-in location', detail: 'New country for jonas · MFA verified', time: 'Oct 18' },
];
