// How players reach a game server: the host and port to type into the game.
//
// The host is, in order: the allocation's alias (an administrator's name for
// that IP), the allocation's own IP when it is bound to one, and the node's
// address from GET /game-hosts (its public address under Administration →
// Nodes, else the node's own IP when the panel knows it). The panel's own
// host name is never used: it is for operators, not players. Without any of
// these the address is empty (source 'none'). Loopback and private addresses
// are still shown, with a note that players elsewhere cannot use them.
import { api } from '$lib/api/client';
import type { Bot } from '$lib/api/types';
import type { BlueprintSpec } from '$lib/api/games';

const state = $state<{ hosts: Record<string, string>; loaded: boolean }>({ hosts: {}, loaded: false });
let loading: Promise<void> | null = null;

/** Loads the nodes' public addresses once per page load (GET /game-hosts). */
export function loadGameHosts(): Promise<void> {
	loading ??= api<{ hosts: Record<string, string> }>('GET', '/game-hosts')
		.then((r) => {
			state.hosts = r.hosts ?? {};
		})
		.catch(() => {})
		.finally(() => (state.loaded = true));
	return loading;
}

/** A node's address for players ('' = not known). */
export function nodeHost(nodeID: string): string {
	return state.hosts[nodeID] ?? '';
}

export type Reach = 'public' | 'lan' | 'local';
export type ConnectInfo = {
	/** '' when no address is known yet (source 'none'). */
	host: string;
	port: number;
	/** Always host:port, for copying ('' without a host). */
	full: string;
	/** What to show: the port is left out when it is the game's default. */
	short: string;
	reach: Reach;
	/** Where the host came from, for the explanation under it. */
	source: 'alias' | 'ip' | 'node' | 'none';
	queryPort?: number;
};

const defaultPorts: Record<string, number> = { 'minecraft-java': 25565 };

export function reachOf(host: string): Reach {
	const h = host.toLowerCase().replace(/^\[|\]$/g, '');
	if (h === 'localhost' || h.endsWith('.localhost') || h === '::1' || /^127\./.test(h) || h === '0.0.0.0') return 'local';
	if (
		/^10\./.test(h) ||
		/^192\.168\./.test(h) ||
		/^172\.(1[6-9]|2\d|3[01])\./.test(h) ||
		/^100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\./.test(h) ||
		/^169\.254\./.test(h) ||
		/^f[cd][0-9a-f]{2}:/.test(h) ||
		/^fe80:/.test(h) ||
		/\.(local|lan|home|internal|test)$/.test(h) ||
		!h.includes('.') && !h.includes(':')
	)
		return 'lan';
	return 'public';
}

/** The address players use, or null when the server has no port yet. */
export function connectInfo(b: Bot, spec?: BlueprintSpec | null): ConnectInfo | null {
	const a = b.allocations?.find((x) => x.primary);
	if (!a) return null;
	void state.hosts; // re-evaluate when the addresses arrive
	let host = '';
	let source: ConnectInfo['source'] = 'none';
	if (a.alias) [host, source] = [a.alias, 'alias'];
	else if (a.ip && a.ip !== '0.0.0.0' && a.ip !== '::') [host, source] = [a.ip, 'ip'];
	else if (state.hosts[b.node_id]) [host, source] = [state.hosts[b.node_id], 'node'];
	if (!host) return { host: '', port: a.port, full: '', short: '', reach: 'public', source };
	const bracket = host.includes(':') && !host.startsWith('[') ? `[${host}]` : host;
	const full = `${bracket}:${a.port}`;
	const def = spec?.query ? defaultPorts[spec.query] : b.blueprint_id && a.port === 25565 ? 25565 : undefined;
	let queryPort: number | undefined;
	if (spec?.query_allocation) {
		const extra = (b.allocations ?? []).filter((x) => !x.primary).sort((x, y) => x.port - y.port);
		const q = extra[spec.query_allocation - 1];
		if (q && q.port !== a.port) queryPort = q.port;
	}
	return { host, port: a.port, full, short: def === a.port ? bracket : full, reach: reachOf(host), source, queryPort };
}

/** The join address as one string (the short form), or '' without a port. */
export function joinAddress(b: Bot): string {
	return connectInfo(b)?.short ?? '';
}

/** One line on where to paste the address, per game. */
export function joinHint(spec: { slug: string; category: string; query?: string } | null | undefined): string {
	if (!spec) return 'Connect from the game with this address.';
	const byslug: Record<string, string> = {
		'minecraft-velocity': 'Players join the proxy: in Minecraft, Multiplayer → Add Server, then paste the address.',
		'steam-valheim': 'In Valheim: Start game → Join game → Join IP, then enter the address and the server password.',
		'steam-rust': 'In Rust: press F1 and enter client.connect followed by the address.',
		'steam-project-zomboid': 'In Project Zomboid: Join → enter the IP and port as a favourite, then join.'
	};
	if (byslug[spec.slug]) return byslug[spec.slug];
	if (spec.query === 'minecraft-java' || spec.category.toLowerCase().includes('minecraft')) return 'In Minecraft: Multiplayer → Add Server, then paste the address.';
	if (spec.query === 'steam' || spec.category.toLowerCase().includes('steam')) return 'In Steam: View → Game Servers → Favorites → Add a server with this address.';
	return 'Connect from the game with this address.';
}
