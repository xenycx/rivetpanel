// Game icons for server types, by blueprint slug. The images come from
// Dashboard Icons (Apache-2.0) and selfh.st/icons (CC BY 4.0); see
// src/lib/assets/games/NOTICE. Types that have no icon of their own use their
// family's icon (Minecraft or Steam), and anything else falls back to a drawn
// gamepad.
import minecraft from '$lib/assets/games/minecraft.webp';
import fabric from '$lib/assets/games/fabricmc.webp';
import valheim from '$lib/assets/games/valheim.webp';
import steam from '$lib/assets/games/steam.svg';
import paper from '$lib/assets/games/papermc-paper.svg';
import folia from '$lib/assets/games/papermc-folia.svg';
import velocity from '$lib/assets/games/papermc-velocity.svg';
import purpur from '$lib/assets/games/purpurmc.svg';

const bySlug: Record<string, string> = {
	'minecraft-vanilla': minecraft,
	'minecraft-paper': paper,
	'minecraft-purpur': purpur,
	'minecraft-folia': folia,
	'minecraft-forge': minecraft,
	'minecraft-neoforge': minecraft,
	'minecraft-velocity': velocity,
	'minecraft-fabric': fabric,
	'steam-valheim': valheim,
	'steam-rust': steam,
	'steam-project-zomboid': steam
};

/** The icon for a server type, or '' when the generic fallback should be drawn. */
export function gameIcon(t: { slug?: string; category?: string } | null | undefined): string {
	if (!t) return '';
	if (t.slug && bySlug[t.slug]) return bySlug[t.slug];
	const c = (t.category ?? '').toLowerCase();
	const s = (t.slug ?? '').toLowerCase();
	if (c.includes('minecraft') || s.startsWith('minecraft')) return minecraft;
	if (c.includes('steam') || s.startsWith('steam')) return steam;
	return '';
}

/** A short plain-language line about what a server type is for. */
export function gameBlurb(slug: string): string {
	return (
		{
			'minecraft-paper': 'Fast, with plugins. The usual choice.',
			'minecraft-vanilla': 'The official server, no plugins or mods.',
			'minecraft-purpur': 'Paper plus extra gameplay options.',
			'minecraft-fabric': 'Lightweight mods; popular for new versions.',
			'minecraft-forge': 'The classic mod loader for big modpacks.',
			'minecraft-neoforge': 'Modern Forge fork for recent modpacks.',
			'minecraft-folia': 'Paper for very large, multi-threaded worlds.',
			'minecraft-velocity': 'A proxy that links several servers together.',
			'steam-valheim': 'Viking survival for up to 10 players.',
			'steam-rust': 'Survival with procedurally generated maps.',
			'steam-project-zomboid': 'Zombie survival, co-op or PvP.'
		}[slug] ?? ''
	);
}
