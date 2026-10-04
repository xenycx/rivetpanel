<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { Module } from '$lib/api/types';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let modules = $state<Module[] | null>(null);
	let error = $state('');

	onMount(async () => {
		try {
			modules = (await api<{ modules: Module[] }>('GET', '/modules')).modules;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The module catalog could not be loaded.';
		}
	});

	const tone = (state: Module['state']) => (state === 'stable' ? 'run' : state === 'preview' ? 'warn' : undefined);
</script>

<svelte:head><title>Modules · RivetPanel</title></svelte:head>

<div class="flex flex-wrap items-start justify-between gap-3">
	<div>
		<h2 class="text-section">Modules</h2>
		<p class="mt-1 max-w-3xl text-muted">Stable capabilities are ready for normal use. Preview capabilities are opt-in and may still change. Off modules expose no user interface or API.</p>
	</div>
	<a class="btn btn-secondary" href="/admin/environment?find=RIVET_MODULES">Configure modules</a>
</div>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if !modules && !error}
	<div class="mt-5"><Skeleton rows={6} label="Loading modules" /></div>
{:else if modules}
	<ul class="list-card mt-5">
		{#each modules as module (module.key)}
			<li class="flex flex-col gap-2 px-4 py-3.5 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
				<div class="min-w-0">
					<div class="flex flex-wrap items-center gap-2">
						<h3 class="font-medium">{module.label}</h3>
						<code class="text-small text-muted">{module.key}</code>
					</div>
					<p class="mt-1 text-small text-muted">{module.description}</p>
				</div>
				<span class="pill shrink-0" data-tone={tone(module.state)}>{module.state === 'off' ? 'Off' : module.state === 'preview' ? 'Preview' : 'Stable'}</span>
			</li>
		{/each}
	</ul>

	<Notice tone="info" class="mt-5" title="Changes apply after restart">
		Set <code>RIVET_MODULES</code> in Environment using entries such as <code>agents=preview,game_servers=preview</code>. A module marked preview is discoverable, but it does not imply that every planned capability is implemented.
	</Notice>
{/if}
