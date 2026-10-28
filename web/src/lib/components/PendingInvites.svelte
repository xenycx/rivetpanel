<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import type { AccountInvite } from '$lib/api/types';
	import { fmtAgo, fmtUntil, fmtWhen } from '$lib/args';
	import { inviteRoleName, limitsText, workspaceText } from '$lib/invites';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	// Invitations that have not been used or expired yet. Bump refreshKey (or
	// call reload) to fetch the list again, for example after creating one.
	let { refreshKey = 0 }: { refreshKey?: number } = $props();

	let invites = $state<AccountInvite[] | null>(null);
	let error = $state('');
	let busy = $state('');

	export async function reload() {
		try {
			invites = (await api<{ invites: AccountInvite[] }>('GET', '/admin/account-invites')).invites;
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The invitations could not be loaded.';
		}
	}
	$effect(() => {
		void refreshKey;
		reload();
	});

	function setup(v: AccountInvite): string {
		const parts = [limitsText(v.quota), v.node_id ? `on ${v.node_name || 'a pinned node'}` : '', workspaceText(v)].filter(Boolean);
		return parts.length ? parts.join(' · ') : 'Panel defaults';
	}

	async function revoke(v: AccountInvite) {
		const who = v.email || 'the open link';
		const ok = await confirmDialog({
			title: `Revoke the invitation for ${who}?`,
			body: 'The link stops working at once. Accounts already created are not affected.',
			confirmLabel: 'Revoke invitation',
			tone: 'danger'
		});
		if (!ok) return;
		busy = v.id;
		try {
			await api('DELETE', `/admin/account-invites/${v.id}`);
			toast(`Revoked the invitation for ${who}`);
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The invitation could not be revoked.', 'fail');
		} finally {
			busy = '';
		}
		await reload();
	}
</script>

<section class="mt-8" aria-labelledby="pending-invites-h">
	<h3 id="pending-invites-h" class="text-title font-semibold">Pending invitations</h3>
	<p class="mt-1 mb-3 max-w-prose text-small text-muted">Links that have not been used yet. An invitation sets the new account's role (built-in or custom) and can also set limits for their personal workspace, the node their apps run on and a team workspace to join. Each link works once; revoke one to stop it before it expires.</p>
	{#if error}
		<Notice tone="fail" live>{error}</Notice>
	{:else if invites === null}
		<Skeleton rows={2} />
	{:else if !invites.length}
		<EmptyState title="No pending invitations" compact>Use Invite user to create a link for someone to make their own account.</EmptyState>
	{:else}
		<!-- A table from 48rem of list width; stacked rows below it (phones, a narrow content column). -->
		<div class="@container card overflow-hidden">
			<ul class="[&>li+li]:border-t [&>li+li]:border-rule-soft">
				<li class="hidden gap-4 bg-paper-2/40 px-4 py-2 text-small font-medium text-muted @3xl:grid @3xl:grid-cols-[minmax(0,1.4fr)_minmax(0,0.8fr)_minmax(0,1.6fr)_minmax(0,1.1fr)_6.5rem_5rem]" aria-hidden="true">
					<span>Email</span><span>Role</span><span>Setup</span><span>Created by</span><span>Expires</span><span></span>
				</li>
				{#each invites as v (v.id)}
					<li class="grid gap-x-4 gap-y-1 px-4 py-3 @3xl:grid-cols-[minmax(0,1.4fr)_minmax(0,0.8fr)_minmax(0,1.6fr)_minmax(0,1.1fr)_6.5rem_5rem] @3xl:items-center">
						<p class="min-w-0 font-medium break-all">{#if v.email}{v.email}{:else}<span class="text-muted">Anyone with the link</span>{/if}</p>
						<p class="min-w-0 text-small"><span class="text-muted @3xl:hidden">Role:</span> {inviteRoleName(v)}</p>
						<p class="min-w-0 text-small break-words"><span class="text-muted @3xl:hidden">Setup:</span> {setup(v)}</p>
						<p class="min-w-0 text-small" title="Created {fmtWhen(v.created_at_ms)}"><span class="text-muted @3xl:hidden">Created by</span> <span class="break-all">{v.created_by_email || 'Unknown'}</span><span class="text-muted @3xl:hidden">,</span> <span class="text-muted @3xl:block">{fmtAgo(v.created_at_ms)}</span></p>
						<p class="text-small" title={fmtWhen(v.expires_at_ms)}><span class="text-muted @3xl:hidden">Expires</span> {fmtUntil(v.expires_at_ms)}</p>
						<div class="mt-1 @3xl:mt-0 @3xl:text-right"><button class="btn btn-sm" type="button" disabled={busy === v.id} onclick={() => revoke(v)}>Revoke</button></div>
					</li>
				{/each}
			</ul>
		</div>
	{/if}
</section>
