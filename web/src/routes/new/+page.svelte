<script lang="ts">
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import { groupedCreateOptions } from '$lib/create';
	import { session } from '$lib/session.svelte';

	// One place to start anything: the workload kinds first, each with the
	// ways it can begin, then account and administration chores.
	const about: Record<string, { icon: IconName; text: string }> = {
		Bot: { icon: 'terminal', text: 'A Discord bot, worker or script in Node.js, Python, Go, Rust, Java, Ruby or PHP. The panel builds it and keeps it running.' },
		'Game server': { icon: 'gamepad', text: 'Minecraft and Steam server types, installed on the first start, with players, properties and mods.' },
		Container: { icon: 'box', text: 'Any image from an allowed registry, or a Compose file imported as a stack of services that reach each other by name.' },
		Website: { icon: 'globe', text: 'Static files, a static build from GitHub, or a running web app at its own address, with custom domains.' }
	};
	const groups = $derived(groupedCreateOptions());
	const workloads = $derived(groups.filter(([g]) => g in about));
	const chores = $derived(groups.filter(([g]) => !(g in about)));
	const admin = $derived(session.user?.role === 'admin');
</script>

<svelte:head><title>New · RivetPanel</title></svelte:head>

<header class="page-head">
	<h1 class="font-semibold">What are you running?</h1>
	<p class="text-small text-muted">Everything runs in its own container on this panel; pick the kind that fits and how it starts.</p>
</header>

<section class="mt-6 max-w-4xl divide-y divide-rule-soft border-y border-rule-soft" aria-label="Kinds of apps">
	{#each workloads as [group, opts] (group)}
		<div class="grid gap-3 py-5 md:grid-cols-[minmax(0,16rem)_minmax(0,1fr)] md:gap-8">
			<div class="flex items-start gap-3">
				<span class="grid size-9 shrink-0 place-items-center rounded-control bg-paper-2 text-muted"><Icon name={about[group].icon} size={16} /></span>
				<div class="min-w-0">
					<h2 class="text-title font-semibold">{group}</h2>
					<p class="mt-1 max-w-measure text-small text-muted">{about[group].text}</p>
				</div>
			</div>
			<ul class="grid content-start gap-1 sm:grid-cols-2">
				{#each opts as o (o.key)}
					<li>
						<a href={o.href} class="group flex items-start gap-2.5 rounded-control px-2.5 py-2 hover:bg-paper-2/70 focus-visible:bg-paper-2/70">
							<Icon name={o.icon} size={15} class="mt-0.5 shrink-0 text-muted group-hover:text-ink" />
							<span class="min-w-0">
								<span class="block font-medium">{o.label}</span>
								<span class="block text-small text-muted">{o.hint}</span>
							</span>
						</a>
					</li>
				{/each}
			</ul>
		</div>
	{:else}
		<p class="py-5 text-muted">Your account cannot create apps. Ask a workspace owner or an administrator for access.</p>
	{/each}
	{#if !session.features.containers && admin}
		<p class="py-4 text-small text-muted">
			Container apps and Compose stacks are off: turn on the <strong class="font-medium text-ink">Containers</strong> module under <a class="link" href="/admin/modules">Administration → Modules</a> and allow at least one registry (<code>RIVET_CONTAINER_REGISTRIES</code>).
		</p>
	{/if}
</section>

{#if workloads.length}
	<p class="mt-4 text-small text-muted"><a class="link" href="/templates">Browse every template</a> to compare starters for bots, game servers and websites side by side.</p>
{/if}

{#if chores.length}
	<section class="mt-8 max-w-4xl" aria-labelledby="more-h">
		<h2 id="more-h" class="text-title font-semibold">Also</h2>
		<ul class="mt-2 grid gap-1 sm:grid-cols-2 lg:grid-cols-3">
			{#each chores.flatMap(([, o]) => o) as o (o.key)}
				<li>
					<a href={o.href} class="group flex items-start gap-2.5 rounded-control px-2.5 py-2 hover:bg-paper-2/70">
						<Icon name={o.icon} size={15} class="mt-0.5 shrink-0 text-muted group-hover:text-ink" />
						<span class="min-w-0"><span class="block font-medium">{o.label}</span><span class="block text-small text-muted">{o.hint}</span></span>
					</a>
				</li>
			{/each}
		</ul>
	</section>
{/if}
