<script lang="ts">
	import type { ActivityItem } from '$lib/activity';
	import { agents } from '$lib/activity';
	import { logoSeed } from '$lib/avatar';
	import { session } from '$lib/session.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';

	// Who performed a timeline item, drawn so people and the panel never look
	// alike: a person is their round picture; an automatic agent (schedule,
	// GitHub push, the panel, the AI operator) is a square tile with its icon.
	// When the panel reports on an app by itself (a crash), the app's logo
	// carries a small panel badge. Decorative: the name is written beside it.
	let { item, size = 32 }: { item: ActivityItem; size?: number } = $props();

	const me = $derived(item.source === 'person' && !!item.actor && item.actor.id === session.user?.id);
	const appSelf = $derived(item.source === 'system' && item.type === 'change' && !!item.app);
	const kind = $derived(item.app?.kind === 'site' ? 'site' : 'bot');
</script>

{#if item.source === 'person'}
	<span class="mark person" class:me style:--s="{size}px" aria-hidden="true">
		{#if item.actor}
			<Logo src={item.actor.avatar_url} kind="user" seed={logoSeed(item.actor.id, item.actor.logo_variant)} class="size-full rounded-pill" />
		{:else}
			<Icon name="users" size={Math.round(size * 0.5)} />
		{/if}
	</span>
{:else if appSelf && item.app}
	<span class="mark app" style:--s="{size}px" aria-hidden="true">
		<Logo src={item.app.logo_url} {kind} seed={logoSeed(item.app.id, item.app.logo_variant)} class="size-full rounded-[inherit]" />
		<span class="badge" data-src="system"><Icon name="server" size={10} /></span>
	</span>
{:else}
	<span class="mark agent" data-src={item.source} style:--s="{size}px" aria-hidden="true">
		<Icon name={agents[item.source as Exclude<typeof item.source, 'person'>].icon} size={Math.round(size * 0.5)} />
	</span>
{/if}

<style>
	.mark {
		position: relative;
		display: inline-grid;
		flex: none;
		place-items: center;
		width: var(--s);
		height: var(--s);
	}
	.person {
		overflow: hidden;
		border-radius: var(--radius-pill);
		background: var(--color-paper-2);
		color: var(--color-muted);
	}
	.person.me {
		overflow: visible;
		box-shadow:
			0 0 0 2px var(--color-panel),
			0 0 0 3.5px color-mix(in srgb, var(--color-action) 70%, transparent);
	}
	.person.me :global(img) {
		border-radius: var(--radius-pill);
	}
	/* Agents are squares: never mistaken for a person's round picture. */
	.agent {
		--tint: var(--color-data);
		border-radius: calc(var(--radius-tile) * 0.8);
		background: color-mix(in srgb, var(--tint) 13%, var(--color-panel));
		color: var(--tint);
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--tint) 26%, transparent);
	}
	.agent[data-src='ai'] {
		--tint: var(--color-action);
	}
	.agent[data-src='push'] {
		--tint: var(--color-ink);
	}
	.app {
		border-radius: calc(var(--radius-tile) * 0.8);
	}
	.app :global(img) {
		border-radius: calc(var(--radius-tile) * 0.8);
	}
	.badge {
		position: absolute;
		right: -3px;
		bottom: -3px;
		display: grid;
		place-items: center;
		width: 16px;
		height: 16px;
		border-radius: 5px;
		background: var(--color-data);
		color: var(--color-panel);
		box-shadow: 0 0 0 2px var(--color-panel);
	}
</style>
