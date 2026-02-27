<script lang="ts">
	import { onDestroy } from 'svelte';

	let { expiresAt }: { expiresAt: number } = $props();

	let now = $state(Date.now());
	const timer = setInterval(() => { now = Date.now(); }, 1000);
	onDestroy(() => clearInterval(timer));

	let diff = $derived(expiresAt - now);
	let expired = $derived(diff <= 0);
	let text = $derived.by(() => {
		if (expired) return 'expired';
		const totalSecs = Math.floor(diff / 1000);
		const hours = Math.floor(totalSecs / 3600);
		const mins = Math.floor((totalSecs % 3600) / 60);
		const secs = totalSecs % 60;
		return `${hours}h ${String(mins).padStart(2, '0')}m ${String(secs).padStart(2, '0')}s`;
	});
	let cls = $derived.by(() => {
		if (expired) return 'r';
		const mins = Math.floor(diff / 60000);
		if (mins < 10) return 'r';
		if (mins < 60) return 'o';
		return 'g';
	});
</script>

<span class={cls}>{text}</span>
