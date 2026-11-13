<script lang="ts">
	import { statusLabel, staffStatusShort } from '$lib/api/support';

	// A ticket status as a tinted lozenge with a lifecycle dot (see app.css,
	// .lozenge). Staff see "Waiting"; requesters "Waiting for you".
	let { status, staff = false, class: cls = '' }: { status: string; staff?: boolean; class?: string } = $props();
	const text = $derived((staff ? staffStatusShort : statusLabel)[status] ?? status);
</script>

<span class="lozenge {cls}" data-s={status}><span class="lozenge-dot" aria-hidden="true"></span>{text}</span>
