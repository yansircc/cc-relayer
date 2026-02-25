<script lang="ts">
	import '$lib/global.css';
	import { base } from '$app/paths';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';

	interface Props {
		children: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	$effect(() => {
		if ($page.url.pathname.endsWith('/login')) return;
		fetch('/admin/health', { credentials: 'same-origin', cache: 'no-store' }).then((r) => {
			if (r.status === 401) goto(`${base}/login`, { replaceState: true });
		}).catch(() => {});
	});
</script>

<h1><a href="{base}/dashboard" style="text-decoration:none;color:inherit;">cc-relayer</a></h1>

{@render children()}
