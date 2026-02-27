<script lang="ts">
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { api } from '$lib/api';
	import { timeAgo, fmtDate, tagClass } from '$lib/format';
	import TokenCountdown from '$lib/components/TokenCountdown.svelte';
	import PriorityEditor from '$lib/components/PriorityEditor.svelte';
	import ConfirmAction from '$lib/components/ConfirmAction.svelte';

	interface AccountDetail {
		id: string;
		email: string;
		status: string;
		priority: number;
		priority_mode: string;
		auto_score: number;
		schedulable: boolean;
		errorMessage: string;
		extInfo: { email: string; orgUUID: string; orgName: string } | null;
		createdAt: string;
		lastUsedAt: string | null;
		lastRefreshAt: string | null;
		expiresAt: number;
		fiveHourStatus: string;
		overloadedUntil: string | null;
		opusRateLimitEndAt: string | null;
		stainless: Record<string, string> | null;
		sessions: { session_uuid: string; account_id: string; created_at: string; last_used_at: string; expires_at: string }[];
	}

	let acct = $state<AccountDetail | null>(null);
	let error = $state('');
	let loading = $state(true);
	let testResult = $state<{ ok: boolean; latency_ms: number; error?: string; time?: string } | null>(null);
	let testing = $state(false);
	let actionError = $state('');

	$effect(() => {
		loadAccount();
	});

	async function loadAccount() {
		try {
			acct = await api<AccountDetail>('/accounts/' + $page.params.id);
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	function countdownText(dateStr: string | null): string {
		if (!dateStr) return '-';
		const diff = new Date(dateStr).getTime() - Date.now();
		if (diff <= 0) return 'passed';
		const totalSecs = Math.floor(diff / 1000);
		const mins = Math.floor(totalSecs / 60);
		const secs = totalSecs % 60;
		if (mins < 60) return `${mins}m ${String(secs).padStart(2, '0')}s`;
		const hours = Math.floor(mins / 60);
		return `${hours}h ${mins % 60}m ${String(secs).padStart(2, '0')}s`;
	}

	function ttlFromExpires(expiresAt: string): string {
		if (!expiresAt) return '-';
		const diff = new Date(expiresAt).getTime() - Date.now();
		if (diff <= 0) return 'expired';
		const mins = Math.floor(diff / 60000);
		if (mins < 60) return `${mins}m`;
		const hours = Math.floor(mins / 60);
		return `${hours}h ${mins % 60}m`;
	}

	function fiveHourColor(status: string): string {
		if (status === 'allowed') return 'g';
		if (status === 'stopped' || status === 'warning') return 'o';
		return 'r';
	}

	async function testAccount() {
		if (!acct) return;
		testing = true;
		testResult = null;
		try {
			const data = await api<{ ok: boolean; latency_ms: number; error?: string }>(`/accounts/${acct.id}/test`, { method: 'POST' });
			testResult = { ...data, time: new Date().toLocaleTimeString('en-GB', { hour12: false }) };
		} catch (e: any) {
			testResult = { ok: false, latency_ms: 0, error: e.message, time: new Date().toLocaleTimeString('en-GB', { hour12: false }) };
		} finally {
			testing = false;
		}
	}

	async function forceRefresh() {
		if (!acct) return;
		actionError = '';
		try {
			await api(`/accounts/${acct.id}/refresh`, { method: 'POST' });
			await loadAccount();
		} catch (e: any) {
			actionError = e.message;
		}
	}

	async function toggleStatus() {
		if (!acct) return;
		const newStatus = acct.status === 'disabled' ? 'active' : 'disabled';
		actionError = '';
		try {
			await api(`/accounts/${acct.id}/status`, {
				method: 'POST',
				body: JSON.stringify({ status: newStatus })
			});
			acct.status = newStatus;
		} catch (e: any) {
			actionError = e.message;
		}
	}

	async function deleteAccount() {
		if (!acct) return;
		actionError = '';
		try {
			await api(`/accounts/${acct.id}`, { method: 'DELETE' });
			goto(`${base}/dashboard`);
		} catch (e: any) {
			actionError = e.message;
		}
	}

	async function unbindSession(uuid: string) {
		if (!acct) return;
		actionError = '';
		try {
			await api(`/sessions/binding/${uuid}`, { method: 'DELETE' });
			acct.sessions = acct.sessions.filter((s) => s.session_uuid !== uuid);
		} catch (e: any) {
			actionError = e.message;
		}
	}

	function handlePriorityUpdate(mode: string, priority: number) {
		if (!acct) return;
		acct.priority_mode = mode;
		acct.priority = priority;
	}

	let editingEmail = $state(false);
	let emailInput = $state('');
	let savingEmail = $state(false);
	let emailError = $state('');

	async function saveEmail() {
		if (!acct) return;
		const trimmed = emailInput.trim();
		if (!trimmed) return;
		savingEmail = true;
		emailError = '';
		try {
			const res = await api<{ email: string }>(`/accounts/${acct.id}/email`, {
				method: 'POST',
				body: JSON.stringify({ email: trimmed })
			});
			acct.email = res.email;
			editingEmail = false;
		} catch (e: any) {
			emailError = e.message;
		} finally {
			savingEmail = false;
		}
	}
</script>

{#if error}
	<p class="error-msg">{error}</p>
	<p><a href="{base}/dashboard">&larr; back to dashboard</a></p>
{:else if loading}
	<p class="loading">loading account...</p>
{:else if acct}
	<h2>{acct.email} <span class={tagClass(acct.status)}>{acct.status}</span></h2>

	<div class="actions">
		<button class="link" onclick={testAccount} disabled={testing}>{testing ? '[testing...]' : '[test]'}</button>
		<button class="link" onclick={forceRefresh}>[force refresh token]</button>
		<button class="link {acct.status === 'disabled' ? 'g' : 'r'}" onclick={toggleStatus}>
			[{acct.status === 'disabled' ? 'enable' : 'disable'}]
		</button>
		<ConfirmAction label="[delete]" cls="r" onclick={deleteAccount} />
	</div>

	{#if actionError}<p class="error-msg">{actionError}</p>{/if}

	{#if testResult}
		<div class="bar" style="margin-top:0">
			{#if testResult.ok}
				<span class="g">&#10003; ok</span> &mdash; haiku &mdash; {(testResult.latency_ms / 1000).toFixed(1)}s &mdash; <span class="muted">{testResult.time}</span>
			{:else}
				<span class="r">&#10007; failed</span> &mdash; {testResult.error} &mdash; <span class="muted">{testResult.time}</span>
			{/if}
		</div>
	{/if}

	<h2>info</h2>
	<dl>
		<dt>id</dt>
		<dd class="muted">{acct.id}</dd>

		<dt>email</dt>
		<dd>
			{#if editingEmail}
				<input type="text" bind:value={emailInput} onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') saveEmail(); if (e.key === 'Escape') { editingEmail = false; emailError = ''; } }} maxlength={100} style="width:240px;font:13px monospace;border:1px solid #ccc;padding:0 4px;">
				<button class="link" style="font-size:12px" onclick={saveEmail} disabled={savingEmail}>{savingEmail ? 'saving...' : '[save]'}</button>
				<button class="link" style="font-size:12px;margin-left:4px" onclick={() => { editingEmail = false; emailError = ''; }}>[cancel]</button>
			{:else}
				{acct.email}
				<button class="link" style="font-size:12px;margin-left:6px" onclick={() => { emailInput = acct!.email; editingEmail = true; emailError = ''; }}>[edit]</button>
			{/if}
			{#if emailError}<span class="error-msg">{emailError}</span>{/if}
		</dd>

		<dt>org</dt>
		<dd>
			{#if acct.extInfo}
				{acct.extInfo.orgName} <span class="muted">(uuid: {acct.extInfo.orgUUID})</span>
			{:else}
				<span class="muted">-</span>
			{/if}
		</dd>

		<dt>priority</dt>
		<dd>
			<PriorityEditor
				accountId={acct.id}
				priority={acct.priority}
				priorityMode={acct.priority_mode}
				autoScore={acct.auto_score}
				onupdate={handlePriorityUpdate}
			/>
		</dd>

		<dt>created</dt>
		<dd>{fmtDate(acct.createdAt)}</dd>
	</dl>

	<h2>token</h2>
	<dl>
		<dt>access token expires</dt>
		<dd><TokenCountdown expiresAt={acct.expiresAt} /></dd>

		<dt>last refreshed</dt>
		<dd>{#if acct.lastRefreshAt}{timeAgo(acct.lastRefreshAt)} ({new Date(acct.lastRefreshAt).toLocaleTimeString('en-GB', { hour12: false })}){:else}<span class="muted">-</span>{/if}</dd>

		<dt>refresh status</dt>
		<dd>
			{#if acct.expiresAt > Date.now()}
				<span class="g">ok</span>
			{:else}
				<span class="r">expired</span>
			{/if}
		</dd>
	</dl>

	<h2>scheduling</h2>
	<dl>
		<dt>schedulable</dt>
		<dd class={acct.schedulable ? 'g' : 'r'}>{acct.schedulable ? 'yes' : 'no'}</dd>

		<dt>overloaded until</dt>
		<dd>
			{#if acct.overloadedUntil}
				<span class="o">{countdownText(acct.overloadedUntil)}</span>
			{:else}
				<span class="muted">-</span>
			{/if}
		</dd>

		<dt>5h window</dt>
		<dd><span class={fiveHourColor(acct.fiveHourStatus)}>{acct.fiveHourStatus || '-'}</span></dd>

		<dt>opus rate limit</dt>
		<dd>
			{#if acct.opusRateLimitEndAt}
				<span class="o">{countdownText(acct.opusRateLimitEndAt)}</span>
			{:else}
				<span class="muted">-</span>
			{/if}
		</dd>

		<dt>last used</dt>
		<dd>{#if acct.lastUsedAt}{timeAgo(acct.lastUsedAt)} ({new Date(acct.lastUsedAt).toLocaleTimeString('en-GB', { hour12: false })}){:else}<span class="muted">-</span>{/if}</dd>

		<dt>error message</dt>
		<dd>
			{#if acct.errorMessage}
				<span class="r">{acct.errorMessage}</span>
			{:else}
				<span class="muted">-</span>
			{/if}
		</dd>
	</dl>

	<h2>stainless fingerprint</h2>
	{#if acct.stainless && Object.keys(acct.stainless).length > 0}
		<dl>
			{#each Object.entries(acct.stainless) as [key, value] (key)}
				<dt>{key}</dt>
				<dd>{value}</dd>
			{/each}
		</dl>
	{:else}
		<p class="muted">not captured yet</p>
	{/if}

	<h2>bound sessions <span class="muted">({acct.sessions.length})</span></h2>
	{#if acct.sessions.length === 0}
		<p class="muted">no active session bindings</p>
	{:else}
		<table><thead>
			<tr>
				<th>session uuid</th>
				<th>last active</th>
				<th>ttl</th>
				<th></th>
			</tr></thead><tbody>
			{#each acct.sessions as s (s.session_uuid)}
				<tr>
					<td class="muted">{s.session_uuid}</td>
					<td>{timeAgo(s.last_used_at)}</td>
					<td>{ttlFromExpires(s.expires_at)}</td>
					<td><ConfirmAction label="unbind" onclick={() => unbindSession(s.session_uuid)} /></td>
				</tr>
			{/each}
		</tbody></table>
	{/if}

	<p style="margin-top:16px;font-size:12px"><a href="{base}/dashboard">&larr; back</a></p>
{/if}
