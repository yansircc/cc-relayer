<script lang="ts">
	import { onDestroy } from 'svelte';

	let { until }: { until: string | null } = $props();

	let now = $state(Date.now());
	let timer: ReturnType<typeof setInterval> | null = null;

	let diff = $derived(until ? new Date(until).getTime() - now : 0);
	let active = $derived(diff > 0);
	let text = $derived.by(() => {
		if (!active) return '\u2013';
		const mins = Math.floor(diff / 60000);
		const secs = Math.floor((diff % 60000) / 1000);
		return `${mins}m ${String(secs).padStart(2, '0')}s`;
	});

	$effect(() => {
		if (active && !timer) {
			timer = setInterval(() => { now = Date.now(); }, 1000);
		} else if (!active && timer) {
			clearInterval(timer);
			timer = null;
		}
	});

	onDestroy(() => { if (timer) clearInterval(timer); });
</script>

<td class={active ? 'o' : 'muted'}>{text}</td>
