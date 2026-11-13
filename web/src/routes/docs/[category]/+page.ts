import { error } from '@sveltejs/kit';
import { category } from '$lib/docs';
import type { PageLoad } from './$types';

export const load: PageLoad = ({ params }) => {
	const c = category(params.category);
	if (!c) error(404, 'There is no documentation section at this address.');
	return { category: c };
};
