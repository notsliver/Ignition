import { api } from '../api/client';

const decode = value => Uint8Array.from(atob(value.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0));
const encode = value => btoa(String.fromCharCode(...new Uint8Array(value))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
export const supportsPasskeys = () => !!(window.isSecureContext && window.PublicKeyCredential && navigator.credentials);

function responseJSON(credential) {
  const response = {};
  for (const key of ['clientDataJSON', 'attestationObject', 'authenticatorData', 'signature', 'userHandle']) {
    if (credential.response[key]) response[key] = encode(credential.response[key]);
  }
  if (credential.response.getTransports) response.transports = credential.response.getTransports();
  return { id: credential.id, rawId: encode(credential.rawId), type: credential.type, authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(), response };
}

export async function passkeyCeremony(flow, register = false) {
  if (!supportsPasskeys()) throw new Error('Passkeys need a supported browser on HTTPS or localhost.');
  const publicKey = { ...flow.options.publicKey, challenge: decode(flow.options.publicKey.challenge) };
  if (register) publicKey.user = { ...publicKey.user, id: decode(publicKey.user.id) };
  for (const key of ['excludeCredentials', 'allowCredentials']) {
    if (publicKey[key]) publicKey[key] = publicKey[key].map(c => ({ ...c, id: decode(c.id) }));
  }
  try {
    const credential = register ? await navigator.credentials.create({ publicKey }) : await navigator.credentials.get({ publicKey });
    if (!credential) throw new Error('No passkey was selected.');
    return { flowId: flow.flowId, credential: responseJSON(credential) };
  } catch (error) {
    if (['NotAllowedError', 'AbortError'].includes(error.name)) throw new Error('Passkey request cancelled or timed out. Try again when ready.');
    throw error;
  }
}
export const signInWithPasskey = async () => api.finishPasskeyLogin(await passkeyCeremony(await api.beginPasskeyLogin()));
