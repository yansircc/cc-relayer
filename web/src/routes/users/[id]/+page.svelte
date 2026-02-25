<script lang="ts">
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { api } from '$lib/api';
	import { timeAgo, fmtNum, fmtCost, fmtDate, tagClass, statusColor, shortModel } from '$lib/format';
	import ConfirmAction from '$lib/components/ConfirmAction.svelte';

	interface UsagePeriod {
		label: string;
		requests: number;
		input_tokens: number;
		output_tokens: number;
		cache_read_tokens: number;
		cost_usd: number;
	}

	interface ModelUsageRow {
		model: string;
		requests: number;
		input_tokens: number;
		output_tokens: number;
		cache_read_tokens: number;
		cost_usd: number;
	}

	interface RecentRequest {
		ID: number;
		UserID: string;
		AccountID: string;
		Model: string;
		InputTokens: number;
		OutputTokens: number;
		CacheReadTokens: number;
		CacheCreateTokens: number;
		CostUSD: number;
		Status: string;
		DurationMs: number;
		CreatedAt: string;
	}

	interface UserDetail {
		id: string;
		name: string;
		token_prefix: string;
		status: string;
		created_at: string;
		last_active_at: string | null;
		usage: UsagePeriod[];
		model_usage: ModelUsageRow[];
		recent_requests: RecentRequest[];
	}

	let user = $state<UserDetail | null>(null);
	let error = $state('');
	let loading = $state(true);
	let newToken = $state('');
	let actionError = $state('');

	$effect(() => {
		loadUser();
	});

	async function loadUser() {
		try {
			user = await api<UserDetail>('/users/' + $page.params.id);
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	async function regenerateToken() {
		if (!user) return;
		actionError = '';
		try {
			const result = await api<{ token: string }>(`/users/${user.id}/regenerate`, { method: 'POST' });
			newToken = result.token;
		} catch (e: any) {
			actionError = e.message;
		}
	}

	async function toggleStatus() {
		if (!user) return;
		const newStatus = user.status === 'active' ? 'disabled' : 'active';
		actionError = '';
		try {
			await api(`/users/${user.id}/status`, {
				method: 'POST',
				body: JSON.stringify({ status: newStatus })
			});
			user.status = newStatus;
		} catch (e: any) {
			actionError = e.message;
		}
	}

	async function deleteUser() {
		if (!user) return;
		actionError = '';
		try {
			await api(`/users/${user.id}`, { method: 'DELETE' });
			goto(`${base}/dashboard`);
		} catch (e: any) {
			actionError = e.message;
		}
	}
</script>

{#if error}
	<p class="error-msg">{error}</p>
	<p><a href="{base}/dashboard">&larr; back to dashboard</a></p>
{:else if loading}
	<p class="loading">loading user...</p>
{:else if user}
	{@const usage = user.usage ?? []}
	{@const models = user.model_usage ?? []}
	{@const requests = user.recent_requests ?? []}

	<h2>{user.name} <span class={tagClass(user.status)}>{user.status}</span></h2>

	<div class="actions">
		<ConfirmAction label="[regenerate token]" onclick={regenerateToken} />
		<button class="link {user.status === 'active' ? 'r' : 'g'}" onclick={toggleStatus}>
			[{user.status === 'active' ? 'disable' : 'enable'}]
		</button>
		<ConfirmAction label="[delete]" cls="r" onclick={deleteUser} />
	</div>

	{#if actionError}<p class="error-msg">{actionError}</p>{/if}

	{#if newToken}
		<div class="bar">
			token: <b>{newToken}</b>
			<br><br>
			<span class="r">Copy now -- this token will not be shown again.</span>
			<br>
			Configure Claude Code:
			<br>
			&nbsp;&nbsp;export ANTHROPIC_API_KEY={newToken}
		</div>
	{/if}

	<dl>
		<dt>token prefix</dt>
		<dd>{user.token_prefix} <span class="muted">(full token not stored)</span></dd>

		<dt>created</dt>
		<dd>{fmtDate(user.created_at)}</dd>

		<dt>last active</dt>
		<dd>{#if user.last_active_at}{timeAgo(user.last_active_at)} ({new Date(user.last_active_at).toLocaleTimeString('en-GB', { hour12: false })}){:else}<span class="muted">-</span>{/if}</dd>
	</dl>

	<!-- Usage periods -->
	<h2>usage</h2>
	{#if usage.length === 0}
		<p class="muted">no usage data</p>
	{:else}
		<table><thead>
			<tr>
				<th></th>
				<th class="num">requests</th>
				<th class="num">input</th>
				<th class="num">output</th>
				<th class="num">cache read</th>
				<th class="num">cost</th>
			</tr></thead><tbody>
			{#each usage as u, i (u.label)}
				<tr>
					<td>{u.label}</td>
					<td class="num">{fmtNum(u.requests)}</td>
					<td class="num">{fmtNum(u.input_tokens)}</td>
					<td class="num">{fmtNum(u.output_tokens)}</td>
					<td class="num">{fmtNum(u.cache_read_tokens)}</td>
					<td class="num">{#if i === usage.length - 1}<b>{fmtCost(u.cost_usd)}</b>{:else}{fmtCost(u.cost_usd)}{/if}</td>
				</tr>
			{/each}
		</tbody></table>
	{/if}

	<!-- Per-model breakdown -->
	<h2>by model (7 days)</h2>
	{#if models.length === 0}
		<p class="muted">no model data</p>
	{:else}
		<table><thead>
			<tr>
				<th>model</th>
				<th class="num">reqs</th>
				<th class="num">input</th>
				<th class="num">output</th>
				<th class="num">cache read</th>
				<th class="num">cost</th>
			</tr></thead><tbody>
			{#each models as m (m.model)}
				<tr>
					<td>{m.model}</td>
					<td class="num">{fmtNum(m.requests)}</td>
					<td class="num">{fmtNum(m.input_tokens)}</td>
					<td class="num">{fmtNum(m.output_tokens)}</td>
					<td class="num">{fmtNum(m.cache_read_tokens)}</td>
					<td class="num">{fmtCost(m.cost_usd)}</td>
				</tr>
			{/each}
		</tbody></table>
	{/if}

	<!-- Recent requests -->
	<h2>recent requests</h2>
	{#if requests.length === 0}
		<p class="muted">no recent requests</p>
	{:else}
		<table><thead>
			<tr>
				<th>time</th>
				<th>model</th>
				<th class="num">input</th>
				<th class="num">output</th>
				<th class="num">cache r/w</th>
				<th>account</th>
				<th>status</th>
				<th class="num">duration</th>
			</tr></thead><tbody>
			{#each requests as r (r.ID)}
				<tr>
					<td class="muted">{new Date(r.CreatedAt).toLocaleTimeString('en-GB', { hour12: false })}</td>
					<td>{shortModel(r.Model)}</td>
					<td class="num">{fmtNum(r.InputTokens)}</td>
					<td class="num">{fmtNum(r.OutputTokens)}</td>
					<td class="num">{fmtNum(r.CacheReadTokens)} / {fmtNum(r.CacheCreateTokens)}</td>
					<td>{r.AccountID}</td>
					<td class={statusColor(r.Status)}>{r.Status}</td>
					<td class="num">{r.DurationMs > 0 ? (r.DurationMs / 1000).toFixed(1) + 's' : '-'}</td>
				</tr>
			{/each}
		</tbody></table>
	{/if}

	<p style="margin-top:16px;font-size:12px"><a href="{base}/dashboard">&larr; back</a></p>
{/if}
