<script lang="ts">
	import '$lib/global.css';
	import { base } from '$app/paths';
	import { page } from '$app/stores';

	interface Props {
		children: import('svelte').Snippet;
	}

	const navItems = [
		{ href: '/dashboard', label: 'ops' },
		{ href: '/accounts', label: 'accounts' },
		{ href: '/users', label: 'users' },
		{ href: '/activity', label: 'activity' },
		{ href: '/migrations', label: 'migration' }
	];

	let { children }: Props = $props();

	function activeNav(href: string) {
		const path = $page.url.pathname;
		const full = `${base}${href}`;
		return path === full || path.startsWith(full + '/');
	}

	function standalonePage() {
		const path = $page.url.pathname;
		return path === `${base}/login` || path === `${base}/status`;
	}
</script>

{#if !standalonePage()}
	<h1><a href="{base}/dashboard" style="text-decoration:none;color:inherit;">broker</a></h1>
	<div class="topnav">
		{#each navItems as item (item.href)}
			{#if activeNav(item.href)}
				<span class="active">[{item.label}]</span>
			{:else}
				<a href="{base}{item.href}">[{item.label}]</a>
			{/if}
		{/each}
	</div>
{/if}

{@render children()}
