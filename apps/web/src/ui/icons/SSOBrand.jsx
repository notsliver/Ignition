import { ShieldCheckIcon } from '@heroicons/react/16/solid';
import { GoogleIcon, DiscordIcon, MicrosoftIcon, OktaIcon, AuthentikIcon, KeycloakIcon, Auth0Icon } from './brands';

export const ssoPresets = [
  {id:'google', label:'Google', icon:GoogleIcon, issuer:'https://accounts.google.com', fixed:true, guide:'Create an OAuth client with application type Web application in Google Cloud. Add the redirect URI below under Authorized redirect URIs.', docs:'https://developers.google.com/identity/openid-connect/openid-connect'},
  {id:'discord', label:'Discord', icon:DiscordIcon, issuer:'https://discord.com', fixed:true, guide:'Create an application in the Discord Developer Portal. Under OAuth2, register the redirect URI below, then copy the client ID and client secret. No bot permissions are needed.', docs:'https://discord.com/developers/docs/topics/oauth2'},
  {id:'entra', label:'Microsoft Entra', icon:MicrosoftIcon, placeholder:'https://login.microsoftonline.com/YOUR-TENANT-ID/v2.0', guide:'Register a single-tenant application in Microsoft Entra. Add a Web redirect URI, create a client secret, and use your Directory (tenant) ID in the issuer URL.', docs:'https://learn.microsoft.com/en-us/entra/identity-platform/v2-protocols-oidc'},
  {id:'okta', label:'Okta', icon:OktaIcon, placeholder:'https://your-org.okta.com/oauth2/default', guide:'Create an OIDC Web Application with Authorization Code enabled. Register the redirect URI and assign the people or groups allowed to use it. Copy its issuer from the authorization server.', docs:'https://developer.okta.com/docs/guides/implement-grant-type/authcode/main/'},
  {id:'authentik', label:'Authentik', icon:AuthentikIcon, placeholder:'https://auth.example.com/application/o/ignition/', guide:'Create an OAuth2/OpenID provider and application with a confidential client. Register the redirect URI and copy the issuer from the provider’s OpenID configuration.', docs:'https://docs.goauthentik.io/add-secure-apps/providers/oauth2/'},
  {id:'keycloak', label:'Keycloak', icon:KeycloakIcon, placeholder:'https://auth.example.com/realms/YOUR-REALM', guide:'Create an OpenID Connect client in your realm. Enable Client authentication and Standard flow, set the valid redirect URI, and copy the secret from Credentials.', docs:'https://www.keycloak.org/securing-apps/oidc-layers'},
  {id:'auth0', label:'Auth0', icon:Auth0Icon, placeholder:'https://YOUR-TENANT.auth0.com', guide:'Create a Regular Web Application. Add the redirect URI under Allowed Callback URLs, then copy the domain, client ID and client secret.', docs:'https://auth0.com/docs/authenticate/protocols/openid-connect-protocol'},
  {id:'custom', label:'Custom OpenID Connect', icon:ShieldCheckIcon, placeholder:'https://auth.example.com', guide:'Register a confidential web client with your identity provider. Use its issuer URL, not the discovery document URL. Discovery, PKCE and signed ID tokens are required.'},
];
export function inferSSOPreset(status) {
  if(status.provider) return status.provider;
  if(status.issuer === 'https://accounts.google.com') return 'google';
  if(status.issuer === 'https://discord.com') return 'discord';
  return 'custom';
}
export function SSOBrand({provider, iconUrl, ...props}) {
  if ((!provider || provider === 'custom') && iconUrl) return <img src={iconUrl} alt="" {...props} />;
  const Icon = ssoPresets.find(p=>p.id===provider)?.icon || ShieldCheckIcon;
  return <Icon {...props} />;
}
