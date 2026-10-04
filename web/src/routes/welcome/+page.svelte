<script lang="ts">
	import { session } from '$lib/session.svelte';
	import { theme, cycleTheme } from '$lib/ui/theme.svelte';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import { toast } from '$lib/ui/toast.svelte';

	const repo = 'https://github.com/xenycx/rivetpanel';
	const features: { icon: IconName; title: string; text: string }[] = [
		{ icon: 'shield', title: 'Hardened isolation', text: 'Every bot runs in its own container: memory, CPU and process limits, no capabilities, read-only root, non-root user.' },
		{ icon: 'terminal', title: 'Live console', text: 'Stream output, send input, pause, copy and download, with permissions checked on every line.' },
		{ icon: 'github', title: 'Deploy from GitHub', text: 'Pick a repository and branch. Pushes deploy automatically, with history, a changes preview and roll back.' },
		{ icon: 'archive', title: 'Crash-safe backups', text: 'Scheduled and manual snapshots with sealed secrets. Restores and deploys roll back cleanly if the server dies mid-way.' },
		{ icon: 'clock', title: 'Schedules', text: 'Nightly backups, weekly restarts, timed deploys. Time zones, daylight saving and no surprise catch-up runs.' },
		{ icon: 'activity', title: 'Health & alerts', text: 'Bots report a heartbeat. Get Discord alerts for crashes, failed deploys, backups and silent bots.' },
		{ icon: 'users', title: 'Teams', text: 'Share bots with fine-grained permissions or one-use invitation links, and see who changed what.' },
		{ icon: 'key', title: 'Secrets, sealed', text: 'Environment variables are encrypted with AES-256-GCM and masked everywhere. Two-step sign-in for accounts.' },
		{ icon: 'code', title: 'Automation API', text: 'Scoped tokens for CI: deploy, restart, back up. Idempotent requests and an OpenAPI description.' },
		{ icon: 'chart', title: 'Analytics', text: 'Guilds, latency, commands and events from tiny SDKs for JavaScript, Python, Go, Rust, Java and Ruby.' },
		{ icon: 'folder', title: 'Files & SFTP', text: 'A web editor with conflict detection, zip upload, package manager, and SFTP with per-bot folders.' },
		{ icon: 'sliders', title: 'Capacity control', text: 'Memory budgets per server and per user, build slots and disk checks, so a small host never overcommits.' }
	];
	const langs = [
		{ name: 'discord.js', lang: 'JavaScript', color: '#f0c14b' },
		{ name: 'TypeScript', lang: 'Node 24', color: '#4a8fe7' },
		{ name: 'discord.py', lang: 'Python', color: '#4b8bbe' },
		{ name: 'Poise', lang: 'Rust', color: '#e2753a' },
		{ name: 'JDA', lang: 'Java', color: '#e76f51' },
		{ name: 'DiscordGo', lang: 'Go', color: '#29beb0' },
		{ name: 'discordrb', lang: 'Ruby', color: '#d6453d' }
	];
	const install = `docker run -d --name rivetpanel --restart unless-stopped \\
  -p 8080:8080 \\
  -v /var/run/docker.sock:/var/run/docker.sock \\
  -v /var/lib/rivetpanel:/var/lib/rivetpanel \\
  ghcr.io/xenycx/rivetpanel:latest`;
	function copy() {
		navigator.clipboard?.writeText(install).then(() => toast('Command copied', 'success'));
	}
	const fleet = [
		{ name: 'moderation', meta: 'discord.js · 256 MiB', state: 'Running', tone: 'run' },
		{ name: 'music-rs', meta: 'Rust · building', state: 'Building', tone: 'warn' },
		{ name: 'tickets', meta: 'discord.py · 128 MiB', state: 'Running', tone: 'run' },
		{ name: 'economy', meta: 'Go · stopped for backup', state: 'Stopped', tone: 'idle' }
	];
</script>

<svelte:head>
	<title>RivetPanel · Self-hosted Discord bot hosting</title>
	<meta name="description" content="Run every Discord bot on your own server with container isolation, GitHub deploys, backups, schedules and a live console. One small binary." />
</svelte:head>

<div class="landing">
	<header class="sticky top-0 z-30 border-b border-rule-soft backdrop-blur-md" style="background: color-mix(in srgb, var(--color-paper) 80%, transparent)">
		<nav class="mx-auto flex h-16 max-w-6xl items-center gap-6 px-5" aria-label="Main">
			<a href="/" class="flex items-center gap-2.5" aria-label="RivetPanel">
				<img src="/favicon.svg" alt="" width="30" height="30" class="rounded-tile" />
				<span class="font-semibold tracking-[0.08em] uppercase">RivetPanel</span>
			</a>
			<div class="hidden items-center gap-5 text-muted md:flex">
				<a href="#features" class="hover:text-ink">Features</a>
				<a href="#how" class="hover:text-ink">How it works</a>
				<a href="#self-host" class="hover:text-ink">Self-host</a>
				<a href={repo} class="hover:text-ink" target="_blank" rel="noopener">GitHub</a>
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
		<section class="relative overflow-hidden">
			<div class="glow" aria-hidden="true"></div>
			<div class="mx-auto grid max-w-6xl items-center gap-12 px-5 pt-16 pb-20 lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)] lg:pt-24">
				<div>
					<p class="eyebrow">Self-hosted Discord bot hosting</p>
					<h1 class="mt-4 text-[2.6rem] leading-[1.05] font-semibold tracking-tight sm:text-[3.4rem]">Run every bot.<br />Know its <span class="text-action">state</span>.</h1>
					<p class="mt-5 max-w-xl text-title leading-relaxed text-muted">One small binary turns your server into a bot host: container isolation, GitHub deploys, crash-safe backups, schedules, alerts and a live console, for bots in six languages.</p>
					<div class="mt-8 flex flex-wrap gap-3">
						<a href={session.user ? '/dashboard' : '/login'} class="btn btn-primary h-11 px-5">{session.user ? 'Open your panel' : 'Sign in to your panel'}<Icon name="chevronRight" size={14} /></a>
						<a href="#self-host" class="btn h-11 px-5"><Icon name="download" size={15} />Install it</a>
					</div>
					<dl class="mt-10 grid max-w-lg grid-cols-3 gap-4">
						<div><dt class="eyebrow">Idle memory</dt><dd class="mt-1 font-mono text-section font-medium">&lt; 50 MB</dd></div>
						<div><dt class="eyebrow">Languages</dt><dd class="mt-1 font-mono text-section font-medium">6</dd></div>
						<div><dt class="eyebrow">Services</dt><dd class="mt-1 font-mono text-section font-medium">1 binary</dd></div>
					</dl>
				</div>

				<!-- Product preview -->
				<div class="relative" aria-label="Preview of the fleet overview" role="img">
					<div class="card preview p-5">
						<div class="flex items-center justify-between">
							<p class="eyebrow">Fleet</p>
							<span class="pill" data-tone="run"><span class="side-dot !m-0 !size-1.5" data-tone="run"></span>2 running</span>
						</div>
						<ul class="mt-4 grid gap-2">
							{#each fleet as b (b.name)}
								<li class="spine flex items-center gap-3 rounded-tile border border-rule-soft bg-paper py-3 pr-4 pl-5" data-tone={b.tone} data-busy={b.tone === 'warn'}>
									<div class="min-w-0 flex-1">
										<p class="font-semibold">{b.name}</p>
										<p class="text-small text-muted">{b.meta}</p>
									</div>
									<span class="pill" data-tone={b.tone}>{b.state}</span>
								</li>
							{/each}
						</ul>
						<div class="mt-4 grid grid-cols-2 gap-4 border-t border-rule-soft pt-4">
							<div class="flex items-center gap-3"><span class="eyebrow">RAM</span><div class="meter flex-1"><i style="width: 42%"></i></div></div>
							<div class="flex items-center gap-3"><span class="eyebrow">CPU</span><div class="meter flex-1"><i style="width: 18%"></i></div></div>
						</div>
					</div>
					<div class="card console absolute -bottom-8 -left-4 hidden w-72 p-3 font-mono text-[12px] sm:block" aria-hidden="true">
						<p class="text-muted">$ console · moderation</p>
						<p class="mt-1"><span class="text-run">✓</span> Logged in as Moderator#0420</p>
						<p><span class="text-run">✓</span> /ping registered in 12 guilds</p>
						<p class="text-action">▍</p>
					</div>
				</div>
			</div>
		</section>

		<!-- Languages -->
		<section class="border-y border-rule-soft">
			<div class="mx-auto flex max-w-6xl flex-wrap items-center justify-center gap-x-8 gap-y-4 px-5 py-7">
				<span class="eyebrow">Starter templates</span>
				{#each langs as l (l.name)}
					<span class="flex items-center gap-2 text-small"><span class="size-2 rounded-pill" style="background:{l.color}"></span><span class="font-medium">{l.name}</span><span class="text-muted">{l.lang}</span></span>
				{/each}
			</div>
		</section>

		<!-- Features -->
		<section id="features" class="mx-auto max-w-6xl scroll-mt-20 px-5 py-20">
			<p class="eyebrow">Everything a bot needs</p>
			<h2 class="mt-3 max-w-2xl text-[2rem] leading-tight font-semibold tracking-tight">From first <code class="text-action">/ping</code> to a fleet you can trust<span class="text-action">.</span></h2>
			<ul class="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
				{#each features as f (f.title)}
					<li class="card feature p-5">
						<span class="grid size-10 place-items-center rounded-tile" style="background: color-mix(in srgb, var(--color-action) 13%, transparent)"><Icon name={f.icon} class="text-action" size={18} /></span>
						<h3 class="mt-4 text-title font-semibold">{f.title}</h3>
						<p class="mt-1.5 text-small leading-relaxed text-muted">{f.text}</p>
					</li>
				{/each}
			</ul>
		</section>

		<!-- How it works -->
		<section id="how" class="scroll-mt-20 border-t border-rule-soft">
			<div class="mx-auto max-w-6xl px-5 py-20">
				<p class="eyebrow">How it works</p>
				<h2 class="mt-3 text-[2rem] leading-tight font-semibold tracking-tight">Three steps to a running bot<span class="text-action">.</span></h2>
				<ol class="mt-10 grid gap-4 md:grid-cols-3">
					{#each [{ t: 'Start the panel', d: 'One container (or one binary with systemd) next to Docker. It prints a setup code in its log.' }, { t: 'Finish the setup', d: 'Create the administrator, set the address, and optionally connect GitHub and Discord in the browser.' }, { t: 'Deploy a bot', d: 'Pick a template or a repository, paste the token, press Start. Watch the build and the console live.' }] as s, i (s.t)}
						<li class="card relative p-6">
							<span class="font-mono text-[2.5rem] leading-none font-semibold text-action/30">0{i + 1}</span>
							<h3 class="mt-3 text-title font-semibold">{s.t}</h3>
							<p class="mt-1.5 text-small leading-relaxed text-muted">{s.d}</p>
						</li>
					{/each}
				</ol>
			</div>
		</section>

		<!-- Self-host -->
		<section id="self-host" class="scroll-mt-20 border-t border-rule-soft">
			<div class="mx-auto grid max-w-6xl items-center gap-10 px-5 py-20 lg:grid-cols-2">
				<div>
					<p class="eyebrow">Self-host</p>
					<h2 class="mt-3 text-[2rem] leading-tight font-semibold tracking-tight">Your server, your bots, your data<span class="text-action">.</span></h2>
					<p class="mt-4 max-w-prose text-muted">SQLite instead of a database server, no message broker, no cloud account. Secrets are encrypted with keys that stay on your disk. Linux on amd64 or arm64 with Docker is all it needs.</p>
					<ul class="mt-6 grid gap-2 text-small">
						{#each ['Docker image on GitHub Packages, or a release archive with systemd units', 'Automatic HTTPS works behind Caddy, nginx or a Cloudflare Tunnel', 'Backup, restore and key rotation from the command line'] as li (li)}
							<li class="flex gap-2.5"><Icon name="check" class="mt-[3px] shrink-0 text-run" size={14} />{li}</li>
						{/each}
					</ul>
				</div>
				<div class="card overflow-hidden">
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
				<h2 class="text-[1.75rem] font-semibold tracking-tight">Ready when your bots are<span class="text-action">.</span></h2>
				<div class="mt-6 flex flex-wrap justify-center gap-3">
					<a href={session.user ? '/dashboard' : '/login'} class="btn btn-primary h-11 px-5">{session.user ? 'Open your panel' : 'Sign in'}</a>
					<a href={repo} class="btn h-11 px-5" target="_blank" rel="noopener"><Icon name="github" size={16} />View on GitHub</a>
				</div>
			</div>
		</section>
	</main>

	<footer class="border-t border-rule-soft">
		<div class="mx-auto flex max-w-6xl flex-wrap items-center gap-4 px-5 py-8 text-small text-muted">
			<img src="/favicon.svg" alt="" width="20" height="20" class="rounded-control" />
			<span>RivetPanel · self-hosted Discord bot hosting</span>
			<span class="flex-1"></span>
			<a href={repo} class="hover:text-ink" target="_blank" rel="noopener">Source</a>
			<a href="{repo}/tree/main/docs" class="hover:text-ink" target="_blank" rel="noopener">Documentation</a>
		</div>
	</footer>
</div>

<style>
	.landing {
		min-height: 100dvh;
	}
	.glow {
		position: absolute;
		inset: -30% -10% auto 35%;
		height: 720px;
		pointer-events: none;
		background: radial-gradient(closest-side, color-mix(in srgb, var(--color-action) 22%, transparent), transparent);
		filter: blur(10px);
	}
	.preview {
		box-shadow: var(--shadow-overlay);
		transform: perspective(1400px) rotateY(-6deg) rotateX(3deg);
	}
	.console {
		box-shadow: var(--shadow-overlay);
		background: var(--color-term);
		color: var(--color-term-ink);
		border-color: color-mix(in srgb, var(--color-term-ink) 12%, transparent);
	}
	.feature {
		transition:
			border-color 160ms,
			transform 160ms;
	}
	.feature:hover {
		border-color: color-mix(in srgb, var(--color-action) 45%, var(--color-rule));
		transform: translateY(-2px);
	}
	.dot {
		display: block;
		width: 9px;
		height: 9px;
		border-radius: 999px;
		background: var(--color-rule);
	}
	@media (prefers-reduced-motion: reduce) {
		.preview {
			transform: none;
		}
	}
</style>
