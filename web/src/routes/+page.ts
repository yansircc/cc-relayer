import { redirect } from '@sveltejs/kit';
import { base } from '$app/paths';

export const ssr = false;

export function load() {
	redirect(307, `${base}/dashboard`);
}
