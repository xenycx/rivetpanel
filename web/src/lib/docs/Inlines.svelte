<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import Self from './Inlines.svelte';
	import type { Inline, Enforcement } from './types';

	let { c }: { c: Inline[] } = $props();

	const ENF: Record<Enforcement, { label: string; title: string }> = {
		panel: { label: 'Panel', title: 'Enforced by the panel itself' },
		docker: { label: 'Docker', title: 'Enforced by the container runtime' },
		host: { label: 'Host', title: 'Needs host controls (kernel, filesystem or network); the panel cannot enforce it' },
		none: { label: 'Not enforced', title: 'Stored or recorded only; nothing enforces it yet' }
	};
	const external = (href: string) => /^https?:/i.test(href) && !href.startsWith(location.origin);
</script>

{#each c as x, i (i)}{#if typeof x === 'string'}{x}{:else if x.t === 'code'}<code class="doc-code">{x.v}</code>{:else if x.t === 'b'}<strong><Self c={x.c} /></strong>{:else if x.t === 'i'}<em><Self c={x.c} /></em>{:else if x.t === 'del'}<del><Self c={x.c} /></del>{:else if x.t === 'a'}<a class="link" href={x.href} target={external(x.href) ? '_blank' : undefined} rel={external(x.href) ? 'noreferrer' : undefined}><Self c={x.c} />{#if external(x.href)}<Icon name="external" size={11} class="ml-0.5 inline align-[-1px]" label="(opens in a new tab)" />{/if}</a>{:else if x.t === 'path'}<span class="doc-path">{#each x.parts as part, j (j)}{#if j}<Icon name="chevronRight" size={11} class="doc-path-sep" />{/if}<span>{part}</span>{/each}</span>{:else if x.t === 'enf'}<span class="doc-enf" data-k={x.k} title={ENF[x.k].title}><Icon name={x.k === 'none' || x.k === 'host' ? 'alert' : 'shield'} size={11} />{ENF[x.k].label}</span>{:else if x.t === 'br'}<br />{/if}{/each}
