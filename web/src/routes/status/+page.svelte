<script lang="ts">
	import Countdown from '$lib/components/Countdown.svelte';
	import { dotClass, remainClass, remainTime } from '$lib/format';

	interface PublicLimit {
		name: string;
		remaining_pct: number;
		reset_at?: number;
	}

	interface PublicAccount {
		status: string;
		cooldown_until?: number;
		limits: PublicLimit[];
	}

	type PublicStatus = Record<string, PublicAccount[]>;

	let status = $state<PublicStatus | null>(null);
	let error = $state('');
	let loading = $state(false);
	let lastRefresh = $state('');
	let groups = $derived.by(() =>
		Object.entries(status ?? {}).sort(([left], [right]) => left.localeCompare(right))
	);

	$effect(() => {
		loadStatus();
	});

	async function loadStatus() {
		loading = true;
		error = '';
		status = null;
		try {
			const response = await fetch('/api/status', {
				cache: 'no-store',
				credentials: 'omit'
			});
			if (!response.ok) {
				throw new Error(`status request failed (${response.status})`);
			}
			status = await response.json();
			lastRefresh = new Date().toLocaleTimeString('en-GB', { hour12: false });
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}
</script>

<h1>broker status</h1>

<span class="refresh">
	<button class="link" onclick={loadStatus} disabled={loading}>[refresh]</button>
	{#if lastRefresh}<span class="muted"> {lastRefresh}</span>{/if}
</span>

{#if loading}
	<p class="loading">loading...</p>
{:else if error}
	<p class="error-msg">{error}</p>
{:else if groups.length === 0}
	<p class="muted">no accounts</p>
{:else}
	{#each groups as [provider, accounts] (provider)}
		<h2>{provider} accounts</h2>
		<div class="sub">{accounts.length} accounts</div>
		<div class="table-wrap">
			<table>
				<thead>
					<tr>
						<th>account</th>
						<th>status</th>
						<th>cooldown</th>
						<th>remaining</th>
					</tr>
				</thead>
				<tbody>
					{#each accounts as account}
						<tr>
							<td class="account-number"></td>
							<td><span class={dotClass(account.status)}>{account.status}</span></td>
							<Countdown until={account.cooldown_until ? account.cooldown_until * 1000 : null} tag="td" variant="cooldown" />
							<td>
								{#if account.limits.length === 0}
									<span class="muted">&ndash;</span>
								{:else}
									{#each account.limits as limit, index (`${limit.name}:${index}`)}
										<span class="limit">
											{limit.name}
											<span class={remainClass(limit.remaining_pct)}>{limit.remaining_pct}%</span>
											{#if limit.reset_at}<span class="muted">{remainTime(limit.reset_at)}</span>{/if}
										</span>
									{/each}
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/each}
{/if}

<style>
	tbody {
		counter-reset: account;
	}

	tbody tr {
		counter-increment: account;
	}

	.account-number::before {
		content: '账号 ' counter(account);
	}

	.table-wrap {
		overflow-x: auto;
	}

	.limit {
		display: inline-flex;
		gap: 0.5ch;
		margin-right: 14px;
	}
</style>
