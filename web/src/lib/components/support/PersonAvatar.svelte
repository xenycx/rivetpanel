<script lang="ts">
	import type { Person } from '$lib/api/support';
	import { logoSeed } from '$lib/avatar';
	import { session } from '$lib/session.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';

	// An account's picture on a ticket. `team` draws the support team (staff
	// hidden from requesters); no person draws an empty, dashed "nobody".
	// A ring marks the signed-in account. Decorative: the name is written
	// next to it or given as the title.
	let { person = null, team = false, size = 20, class: cls = '' }: { person?: Person | null; team?: boolean; size?: number; class?: string } = $props();
	const me = $derived(!!person && person.id === session.user?.id);
</script>

{#if team}
	<span class="avatar-team {cls}" style:width="{size}px" style:height="{size}px" title="Support team" aria-hidden="true"><Icon name="lifebuoy" size={Math.round(size * 0.62)} /></span>
{:else if person}
	<span class="avatar-wrap {cls}" class:me style:width="{size}px" style:height="{size}px" title={person.name} aria-hidden="true">
		<Logo src={person.avatar_url} kind="user" seed={logoSeed(person.id, person.logo_variant)} class="size-full rounded-pill" />
	</span>
{:else}
	<span class="avatar-none {cls}" style:width="{size}px" style:height="{size}px" title="Unassigned" aria-hidden="true"></span>
{/if}
