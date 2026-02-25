<script lang="ts">
	import { api } from '$lib/api';

	let { accountId, priority, priorityMode, autoScore, onupdate }:
		{ accountId: string; priority: number; priorityMode: string; autoScore: number; onupdate: (mode: string, priority: number) => void } = $props();

	let isAuto = $derived(priorityMode === 'auto');
	let input = $state('');
	let saving = $state(false);
	let error = $state('');

	$effect(() => { input = String(priority); });

	async function save() {
		const val = parseInt(input);
		if (isNaN(val)) return;
		saving = true;
		error = '';
		try {
			await api(`/accounts/${accountId}/priority`, {
				method: 'POST',
				body: JSON.stringify({ mode: 'manual', priority: val })
			});
			onupdate('manual', val);
		} catch (e: any) {
			error = e.message;
		} finally {
			saving = false;
		}
	}

	async function switchToAuto() {
		error = '';
		try {
			await api(`/accounts/${accountId}/priority`, {
				method: 'POST',
				body: JSON.stringify({ mode: 'auto' })
			});
			onupdate('auto', priority);
		} catch (e: any) {
			error = e.message;
		}
	}

	async function switchToManual() {
		error = '';
		try {
			await api(`/accounts/${accountId}/priority`, {
				method: 'POST',
				body: JSON.stringify({ mode: 'manual', priority })
			});
			onupdate('manual', priority);
		} catch (e: any) {
			error = e.message;
		}
	}
</script>

{#if isAuto}
	<span class="g">auto</span> <span class="muted">(score: {autoScore})</span>
	<button class="link" style="font-size:12px;margin-left:6px" onclick={switchToManual}>[switch to manual]</button>
{:else}
	<input
		class="pri-edit"
		type="text"
		bind:value={input}
		onkeydown={(e) => { if (e.key === 'Enter') save(); }}
	>
	<button class="link" style="font-size:12px" onclick={save} disabled={saving}>
		{saving ? 'saving...' : '[save]'}
	</button>
	<button class="link" style="font-size:12px;margin-left:6px" onclick={switchToAuto}>[switch to auto]</button>
{/if}
{#if error}<span class="error-msg">{error}</span>{/if}

<style>
	.pri-edit {
		width: 40px;
		font: 13px monospace;
		border: 1px solid #ccc;
		padding: 0 4px;
	}
	@media (prefers-color-scheme: dark) {
		.pri-edit { background: #252525; color: #ccc; border-color: #555; }
	}
</style>
