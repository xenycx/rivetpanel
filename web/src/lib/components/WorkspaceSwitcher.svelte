<script lang="ts">
	import { goto } from '$app/navigation';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import { currentWorkspace, selectWorkspace, workspaces } from '$lib/workspaces.svelte';

	// Scopes the overview (and new bots and sites) to one workspace.
	let { compact = false }: { compact?: boolean } = $props();
	const current = $derived(currentWorkspace());
	const label = $derived(current ? (current.personal ? 'Personal' : current.name) : 'All workspaces');
	const initials = $derived(current ? label.slice(0, 2).toUpperCase() : '∗');
	const items = $derived<MenuItem[]>([
		{ label: `${workspaces.selected === 'all' ? '✓ ' : ''}All workspaces`, hint: 'Everything you can access', detail: true, onselect: () => selectWorkspace('all') },
		'separator',
		...workspaces.list.map((w) => ({
			label: `${workspaces.selected === w.id ? '✓ ' : ''}${w.personal ? 'Personal' : w.name}`,
			hint: `${w.bots} bot${w.bots === 1 ? '' : 's'}${w.sites ? ` · ${w.sites} site${w.sites === 1 ? '' : 's'}` : ''} · ${w.role || 'administrator'}`,
			detail: true,
			onselect: () => selectWorkspace(w.id)
		})),
		'separator',
		{ label: 'Manage workspaces', onselect: () => goto('/settings/workspaces') }
	]);
</script>

<Menu
	{items}
	label="Switch workspace"
	align="start"
	class={compact ? '' : 'w-full'}
	menuClass={compact ? 'w-64' : ''}
	fixed={compact}
	side={compact}
	triggerClass={compact
		? 'grid size-10 place-items-center rounded-tile border border-rule-soft bg-raised text-ink hover:border-rule'
		: 'flex w-full items-center gap-2.5 rounded-tile border border-rule-soft bg-raised px-2.5 py-2 text-left transition-colors hover:border-rule'}
>
	{#snippet trigger()}
		{#if compact}
			<span title="Workspace: {label}" class="grid place-items-center"><Icon name="building" size={17} /></span>
		{:else}
			<span class="grid size-7 shrink-0 place-items-center rounded-control bg-paper-2 font-mono text-[11px] font-semibold text-ink" aria-hidden="true">{initials}</span>
			<span class="min-w-0 flex-1">
				<span class="eyebrow block leading-none">Workspace</span>
				<span class="mt-0.5 block truncate font-medium">{label}</span>
			</span>
			<Icon name="chevronDown" size={14} class="shrink-0 text-muted" />
		{/if}
	{/snippet}
</Menu>
