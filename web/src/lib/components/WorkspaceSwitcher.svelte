<script lang="ts">
	import { goto } from '$app/navigation';
	import { roleText, type Workspace } from '$lib/api/types';
	import { logoSeed } from '$lib/avatar';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import { currentWorkspace, loadWorkspaces, selectWorkspace, workspaces, wsName } from '$lib/workspaces.svelte';

	// Scopes the overview (and new bots and sites) to one workspace. With a
	// single workspace there is no "All workspaces": that one is shown.
	let { compact = false }: { compact?: boolean } = $props();
	const only = $derived(workspaces.list.length === 1 ? workspaces.list[0] : null);
	const current = $derived(currentWorkspace() ?? only);
	const failed = $derived(!!workspaces.error && !workspaces.loaded);
	const label = $derived(failed ? 'Unavailable' : !workspaces.loaded ? 'Loading…' : current ? wsName(current) : 'All workspaces');
	// "All workspaces" shows the first two workspaces' logos overlapping, in
	// the space one logo takes; the menu row repeats it.
	const stack = $derived(workspaces.list.slice(0, 2));
	const logoOf = (w: Workspace) => ({ src: w.logo_url, kind: 'workspace' as const, seed: logoSeed(w.id, w.logo_variant) });
	const hintOf = (w: Workspace) =>
		`${w.personal && wsName(w) !== 'Personal' ? 'Personal · ' : ''}${w.bots} app${w.bots === 1 ? '' : 's'}${w.sites ? ` · ${w.sites} site${w.sites === 1 ? '' : 's'}` : ''} · ${w.role ? roleText[w.role] : 'Administrator'}`;
	const manage: MenuItem = { label: 'Manage workspaces', onselect: () => goto('/settings/workspaces') };
	const items = $derived.by<MenuItem[]>(() => {
		if (failed) return [{ label: 'Try again', hint: workspaces.error, detail: true, icon: 'restart', onselect: () => void loadWorkspaces() }, 'separator', manage];
		if (only) return [{ label: wsName(only), hint: hintOf(only), detail: true, image: logoOf(only), checked: true, onselect: () => selectWorkspace('all') }, 'separator', manage];
		return [
			{ label: 'All workspaces', hint: 'Everything you can access', detail: true, ...(stack.length ? { image: stack.map(logoOf) } : { icon: 'layers' as const }), checked: workspaces.selected === 'all', onselect: () => selectWorkspace('all') },
			'separator',
			...workspaces.list.map(
				(w): MenuItem => ({
					label: wsName(w),
					hint: hintOf(w),
					detail: true,
					image: logoOf(w),
					checked: workspaces.selected === w.id,
					onselect: () => selectWorkspace(w.id)
				})
			),
			'separator',
			manage
		];
	});
</script>

{#snippet mark()}
	{#if failed}<span class="grid size-7 shrink-0 place-items-center rounded-control bg-paper-2 text-warn" aria-hidden="true"><Icon name="alert" size={14} /></span>
	{:else if current}<Logo {...logoOf(current)} class="size-7 rounded-control" />
	{:else if stack.length > 1}
		<span class="relative block size-7 shrink-0" aria-hidden="true">
			<Logo {...logoOf(stack[0])} class="absolute top-0 left-0 size-5 rounded-inner" />
			<Logo {...logoOf(stack[1])} class="absolute right-0 bottom-0 size-5 rounded-inner ring-2 ring-raised" />
		</span>
	{:else}<span class="grid size-7 shrink-0 place-items-center rounded-control bg-paper-2 text-muted" aria-hidden="true"><Icon name="layers" size={14} /></span>{/if}
{/snippet}

<Menu
	{items}
	label="Switch workspace"
	triggerLabel={failed ? 'Workspaces could not be loaded' : `Workspace: ${label}`}
	align="start"
	class={compact ? '' : 'w-full'}
	menuClass={compact ? 'w-72' : ''}
	fixed={compact}
	side={compact}
	triggerClass={compact
		? 'grid size-10 place-items-center rounded-tile border border-rule-soft bg-raised text-ink hover:border-rule'
		: 'flex w-full items-center gap-2.5 rounded-tile border border-rule-soft bg-raised px-2.5 py-2 text-left transition-colors hover:border-rule'}
>
	{#snippet trigger()}
		{#if compact}
			<span title={failed ? 'Workspaces could not be loaded' : `Workspace: ${label}`} class="grid place-items-center">{@render mark()}</span>
		{:else}
			{@render mark()}
			<span class="min-w-0 flex-1">
				<span class="eyebrow block leading-none">Workspace</span>
				<span class="mt-0.5 block truncate font-medium {failed || !workspaces.loaded ? 'text-muted' : ''}">{label}</span>
			</span>
			<Icon name="chevronDown" size={14} class="shrink-0 text-muted" />
		{/if}
	{/snippet}
</Menu>
