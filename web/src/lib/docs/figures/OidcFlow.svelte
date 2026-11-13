<script lang="ts">
	import Figure from './Figure.svelte';
	import Sequence, { type Actor, type Step } from './Sequence.svelte';
	const actors: Actor[] = [
		{ id: 'b', name: 'Browser', icon: 'monitor' },
		{ id: 'p', name: 'RivetPanel', icon: 'panel', k: 'panel' },
		{ id: 'i', name: 'Identity provider', sub: 'Keycloak, Okta, Entra ID…', icon: 'key' }
	];
	const steps: Step[] = [
		{ from: 'b', to: 'p', label: 'Sign in with the provider' },
		{ from: 'p', to: 'b', label: 'Redirect with PKCE challenge, state (bound by a cookie) and nonce' },
		{ from: 'b', to: 'i', label: 'Sign in at the provider' },
		{ from: 'i', to: 'b', label: 'Redirect to the callback with a code' },
		{ from: 'b', to: 'p', label: 'Callback: the state must match the cookie' },
		{ from: 'p', to: 'i', label: 'Code and PKCE verifier' },
		{ from: 'i', to: 'p', label: 'ID token' },
		{ from: 'p', to: 'p', label: 'Check signature, issuer, audience, expiry, nonce; match the account', tall: true, k: 'panel' },
		{ from: 'p', to: 'b', label: 'Signed in, after TOTP or a passkey if the account has one' }
	];
</script>

<Figure caption="Single sign-on uses the authorization code flow with PKCE. The panel trusts the identity only after it has checked the ID token itself.">
	<Sequence {actors} {steps} />
</Figure>
