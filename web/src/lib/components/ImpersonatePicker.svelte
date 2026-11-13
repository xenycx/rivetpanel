<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import type { User } from '$lib/api/types';
	import { impersonate, session } from '$lib/session.svelte';
	import { picker } from '$lib/accounts.svelte';
	import { logoSeed } from '$lib/avatar';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';

	// Administrators: find an account by name or email and act as it with
	// Enter. The list loads once per opening; typing filters it locally.
	type Person = User & { bots?: number };
	let people = $state<Person[] | null>(null);
	let q = $state('');
	let active = $state(0);
	let busy = $state('');
	let error = $state('');
	let list: HTMLUListElement | undefined = $state();
	let input: HTMLInputElement | undefined = $state();

	$effect(() => {
		if (!picker.open) return;
		q = '';
		active = 0;
		error = '';
		busy = '';
		// Opened from a menu: the menu hands focus back to its button as it
		// closes, so take it once that has happened and typing goes here.
		requestAnimationFrame(() => input?.focus());
		api<{ users: Person[] }>('GET', '/users')
			.then((r) => (people = r.users))
			.catch((e) => (error = e instanceof ApiError ? e.message : 'The accounts could not be loaded.'));
	});

	// Why an account cannot be chosen ('' when it can).
	const blocked = (u: Person) => (u.id === session.user?.id ? 'You' : u.role === 'admin' ? 'Administrator' : u.disabled ? 'Disabled' : '');
	const matches = $derived.by(() => {
		const words = q.trim().toLowerCase().split(/\s+/).filter(Boolean);
		const all = (people ?? []).filter((u) => words.every((w) => `${u.display_name} ${u.email} ${u.role_name}`.toLowerCase().includes(w)));
		// Accounts that can be chosen first, then by name.
		all.sort((a, b) => Number(!!blocked(a)) - Number(!!blocked(b)) || (a.display_name || a.email).localeCompare(b.display_name || b.email));
		return all.slice(0, 50);
	});

	async function choose(u: Person | undefined) {
		if (!u || blocked(u) || busy) return;
		busy = u.id;
		error = '';
		try {
			await impersonate(u.id); // loads the panel as that account
		} catch (e) {
			error = e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) + '.' : 'Signing in as that account failed.';
			busy = '';
		}
	}
	function onkey(e: KeyboardEvent) {
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			active = Math.min(matches.length - 1, active + 1);
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			active = Math.max(0, active - 1);
		} else if (e.key === 'Enter') {
			e.preventDefault();
			void choose(matches[active]);
		}
		queueMicrotask(() => list?.querySelector(`[data-i="${active}"]`)?.scrollIntoView({ block: 'nearest' }));
	}
</script>

<Dialog bind:open={picker.open} title="Sign in as a user" description="See and use the panel exactly as that account does. Your own account stays signed in; return to it from the banner or the account menu." size="md">
	<label class="relative block">
		<span class="sr-only">Find an account by name or email</span>
		<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
		<input
			bind:this={input}
			class="field pl-8"
			placeholder="Name or email"
			bind:value={q}
			oninput={() => (active = 0)}
			onkeydown={onkey}
			role="combobox"
			aria-expanded="true"
			aria-controls="imp-list"
			aria-activedescendant={matches[active] ? `imp-${matches[active].id}` : undefined}
			autocomplete="off"
			data-autofocus
		/>
	</label>
	{#if error}<p class="mt-2 text-small text-fail" role="alert">{error}</p>{/if}
	<ul bind:this={list} id="imp-list" role="listbox" aria-label="Accounts" class="mt-2 max-h-[min(24rem,55vh)] overflow-y-auto">
		{#if people === null && !error}
			<li class="px-2 py-3 text-muted">Loading accounts…</li>
		{:else}
			{#each matches as u, i (u.id)}
				{@const why = blocked(u)}
				<li
					id="imp-{u.id}"
					data-i={i}
					role="option"
					aria-selected={i === active}
					aria-disabled={!!why}
					class="flex items-center gap-2.5 rounded-control px-2 py-1.5 text-[13px] {why ? 'opacity-55' : 'cursor-pointer'} {i === active ? 'bg-paper' : ''}"
					onmousemove={() => (active = i)}
					onclick={() => choose(u)}
					onkeydown={() => {}}
				>
					<Logo src={u.avatar_url} kind="user" seed={logoSeed(u.id, u.logo_variant)} class="size-6 rounded-pill" />
					<span class="min-w-0 flex-1">
						<span class="block truncate font-medium">{u.display_name || u.email}</span>
						<span class="block truncate text-[11.5px] leading-4 text-muted">{u.display_name ? `${u.email} · ` : ''}{u.role === 'admin' ? 'Administrator' : u.role_name || 'User'}{u.bots ? ` · ${u.bots} app${u.bots === 1 ? '' : 's'}` : ''}</span>
					</span>
					{#if busy === u.id}<span class="text-small text-muted">Opening…</span>{:else if why}<span class="text-small text-muted">{why}</span>{:else if i === active}<kbd class="text-small text-muted">Enter</kbd>{/if}
				</li>
			{:else}
				<li class="px-2 py-3 text-muted">No account matches “{q}”.</li>
			{/each}
		{/if}
	</ul>
	<p class="mt-1 border-t border-rule-soft pt-2 text-small text-muted">Administrators and disabled accounts cannot be chosen. Starting and ending are recorded in both accounts' activity.</p>
</Dialog>
