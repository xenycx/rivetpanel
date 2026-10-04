<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { roleText, type Bot, type Operation, type User, type Workspace } from '$lib/api/types';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import AdminBotTable from '$lib/components/AdminBotTable.svelte';
	import OperationRow from '$lib/components/OperationRow.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	type View = { user: User; has_password: boolean; workspaces: Workspace[]; bots: (Bot & { owner_email: string })[]; operations: Operation[] };
	const id = $derived(page.params.id ?? '');
	let v = $state<View | null>(null);
	let error = $state('');
	$effect(() => {
		if (!id) return;
		api<View>('GET', `/admin/users/${id}`)
			.then((r) => ((v = r), (error = '')))
			.catch((e) => (error = e instanceof ApiError ? e.message : 'The account could not be loaded.'));
	});
	const running = $derived(v?.bots.filter((b) => b.desired_state === 'running').length ?? 0);
	const memory = $derived(v?.bots.filter((b) => b.desired_state === 'running').reduce((n, b) => n + b.memory_bytes, 0) ?? 0);
</script>

<svelte:head><title>{v?.user.email ?? 'Account'} · Administration · RivetPanel</title></svelte:head>

<a href="/admin/users" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />All users</a>
{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
{#if !v && !error}
	<Skeleton rows={5} />
{:else if v}
	<header class="card flex flex-wrap items-center gap-4 p-5">
		{#if v.user.avatar_url}<img src={v.user.avatar_url} alt="" class="size-12 rounded-pill object-cover" />{:else}<span class="grid size-12 place-items-center rounded-pill bg-paper-2 font-semibold text-muted" aria-hidden="true">{(v.user.display_name || v.user.email).slice(0, 2).toUpperCase()}</span>{/if}
		<div class="min-w-0 flex-1">
			<h2 class="truncate text-section">{v.user.display_name || v.user.email}</h2>
			<p class="text-small text-muted">{v.user.display_name ? `${v.user.email} · ` : ''}{v.user.role === 'admin' ? 'Administrator' : v.user.role_name || 'User'} · email {v.user.email_verified ? 'verified' : 'not verified'} · joined {fmtWhen(v.user.created_at_ms)} · {v.has_password ? 'password' : 'provider sign-in only'}</p>
		</div>
		{#if v.user.disabled}<span class="pill" data-tone="fail">Disabled</span>{:else}<span class="pill" data-tone="run">Active</span>{/if}
	</header>

	<dl class="mt-5 grid grid-cols-2 gap-3 md:grid-cols-4">
		<div class="card px-4 py-3"><dt class="eyebrow">Bots owned</dt><dd class="mt-1 font-mono text-title font-semibold">{running}/{v.bots.length}</dd><dd class="text-small text-muted">running / total</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Memory</dt><dd class="mt-1 font-mono text-title font-semibold">{fmtBytes(memory)}</dd><dd class="text-small text-muted">assigned to running bots</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Workspaces</dt><dd class="mt-1 font-mono text-title font-semibold">{v.workspaces.length}</dd><dd class="text-small text-muted">member of</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Last operation</dt><dd class="mt-1 text-title font-semibold">{v.operations[0] ? fmtAgo(v.operations[0].created_at_ms) : '—'}</dd></div>
	</dl>

	<section class="mt-8" aria-labelledby="ws-h">
		<h3 id="ws-h" class="mb-2 text-title font-semibold">Workspaces</h3>
		<ul class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
			{#each v.workspaces as w (w.id)}
				<li>
					<a href="/admin/workspaces/{w.id}" class="card flex items-center gap-3 p-4 transition-colors hover:border-rule">
						<span class="grid size-9 shrink-0 place-items-center rounded-tile bg-action/12 font-mono text-[11px] font-semibold text-action" aria-hidden="true">{(w.personal ? 'P' : w.name.slice(0, 2)).toUpperCase()}</span>
						<span class="min-w-0 flex-1">
							<span class="block truncate font-medium">{w.personal ? 'Personal' : w.name}</span>
							<span class="block text-small text-muted">{w.role ? roleText[w.role] : ''} · {w.running_bots}/{w.bots} bots · {w.sites} sites · {w.members} members</span>
						</span>
						<Icon name="chevronRight" size={14} class="text-muted" />
					</a>
				</li>
			{/each}
		</ul>
	</section>

	<section class="mt-8" aria-labelledby="bots-h">
		<h3 id="bots-h" class="mb-2 text-title font-semibold">Bots they own</h3>
		<AdminBotTable bots={v.bots} showOwner={false} empty="This account owns no bots." />
	</section>

	<section class="mt-8" aria-labelledby="ops-h">
		<h3 id="ops-h" class="mb-2 text-title font-semibold">Recent deployments and operations</h3>
		{#if v.operations.length}
			<ul class="card overflow-hidden [&>li+li]:border-t [&>li+li]:border-rule-soft">
				{#each v.operations as op (op.id)}<OperationRow {op} showBot />{/each}
			</ul>
		{:else}
			<p class="card px-4 py-4 text-small text-muted">No deployments, builds or backups on their bots yet.</p>
		{/if}
	</section>
{/if}
