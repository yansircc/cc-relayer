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
		const secs = Math.floor(diff / 1000);
		const mins = Math.floor(secs / 60);
		const remSecs = secs % 60;
		return `${mins}m ${String(remSecs).padStart(2, '0')}s remaining`;
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
