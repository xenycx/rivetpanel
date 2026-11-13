<script lang="ts">
	import Figure from './Figure.svelte';
	import Sequence, { type Actor, type Step } from './Sequence.svelte';
	const actors: Actor[] = [
		{ id: 'a', name: 'Administrator', icon: 'users' },
		{ id: 'p', name: 'RivetPanel', icon: 'panel', k: 'panel' },
		{ id: 'n', name: 'Node', sub: 'rivet-agent', icon: 'server', k: 'docker' }
	];
	const steps: Step[] = [
		{ from: 'a', to: 'p', label: 'Add remote node' },
		{ from: 'p', to: 'a', label: 'One-time command; the token is stored only as a hash' },
		{ from: 'a', to: 'n', label: 'Run rivet-agent enroll on the node' },
		{ from: 'n', to: 'p', label: 'Exchange the token' },
		{ from: 'p', to: 'n', label: 'Node certificate (30 days) and CA; the token is used up' },
		{ from: 'n', to: 'n', label: 'Write node.pem, a root credential', k: 'docker' },
		{ from: 'n', to: 'p', label: 'Dial out over TLS 1.3, checked on every handshake' },
		{ from: 'p', to: 'n', label: 'Desired state, files, logs and console in that connection' }
	];
</script>

<Figure caption="Enrollment turns a one-time token into a node-bound certificate. After that the agent always dials the panel, never the other way round, and renews its certificate over the same connection.">
	<Sequence {actors} {steps} />
</Figure>
