<script lang="ts">
	import SubNav from '$lib/components/ui/SubNav.svelte';
	let { children } = $props();
	const links = [
		{ href: '/settings/profile', label: 'Profile', icon: 'users' as const },
		{ href: '/settings/appearance', label: 'Appearance', icon: 'sliders' as const },
		{ href: '/settings/notifications', label: 'Notifications', icon: 'bell' as const },
		{ href: '/settings/workspaces', label: 'Workspaces', icon: 'building' as const, match: '/settings/workspaces/' },
		{ href: '/settings/connected-accounts', label: 'Connected accounts', icon: 'link' as const },
		{ href: '/settings/security', label: 'Security', icon: 'shield' as const },
		{ href: '/settings/sftp', label: 'SFTP and API keys', icon: 'key' as const }
	];
</script>

<header class="border-b border-rule-soft pb-5">
	<h1 class="text-page">Settings</h1>
	<p class="mt-0.5 text-muted">Your account on this panel.</p>
</header>
<div class="mt-6 flex flex-col gap-6 md:flex-row md:gap-10">
	<SubNav {links} label="Settings sections" />
	<div class="min-w-0 flex-1">{@render children()}</div>
</div>
