<script lang="ts">
	import Notice from '$lib/components/ui/Notice.svelte';
	import AuthFrame from '$lib/components/AuthFrame.svelte';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, setCsrf } from '$lib/api/client';
	import { loadSession } from '$lib/session.svelte';
	import type { User } from '$lib/api/types';
	import Icon from '$lib/components/ui/Icon.svelte';

	type Invite = { email: string; role: string; expires_at_ms: number };
	let token = $state(''); let invite = $state<Invite | null>(null); let enabled = $state(false); let providers = $state<string[]>([]);
	let email = $state(''); let password = $state(''); let error = $state(''); let busy = $state(false);
	const labels: Record<string,string> = { github:'GitHub', discord:'Discord' };
	onMount(async()=>{
		token=location.hash.slice(1); history.replaceState(null,'','/register');
		try { const s=await api<{enabled:boolean;providers:string[]}>('GET','/registration'); enabled=s.enabled;providers=s.providers; } catch {}
		if(token) try { invite=await api<Invite>('POST','/registration/preview',{token}); email=invite.email; } catch(e){error=e instanceof ApiError?e.message:'This invitation is not valid.';}
	});
	async function submit(e:SubmitEvent){e.preventDefault();busy=true;error='';try{const r=await api<{user:User;csrf_token:string}>('POST','/registration',{token,email,password});setCsrf(r.csrf_token);await loadSession();goto('/dashboard');}catch(e){error=e instanceof ApiError?e.message:'The account could not be created.';}finally{busy=false;}}
</script>
<svelte:head><title>Create account · RivetPanel</title></svelte:head>
<AuthFrame>
	<h1 class="text-page font-semibold">Create your account</h1>
	{#if !enabled}<Notice tone="warn" class="mt-4">Registration is closed. Ask an administrator to enable it.</Notice>
	{:else if error && !invite}<Notice tone="fail" class="mt-4" live>{error}</Notice>
	{:else if invite}
		<p class="mt-1 text-muted">Your invitation expires {new Date(invite.expires_at_ms).toLocaleString()}.</p>
		{#if providers.length}<div class="mt-6 grid gap-2">{#each providers as p}<a class="btn w-full" href={`/api/v1/auth/${p}/login`} data-sveltekit-reload><Icon name={p as 'github'} />Continue with {labels[p]}</a>{/each}</div><div class="my-5 flex items-center gap-3 text-small text-muted"><hr class="flex-1 border-rule" />or create a password<hr class="flex-1 border-rule" /></div>{/if}
		{#if error}<Notice tone="fail" class="mb-4" live>{error}</Notice>{/if}
		<form class="space-y-4" onsubmit={submit}><label class="block"><span class="label">Email</span><input class="field" type="email" required bind:value={email} disabled={!!invite.email} /></label><label class="block"><span class="label">Password</span><input class="field" type="password" required minlength="12" autocomplete="new-password" bind:value={password} /><span class="help">At least 12 characters.</span></label><button class="btn btn-primary w-full" disabled={busy}>{busy?'Creating…':'Create account'}</button></form>
	{:else}<p class="mt-4 text-muted">Open the invitation link from your administrator to continue.</p>{/if}
	<p class="mt-6 border-t border-rule-soft pt-4 text-small text-muted">Already have an account? <a class="link" href="/login">Sign in</a></p>
</AuthFrame>
