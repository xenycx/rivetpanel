<script lang="ts">
	import { session } from '$lib/session.svelte';
	import { theme, cycleTheme } from '$lib/ui/theme.svelte';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import GameIcon from '$lib/components/ui/GameIcon.svelte';
	import { toast } from '$lib/ui/toast.svelte';

	// What the product does today. Keep every claim in line with
	// docs/features.md; "Preview" marks modules that are opt-in previews.
	const repo = 'https://github.com/xenycx/rivetpanel';
	type Feature = { icon: IconName; title: string; text: string };
	type Pillar = { id: string; eyebrow: string; title: string; lead: string; preview?: boolean; features: Feature[] };
	const pillars: Pillar[] = [
		{
			id: 'bots',
			eyebrow: 'Discord bot hosting',
			title: 'From first /ping to a fleet you can trust',
			lead: 'Every bot runs in its own locked-down container with memory, CPU and process limits, a non-root user and a read-only root file system.',
			features: [
				{ icon: 'layers', title: 'Starter templates', text: 'Working slash-command bots in JavaScript, TypeScript, Python, Rust, Java, Go and Ruby. Add the token and start.' },
				{ icon: 'github', title: 'Deploy from GitHub', text: 'Paste any public repository or pick your own. The panel detects language, commands and variables, deploys on push and can roll back.' },
				{ icon: 'terminal', title: 'Live console', text: 'Stream output, send input, and see the exact lifecycle state with live CPU, memory, disk and network gauges.' },
				{ icon: 'package', title: 'Databases and packages', text: 'Private PostgreSQL, Redis, MongoDB or MariaDB add-ons per bot, and a package manager for npm, PyPI, crates.io and Go modules.' },
				{ icon: 'globe', title: 'Public pages and sites', text: 'Give a bot a generated page or host static sites, starting from five templates, with custom domains, immutable releases and rollback.' },
				{ icon: 'chart', title: 'Bot insights', text: 'Guilds, latency, commands and events pushed by small SDKs for six languages, kept private to the bot’s team.' }
			]
		},
		{
			id: 'games',
			eyebrow: 'Game servers',
			title: 'Minecraft and Steam servers, without the fiddling',
			lead: 'Pick a game and a version and press Create. RivetPanel downloads the server, picks the right Java or SteamCMD build, reserves the ports and accepts nothing on your behalf: licence terms are yours to confirm.',
			preview: true,
			features: [
				{ icon: 'cube', title: 'Eight Minecraft flavours', text: 'Paper, Purpur, Vanilla, Fabric, Forge, NeoForge, Folia and the Velocity proxy, with checksummed downloads and automatic Java selection.' },
				{ icon: 'gamepad', title: 'Steam dedicated servers', text: 'Valheim, Rust and Project Zomboid installed with an anonymous SteamCMD login, saved with the game’s own stop signal.' },
				{ icon: 'download', title: 'Plugins and mods', text: 'Install from Modrinth with SHA-512 verification. Manage players, whitelists and server.properties from the panel.' },
				{ icon: 'upload', title: 'Bring your own eggs', text: 'Convert a Pterodactyl egg into a server type. Everything that could not be mapped is listed for review first.' }
			]
		},
		{
			id: 'infra',
			eyebrow: 'Infrastructure',
			title: 'One control plane, as many hosts as you need',
			lead: 'Start on one machine. Add more with a small agent that connects out to the panel, so nodes never need an open management port.',
			features: [
				{ icon: 'monitor', title: 'Remote nodes', text: 'rivet-agent runs servers on other Docker hosts over mutually authenticated TLS: console, files, backups, deploys and stats. Preview.' },
				{ icon: 'archive', title: 'Crash-safe backups', text: 'Manual and scheduled snapshots with sealed secrets. Restores and deploys are journaled and roll back cleanly if the host dies mid-way.' },
				{ icon: 'clock', title: 'Schedules', text: 'Backups, restarts, deploys and console command chains on a timetable, in any time zone, with no surprise catch-up runs.' },
				{ icon: 'folder', title: 'Files and SFTP', text: 'A web editor with conflict detection, zip upload and extract, and SFTP with one folder per server, keys included.' },
				{ icon: 'sliders', title: 'Capacity control', text: 'Memory budgets per node and per account, build slots and free-disk checks, so a small host never overcommits.' },
				{ icon: 'chart', title: 'Usage analytics', text: 'CPU, memory, network, uptime, crashes, deploys and backups per server, and panel-wide trends with CSV export.' }
			]
		},
		{
			id: 'identity',
			eyebrow: 'Teams and identity',
			title: 'Share safely, sign in your way',
			lead: 'Workspaces group bots and servers for a team; roles decide what each person and each token may do, and the server checks it on every request.',
			features: [
				{ icon: 'building', title: 'Workspaces and roles', text: 'Team workspaces, custom roles with fine-grained permissions, per-server sharing and one-use invitation links.' },
				{ icon: 'key', title: 'Single sign-on and passkeys', text: 'OpenID Connect providers, GitHub and Discord sign-in, passkeys and two-step codes with recovery codes.' },
				{ icon: 'code', title: 'API clients and automation', text: 'Scoped API clients for the whole API and automation tokens for CI, both revocable and audited.' },
				{ icon: 'shield', title: 'Secrets, sealed', text: 'Environment variables are encrypted with AES-256-GCM, masked everywhere and never written to backups in clear.' }
			]
		},
		{
			id: 'support',
			eyebrow: 'Support and communication',
			title: 'Keep people informed',
			lead: 'Everything a hosting community needs to ask for help and stay up to date, built into the same panel.',
			features: [
				{ icon: 'bell', title: 'Notifications', text: 'A bell for crashes, deploys, backups, sharing, replies and announcements, with in-panel and email choices per category.' },
				{ icon: 'lifebuoy', title: 'Support tickets', text: 'Threaded tickets with statuses, staff assignment and internal notes, linked to the server they are about.' },
				{ icon: 'book', title: 'Help center', text: 'Markdown articles for everyone, signed-in accounts or staff, suggested while someone types a ticket subject.' },
				{ icon: 'activity', title: 'Status page', text: 'Public components with automatic states, incidents, maintenance windows and 90-day uptime bars.' }
			]
		}
	];
	const nav = [
		{ href: '#bots', label: 'Bots' },
		{ href: '#games', label: 'Game servers' },
		{ href: '#infra', label: 'Infrastructure' },
		{ href: '#identity', label: 'Teams' },
		{ href: '#support', label: 'Support' },
		{ href: '#self-host', label: 'Self-host' }
	];
	const games = [
		{ slug: 'minecraft-paper', name: 'Paper' },
		{ slug: 'minecraft-fabric', name: 'Fabric' },
		{ slug: 'minecraft-forge', name: 'Forge' },
		{ slug: 'minecraft-vanilla', name: 'Vanilla' },
		{ slug: 'steam-valheim', name: 'Valheim' },
		{ slug: 'steam-rust', name: 'Rust' },
		{ slug: 'steam-project-zomboid', name: 'Project Zomboid' }
	];
	const langs = [
		{ name: 'discord.js', color: '#f0c14b' },
		{ name: 'TypeScript', color: '#4a8fe7' },
		{ name: 'discord.py', color: '#4b8bbe' },
		{ name: 'Poise', color: '#e2753a' },
		{ name: 'JDA', color: '#e76f51' },
		{ name: 'DiscordGo', color: '#29beb0' },
		{ name: 'discordrb', color: '#d6453d' }
	];
	const install = `docker run -d --name rivetpanel --restart unless-stopped \\
  -p 8080:8080 \\
  -v /var/run/docker.sock:/var/run/docker.sock \\
  -v /var/lib/rivetpanel:/var/lib/rivetpanel \\
  ghcr.io/xenycx/rivetpanel:latest`;
	function copy() {
		navigator.clipboard?.writeText(install).then(() => toast('Command copied', 'success'));
	}
	const fleet: { name: string; meta: string; state: string; tone: string; game?: string }[] = [
		{ name: 'moderation', meta: 'discord.js · 256 MiB', state: 'Running', tone: 'run' },
		{ name: 'survival', meta: 'Paper 1.21 · 4/20 players', state: 'Running', tone: 'run', game: 'minecraft-paper' },
		{ name: 'music-rs', meta: 'Rust · building', state: 'Building', tone: 'warn' },
		{ name: 'vikings', meta: 'Valheim · node eu-2', state: 'Installing', tone: 'warn', game: 'steam-valheim' }
	];
</script>

<svelte:head>
	<title>RivetPanel · Self-hosted bot and game server hosting</title>
	<meta name="description" content="Host Discord bots and Minecraft or Steam game servers on your own machines: container isolation, GitHub deploys, backups, remote nodes, teams, support and an AI assistant in one small binary." />
</svelte:head>

<div class="landing">
	<header class="sticky top-0 z-30 border-b border-rule-soft backdrop-blur-md" style="background: color-mix(in srgb, var(--color-paper) 80%, transparent)">
		<nav class="mx-auto flex h-16 max-w-6xl items-center gap-6 px-5" aria-label="Main">
			<a href="/" class="flex items-center gap-2.5" aria-label="RivetPanel">
				<img src="/favicon.svg" alt="" width="30" height="30" class="rounded-tile" />
				<span class="font-semibold">RivetPanel</span>
			</a>
			<div class="hidden items-center gap-5 text-muted lg:flex">
				{#each nav as n (n.href)}<a href={n.href} class="hover:text-ink">{n.label}</a>{/each}
			</div>
			<div class="ml-auto flex items-center gap-2">
				<button class="tb-btn" onclick={cycleTheme} aria-label="Switch theme"><Icon name={theme.pref === 'system' ? 'monitor' : theme.dark ? 'moon' : 'sun'} size={16} /></button>
				{#if session.user}
					<a href="/dashboard" class="btn btn-primary">Open panel</a>
				{:else}
					<a href="/login" class="btn btn-primary">Sign in</a>
				{/if}
			</div>
		</nav>
	</header>

	<main>
		<!-- Hero -->
		<section>
			<div class="mx-auto grid max-w-6xl items-center gap-12 px-5 pt-14 pb-16 lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)] lg:pt-20">
				<div>
					<p class="eyebrow">Self-hosted bot and game server hosting</p>
					<h1 class="mt-3 text-[2.25rem] leading-[1.1] font-semibold tracking-tight sm:text-[2.875rem]">Run every bot and server.<br />Know its state.</h1>
					<p class="mt-4 max-w-xl text-title leading-relaxed text-muted">One small binary turns your machines into a host for Discord bots and Minecraft or Steam game servers: container isolation, GitHub deploys, crash-safe backups, remote nodes, teams, support tools and an AI assistant.</p>
					<div class="mt-7 flex flex-wrap gap-3">
						<a href={session.user ? '/dashboard' : '/login'} class="btn btn-primary h-10 px-4">{session.user ? 'Open your panel' : 'Sign in to your panel'}</a>
						<a href="#self-host" class="btn h-10 px-4"><Icon name="download" size={15} />Install it</a>
					</div>
					<dl class="mt-10 grid max-w-lg grid-cols-3 gap-4">
						<div><dt class="eyebrow">Idle memory</dt><dd class="mt-1 font-mono text-section font-medium">&lt; 50 MB</dd></div>
						<div><dt class="eyebrow">Server types</dt><dd class="mt-1 font-mono text-section font-medium">11 games</dd></div>
						<div><dt class="eyebrow">Services</dt><dd class="mt-1 font-mono text-section font-medium">1 binary</dd></div>
					</dl>
				</div>

				<!-- Product preview -->
				<div class="grid gap-3" aria-label="Preview of the overview with bots and game servers" role="img">
					<div class="card p-5">
						<div class="flex items-center justify-between">
							<p class="eyebrow">Fleet</p>
							<span class="pill" data-tone="run"><span class="side-dot !m-0 !size-1.5" data-tone="run"></span>2 running</span>
						</div>
						<ul class="mt-4 grid gap-2">
							{#each fleet as b (b.name)}
								<li class="spine flex items-center gap-3 rounded-tile border border-rule-soft bg-paper py-2.5 pr-4 pl-5" data-tone={b.tone} data-busy={b.tone === 'warn'}>
									{#if b.game}<GameIcon type={{ slug: b.game }} size={32} />{:else}<span class="grid size-8 place-items-center rounded-tile bg-paper-2/70 text-action"><Icon name="discord" size={16} /></span>{/if}
									<div class="min-w-0 flex-1">
										<p class="font-semibold">{b.name}</p>
										<p class="truncate text-small text-muted">{b.meta}</p>
									</div>
									<span class="pill" data-tone={b.tone}>{b.state}</span>
								</li>
							{/each}
						</ul>
						<div class="mt-4 grid grid-cols-2 gap-4 border-t border-rule-soft pt-4">
							<div class="flex items-center gap-3"><span class="eyebrow">RAM</span><div class="meter flex-1"><i style="width: 46%"></i></div></div>
							<div class="flex items-center gap-3"><span class="eyebrow">CPU</span><div class="meter flex-1"><i style="width: 22%"></i></div></div>
						</div>
					</div>
					<div class="console hidden rounded-tile border p-3 font-mono text-[12px] sm:block" aria-hidden="true">
						<p class="text-muted">$ console · survival</p>
						<p class="mt-1"><span class="text-run">✓</span> Done (3.2s)! For help, type "help"</p>
						<p><span class="text-run">✓</span> Steve joined the game</p>
						<p class="text-muted">▍</p>
					</div>
				</div>
			</div>
		</section>

		<!-- What runs here -->
		<section class="border-y border-rule-soft">
			<div class="mx-auto grid max-w-6xl gap-6 px-5 py-7 md:grid-cols-2 md:gap-10">
				<div class="flex flex-wrap items-center gap-x-5 gap-y-3">
					<span class="eyebrow w-full">Bot templates</span>
					{#each langs as l (l.name)}
						<span class="flex items-center gap-2 text-small"><span class="size-2 rounded-pill" style="background:{l.color}"></span><span class="font-medium">{l.name}</span></span>
					{/each}
				</div>
				<div class="flex flex-wrap items-center gap-x-4 gap-y-3">
					<span class="eyebrow w-full">Game servers</span>
					{#each games as g (g.slug)}
						<span class="flex items-center gap-2 text-small"><GameIcon type={{ slug: g.slug }} size={24} class="!bg-transparent" /><span class="font-medium">{g.name}</span></span>
					{/each}
					<span class="text-small text-muted">and more</span>
				</div>
			</div>
		</section>

		<!-- Pillars -->
		{#each pillars as p, i (p.id)}
			<section id={p.id} class="scroll-mt-20 {i ? 'border-t border-rule-soft' : ''}">
				<div class="mx-auto max-w-6xl px-5 py-14 sm:py-16">
					<div class="grid gap-4 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)] lg:items-end lg:gap-12">
						<div>
							<p class="eyebrow flex items-center gap-2">{p.eyebrow}{#if p.preview}<span class="pill" data-tone="warn">Preview</span>{/if}</p>
							<h2 class="mt-3 max-w-xl text-[1.75rem] leading-tight font-semibold tracking-tight">{p.title}</h2>
						</div>
						<p class="max-w-prose text-muted">{p.lead}</p>
					</div>
					{#if p.id === 'games'}
						<ul class="mt-8 flex flex-wrap gap-2" aria-label="Supported games">
							{#each games as g (g.slug)}
								<li class="flex items-center gap-2.5 rounded-tile border border-rule-soft bg-panel py-1.5 pr-3.5 pl-1.5"><GameIcon type={{ slug: g.slug }} size={34} /><span class="font-medium">{g.name}</span></li>
							{/each}
							{#each ['Purpur', 'NeoForge', 'Folia', 'Velocity'] as n (n)}
								<li class="flex items-center gap-2.5 rounded-tile border border-rule-soft bg-panel py-1.5 pr-3.5 pl-1.5"><GameIcon type={{ slug: 'minecraft-' + n.toLowerCase() }} size={34} /><span class="font-medium">{n}</span></li>
							{/each}
						</ul>
					{/if}
					<ul class="mt-10 grid gap-x-8 gap-y-6 sm:grid-cols-2 {p.features.length % 3 === 0 ? 'lg:grid-cols-3' : 'lg:grid-cols-4'}">
						{#each p.features as f (f.title)}
							<li class="border-t border-rule-soft pt-4">
								<h3 class="flex items-center gap-2 text-title font-semibold"><Icon name={f.icon} class="text-muted" size={16} />{f.title}</h3>
								<p class="mt-1.5 text-small leading-relaxed text-muted">{f.text}</p>
							</li>
						{/each}
					</ul>
				</div>
			</section>
		{/each}

		<!-- AI -->
		<section class="border-t border-rule-soft">
			<div class="mx-auto max-w-6xl px-5 py-16">
				<div class="card grid gap-6 p-6 sm:p-8 md:grid-cols-[auto_minmax(0,1fr)] md:items-center">
					<span class="grid size-12 place-items-center rounded-tile bg-paper-2"><Icon name="sparkle" class="text-muted" size={22} /></span>
					<div>
						<p class="eyebrow">AI assistant</p>
						<h2 class="mt-2 text-[1.5rem] leading-tight font-semibold tracking-tight">Ask why it crashed</h2>
						<p class="mt-2 max-w-3xl text-muted">One private chat on every page, using the model provider your administrator configures. It knows which bot, server, site or file you are looking at, can read console output and build logs, and proposes changes that wait for your approval before anything is applied.</p>
					</div>
				</div>
			</div>
		</section>

		<!-- How it works -->
		<section id="how" class="scroll-mt-20 border-t border-rule-soft">
			<div class="mx-auto max-w-6xl px-5 py-20">
				<p class="eyebrow">How it works</p>
				<h2 class="mt-3 text-[1.75rem] leading-tight font-semibold tracking-tight">Three steps to something running</h2>
				<ol class="mt-8 grid gap-6 md:grid-cols-3">
					{#each [{ t: 'Start the panel', d: 'One container (or one binary with systemd) next to Docker. It prints a setup code in its log.' }, { t: 'Finish the setup', d: 'Create the administrator, set the address, and optionally connect GitHub, Discord, single sign-on and email in the browser.' }, { t: 'Create from the New menu', d: 'Deploy a bot from a template or repository, or pick a game and a version. Watch the build or install and the console live.' }] as s, i (s.t)}
						<li class="border-t border-rule pt-4">
							<h3 class="flex items-baseline gap-2.5 text-title font-semibold"><span class="font-mono text-small text-muted">{i + 1}.</span>{s.t}</h3>
							<p class="mt-1.5 text-small leading-relaxed text-muted">{s.d}</p>
						</li>
					{/each}
				</ol>
			</div>
		</section>

		<!-- Self-host -->
		<section id="self-host" class="scroll-mt-20 border-t border-rule-soft">
			<div class="mx-auto grid max-w-6xl items-center gap-10 px-5 py-20 lg:grid-cols-2">
				<div class="min-w-0">
					<p class="eyebrow">Self-host</p>
					<h2 class="mt-3 text-[1.75rem] leading-tight font-semibold tracking-tight">Your machines, your servers, your data</h2>
					<p class="mt-4 max-w-prose text-muted">SQLite instead of a database server, no message broker, no cloud account. Secrets are encrypted with keys that stay on your disk. Linux on amd64 or arm64 with Docker is all it needs.</p>
					<ul class="mt-6 grid gap-2 text-small">
						{#each ['Docker image on GitHub Packages, or a release archive with systemd units', 'Works behind Caddy, nginx or a Cloudflare Tunnel', 'Backup, restore and key rotation from the command line', 'Add remote nodes later with one enrollment command'] as li (li)}
							<li class="flex gap-2.5"><Icon name="check" class="mt-[3px] shrink-0 text-run" size={14} />{li}</li>
						{/each}
					</ul>
				</div>
				<div class="card min-w-0 overflow-hidden">
					<div class="flex items-center justify-between border-b border-rule-soft px-4 py-2.5">
						<span class="flex gap-1.5" aria-hidden="true"><i class="dot"></i><i class="dot"></i><i class="dot"></i></span>
						<button class="btn btn-sm btn-quiet" onclick={copy}><Icon name="copy" size={13} />Copy</button>
					</div>
					<pre class="overflow-x-auto bg-term p-5 font-mono text-[13px] leading-relaxed text-term-ink"><span class="text-muted"># then open http://your-server:8080 and enter the setup code</span>
{install}</pre>
				</div>
			</div>
		</section>

		<section class="border-t border-rule-soft">
			<div class="mx-auto max-w-6xl px-5 py-16 text-center">
				<h2 class="text-[1.5rem] font-semibold tracking-tight">Ready when your players are</h2>
				<div class="mt-6 flex flex-wrap justify-center gap-3">
					<a href={session.user ? '/dashboard' : '/login'} class="btn btn-primary h-10 px-4">{session.user ? 'Open your panel' : 'Sign in'}</a>
					<a href="/docs" class="btn h-10 px-4"><Icon name="book" size={16} />Documentation</a>
					<a href={repo} class="btn h-10 px-4" target="_blank" rel="noopener"><Icon name="github" size={16} />View on GitHub</a>
				</div>
			</div>
		</section>
	</main>

	<footer class="border-t border-rule-soft">
		<div class="mx-auto flex max-w-6xl flex-wrap items-center gap-4 px-5 py-8 text-small text-muted">
			<img src="/favicon.svg" alt="" width="20" height="20" class="rounded-control" />
			<span>RivetPanel · self-hosted bot and game server hosting</span>
			<span class="flex-1"></span>
			<a href="/status" class="hover:text-ink">Status</a>
			<a href="/help" class="hover:text-ink">Help center</a>
			<a href={repo} class="hover:text-ink" target="_blank" rel="noopener">Source</a>
		</div>
		<p class="mx-auto max-w-6xl px-5 pb-8 text-[11px] text-muted">Game names and logos belong to their owners and identify the games only. Icons from Dashboard Icons (Apache-2.0).</p>
	</footer>
</div>

<style>
	.landing {
		min-height: 100dvh;
		overflow-x: clip;
	}
	.console {
		background: var(--color-term);
		color: var(--color-term-ink);
		border-color: color-mix(in srgb, var(--color-term-ink) 12%, transparent);
	}
	.dot {
		display: block;
		width: 9px;
		height: 9px;
		border-radius: 999px;
		background: var(--color-rule);
	}
</style>
