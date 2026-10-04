<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import { toast } from '$lib/ui/toast.svelte';

	// Step-by-step instructions and the two fields for one sign-in provider.
	let {
		provider,
		publicUrl,
		clientId = $bindable(''),
		secret = $bindable(''),
		secretSet = false,
		locked = false
	}: { provider: 'github' | 'discord'; publicUrl: string; clientId: string; secret: string; secretSet?: boolean; locked?: boolean } = $props();

	const base = $derived((publicUrl || '').replace(/\/+$/, ''));
	const callback = $derived(`${base || 'https://your-panel'}/api/v1/auth/${provider}/callback`);
	const gh = $derived(provider === 'github');
	function copy(v: string) {
		navigator.clipboard?.writeText(v).then(() => toast('Copied', 'success'));
	}
</script>

<ol class="grid grid-cols-[minmax(0,1fr)] gap-2.5 text-small">
	{#if gh}
		<li class="flex gap-3"><span class="step-no">1</span><span class="min-w-0 flex-1 break-words">Open <a class="link" href="https://github.com/settings/applications/new" target="_blank" rel="noopener">GitHub → Settings → Developer settings → OAuth Apps → New OAuth App</a>.</span></li>
		<li class="flex gap-3"><span class="step-no">2</span><span class="min-w-0 flex-1 break-words">Use any name (for example <em>RivetPanel</em>). Homepage URL: <code class="copyable break-all">{base || 'your panel address'}</code></span></li>
	{:else}
		<li class="flex gap-3"><span class="step-no">1</span><span class="min-w-0 flex-1 break-words">Open the <a class="link" href="https://discord.com/developers/applications" target="_blank" rel="noopener">Discord Developer Portal</a>, create an application (or reuse one) and open <strong>OAuth2</strong>.</span></li>
		<li class="flex gap-3"><span class="step-no">2</span><span class="min-w-0 flex-1 break-words">Under <strong>Redirects</strong>, add the address below and save.</span></li>
	{/if}
	<li class="flex gap-3">
		<span class="step-no">3</span>
		<span class="min-w-0 flex-1">
			{gh ? 'Authorization callback URL:' : 'Redirect:'}
			<span class="mt-1 flex items-center gap-1.5">
				<code class="copyable min-w-0 flex-1 truncate" title={callback}>{callback}</code>
				<button type="button" class="btn btn-sm btn-icon" onclick={() => copy(callback)} aria-label="Copy the callback address"><Icon name="copy" size={13} /></button>
			</span>
		</span>
	</li>
	<li class="flex gap-3"><span class="step-no">4</span><span class="min-w-0 flex-1 break-words">{gh ? 'Register the app, then generate a client secret.' : 'Copy the Client ID and reset the Client Secret.'} Paste both here.</span></li>
</ol>

<div class="@container">
	<div class="mt-4 grid gap-3 @md:grid-cols-2">
		<label class="block">
			<span class="label">Client ID</span>
			<input class="field font-mono" autocomplete="off" spellcheck="false" bind:value={clientId} disabled={locked} placeholder={gh ? 'Ov23li…' : '1234567890…'} />
		</label>
		<label class="block">
			<span class="label">Client secret</span>
			<input class="field font-mono" type="password" autocomplete="new-password" spellcheck="false" bind:value={secret} disabled={locked} placeholder={secretSet ? '•••••••• (saved; leave empty to keep)' : ''} />
		</label>
	</div>
</div>
{#if locked}<p class="help">Set in the environment file; change it there.</p>{/if}
