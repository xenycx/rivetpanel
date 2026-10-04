<script lang="ts">
	import { accents, radii, setAccent, setRadius, setTheme, theme, type Accent, type Radius, type ThemePref } from '$lib/ui/theme.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	const modes: { id: ThemePref; label: string; text: string; icon: 'sun' | 'moon' | 'monitor' }[] = [
		{ id: 'light', label: 'Light', text: 'Bright working surfaces', icon: 'sun' },
		{ id: 'dark', label: 'Dark', text: 'Low-light operations', icon: 'moon' },
		{ id: 'system', label: 'System', text: 'Match this device', icon: 'monitor' }
	];
	// Preview radii are fixed per option so every card shows its own shape,
	// whatever is selected right now.
	const scale: Record<Radius, number> = { none: 0, subtle: 0.5, default: 1, round: 1.5 };
	const choice = 'flex items-center gap-3 rounded-tile border p-3.5 text-left transition-colors';
	const on = 'border-action bg-action/8 shadow-[inset_0_0_0_1px_var(--color-action)]';
	const off = 'border-rule-soft bg-panel hover:border-rule';
</script>

<svelte:head><title>Appearance · RivetPanel</title></svelte:head>

<p class="mb-2 max-w-prose text-muted">These choices apply to this browser only and take effect immediately.</p>

<SettingsSection title="Theme" description="Light, dark, or follow the operating system.">
	<div class="grid gap-3 sm:grid-cols-3">
		{#each modes as mode (mode.id)}
			<button class="{choice} {theme.pref === mode.id ? on : off}" aria-pressed={theme.pref === mode.id} onclick={() => setTheme(mode.id)}>
				<span class="grid size-9 shrink-0 place-items-center rounded-control bg-paper-2 text-action"><Icon name={mode.icon} /></span>
				<span class="min-w-0"><span class="block font-semibold">{mode.label}</span><span class="block text-small text-muted">{mode.text}</span></span>
			</button>
		{/each}
	</div>
</SettingsSection>

<SettingsSection title="Corners" description="How rounded buttons, fields, cards and dialogs are. Square removes rounding everywhere, including badges and avatars.">
	<div class="grid grid-cols-2 gap-3 xl:grid-cols-4">
		{#each radii as r (r.id)}
			{@const k = scale[r.id]}
			<button class="flex-col !items-stretch {choice} {theme.radius === r.id ? on : off}" aria-pressed={theme.radius === r.id} onclick={() => setRadius(r.id)}>
				<span class="flex h-16 items-end gap-2 border border-rule-soft bg-paper p-2.5" style="border-radius: {10 * k}px !important" aria-hidden="true">
					<span class="h-7 flex-1 border border-rule bg-raised" style="border-radius: {6 * k}px !important"></span>
					<span class="h-7 w-10 bg-action" style="border-radius: {6 * k}px !important"></span>
					<span class="size-3 self-start bg-run" style="border-radius: {k ? 999 : 0}px !important"></span>
				</span>
				<span class="flex items-center justify-between gap-2">
					<span class="min-w-0"><span class="block font-semibold">{r.name}</span><span class="block text-small text-muted">{r.text}</span></span>
					{#if theme.radius === r.id}<Icon name="check" class="shrink-0 text-action" />{/if}
				</span>
			</button>
		{/each}
	</div>
</SettingsSection>

<SettingsSection title="Accent color" description="Used for selected navigation, links, focus rings and primary actions. Each is tuned separately for light and dark surfaces.">
	<div class="grid grid-cols-2 gap-2.5 sm:grid-cols-3 xl:grid-cols-4">
		{#each accents as accent (accent.id)}
			<button class="{choice} !p-2.5 {theme.accent === accent.id ? on : off}" aria-pressed={theme.accent === accent.id} onclick={() => setAccent(accent.id as Accent)}>
				<span class="relative size-7 shrink-0 overflow-hidden rounded-pill border border-black/10" aria-hidden="true">
					<span class="absolute inset-y-0 left-0 w-1/2" style="background:{accent.light}"></span>
					<span class="absolute inset-y-0 right-0 w-1/2" style="background:{accent.dark}"></span>
				</span>
				<span class="min-w-0 flex-1 font-medium">{accent.name}</span>
				{#if theme.accent === accent.id}<Icon name="check" class="text-action" />{/if}
			</button>
		{/each}
	</div>
</SettingsSection>
