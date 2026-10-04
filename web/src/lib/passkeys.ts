import { api } from '$lib/api/client';

// WebAuthn needs ArrayBuffers where the server's JSON has base64url strings
// (challenge, user.id, credential ids); responses go back the other way.

const fromB64u = (s: string) => {
	const b = atob(s.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((s.length + 3) % 4));
	return Uint8Array.from(b, (c) => c.charCodeAt(0)).buffer;
};
const toB64u = (b: ArrayBuffer | null | undefined) =>
	b ? btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '') : undefined;

type Desc = { id: string; type: string; transports?: string[] };
const descs = (xs?: Desc[]) => xs?.map((d) => ({ ...d, id: fromB64u(d.id) })) as PublicKeyCredentialDescriptor[] | undefined;

/** Whether this browser can use passkeys at all. */
export const passkeysSupported = () => typeof window !== 'undefined' && !!window.PublicKeyCredential && !!navigator.credentials;

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Opts = { publicKey: any };

async function create(options: Opts) {
	const pk = options.publicKey;
	const cred = (await navigator.credentials.create({
		publicKey: { ...pk, challenge: fromB64u(pk.challenge), user: { ...pk.user, id: fromB64u(pk.user.id) }, excludeCredentials: descs(pk.excludeCredentials) }
	})) as PublicKeyCredential | null;
	if (!cred) throw new Error('No passkey was created.');
	const r = cred.response as AuthenticatorAttestationResponse;
	return {
		id: cred.id,
		rawId: toB64u(cred.rawId),
		type: cred.type,
		response: { clientDataJSON: toB64u(r.clientDataJSON), attestationObject: toB64u(r.attestationObject), transports: r.getTransports?.() ?? [] },
		clientExtensionResults: cred.getClientExtensionResults()
	};
}

async function get(options: Opts) {
	const pk = options.publicKey;
	const cred = (await navigator.credentials.get({ publicKey: { ...pk, challenge: fromB64u(pk.challenge), allowCredentials: descs(pk.allowCredentials) } })) as PublicKeyCredential | null;
	if (!cred) throw new Error('No passkey was chosen.');
	const r = cred.response as AuthenticatorAssertionResponse;
	return {
		id: cred.id,
		rawId: toB64u(cred.rawId),
		type: cred.type,
		response: { clientDataJSON: toB64u(r.clientDataJSON), authenticatorData: toB64u(r.authenticatorData), signature: toB64u(r.signature), userHandle: toB64u(r.userHandle) },
		clientExtensionResults: cred.getClientExtensionResults()
	};
}

/** Registers a passkey for the signed-in account. */
export async function addPasskey(name: string, password: string) {
	const b = await api<{ ticket: string; options: Opts }>('POST', '/me/passkeys/register/begin', { password });
	const credential = await create(b.options);
	return api('POST', '/me/passkeys/register/finish', { ticket: b.ticket, name, credential });
}

/** Passwordless sign-in; returns the /auth/login-shaped answer. */
export async function passkeySignIn<T>() {
	const b = await api<{ ticket: string; options: Opts }>('POST', '/auth/passkey/begin');
	const credential = await get(b.options);
	return api<T>('POST', '/auth/passkey/finish', { ticket: b.ticket, credential });
}

/** A passkey as the second step of a sign-in. */
export async function passkeySecondStep<T>() {
	const b = await api<{ ticket: string; options: Opts }>('POST', '/auth/mfa/passkey/begin');
	const credential = await get(b.options);
	return api<T>('POST', '/auth/mfa/passkey/finish', { ticket: b.ticket, credential });
}
