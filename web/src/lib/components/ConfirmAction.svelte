<script lang="ts">
	let { label, cls = '', onclick }: { label: string; cls?: string; onclick: () => void } = $props();

	let confirming = $state(false);
	let timeout: ReturnType<typeof setTimeout> | null = null;

	function startConfirm() {
		confirming = true;
		timeout = setTimeout(() => { confirming = false; }, 3000);
	}

	function doConfirm() {
		confirming = false;
		if (timeout) clearTimeout(timeout);
		onclick();
	}

	function cancel() {
		confirming = false;
		if (timeout) clearTimeout(timeout);
	}
</script>

{#if confirming}
	<span style="font-size:12px">
		confirm? <button class="link" onclick={doConfirm}>yes</button> / <button class="link" onclick={cancel}>cancel</button>
	</span>
{:else}
	<button class="link {cls}" onclick={startConfirm}>{label}</button>
{/if}
