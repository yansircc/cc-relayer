<script lang="ts">
	import { base } from '$app/paths';
	import { api } from '$lib/api';
	import { fmtNum, fmtCost, timeAgo, tagClass, remainClass, remainTime, eventTypeColor } from '$lib/format';
	import CooldownCell from '$lib/components/CooldownCell.svelte';

	interface UsagePeriod {
		label: string;
		requests: number;
		input_tokens: number;
		output_tokens: number;
		cache_read_tokens: number;
		cost_usd: number;
	}

	interface AccountView {
		id: string;
		email: string;
		status: string;
		priority_mode: string;
		priority: number;
		overloaded_until: string | null;
		last_used_at: string | null;
		five_hour_util: number | null;
		seven_day_util: number | null;
		five_hour_reset: number | null;
		seven_day_reset: number | null;
	}

	interface UserView {
		id: string;
		name: string;
		status: string;
		last_active_at: string | null;
		total_cost: number;
	}

	interface EventInfo {
		type: string;
		account_id: string;
		message: string;
		ts: string;
	}

	interface DashboardData {
		health: {
			sqlite: string;
			uptime: string;
			version: string;
		};
		usage: UsagePeriod[];
		accounts: AccountView[];
		users: UserView[];
		events: EventInfo[];
	}

	let data = $state<DashboardData | null>(null);
	let error = $state('');
	let lastRefresh = $state('');

	let showAddUser = $state(false);
	let newUserName = $state('');
	let creatingUser = $state(false);
	let addUserError = $state('');
	let createdUser = $state<{ name: string; token: string } | null>(null);
	let copied = $state(false);

	$effect(() => {
		loadAll();
	});

	async function loadAll() {
		try {
			data = await api<DashboardData>('/dashboard');
			lastRefresh = new Date().toLocaleTimeString('en-GB', { hour12: false });
		} catch (e: any) {
			error = e.message;
		}
	}

	function formatEventTime(ts: string): string {
		try {
			return new Date(ts).toLocaleTimeString('en-GB', { hour12: false });
		} catch {
			return ts;
		}
	}

	function cancelAddUser() {
		showAddUser = false;
		newUserName = '';
		addUserError = '';
	}

	async function createUser() {
		const name = newUserName.trim();
		if (!name) { addUserError = 'username required'; return; }
		creatingUser = true;
		addUserError = '';
		try {
			const res = await api<{ id: string; name: string; token: string }>('/users', {
				method: 'POST',
				body: JSON.stringify({ name })
			});
			createdUser = { name: res.name, token: res.token };
			if (data) {
				data.users = [...data.users, { id: res.id, name: res.name, status: 'active', last_active_at: null, total_cost: 0 }];
			}
			showAddUser = false;
			newUserName = '';
		} catch (e: any) {
			addUserError = e.message;
		} finally {
			creatingUser = false;
		}
	}

	function buildTestCmd(token: string): string {
		const origin = typeof window !== 'undefined' ? window.location.origin : '';
		return `ANTHROPIC_BASE_URL="${origin}" \\
ANTHROPIC_AUTH_TOKEN="${token}" \\
claude -p --model haiku \\
  --system-prompt "You can only output bool" \\
  --tools "StructuredOutput" \\
  --setting-sources "" \\
  --strict-mcp-config \\
  --mcp-config '{"mcpServers":{}}' \\
  --disable-slash-commands \\
  --output-format stream-json \\
  --verbose \\
  --json-schema '{"type":"object","properties":{"result":{"type":"boolean"}},"required":["result"]}' \\
  "Print 'true' for testing purpose"`;
	}

	async function copyCmd() {
		if (!createdUser) return;
		await navigator.clipboard.writeText(buildTestCmd(createdUser.token));
		copied = true;
		setTimeout(() => { copied = false; }, 2000);
	}

</script>

{#if error}
	<p class="error-msg">{error}</p>
{:else if !data}
	<p class="loading">loading dashboard...</p>
{:else}
	<span class="refresh"><button class="link" onclick={loadAll}>[refresh]</button> <span class="muted">{lastRefresh}</span></span>
	<div class="sub">{data.health.version} &middot; up {data.health.uptime} &middot; sqlite <span class={data.health.sqlite === 'ok' ? 'g' : 'r'}>{data.health.sqlite}</span></div>

	<!-- Usage -->
	<h2>usage</h2>
	{#if data.usage.length === 0}
		<p class="muted">no usage data yet</p>
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
			{#each data.usage as u, i (u.label)}
				<tr>
					<td>{u.label}</td>
					<td class="num">{fmtNum(u.requests)}</td>
					<td class="num">{fmtNum(u.input_tokens)}</td>
					<td class="num">{fmtNum(u.output_tokens)}</td>
					<td class="num">{fmtNum(u.cache_read_tokens)}</td>
					<td class="num">{#if i === data.usage.length - 1}<b>{fmtCost(u.cost_usd)}</b>{:else}{fmtCost(u.cost_usd)}{/if}</td>
				</tr>
			{/each}
		</tbody></table>
	{/if}

	<!-- Accounts -->
	<h2>accounts <a href="{base}/add-account" class="add-link">[+ add]</a></h2>
	{#if data.accounts.length === 0}
		<p class="muted">no accounts</p>
	{:else}
		{@const ref = data.accounts.find(a => a.status === 'active' && a.five_hour_util != null)}
		<table><thead>
			<tr>
				<th>email</th>
				<th>status</th>
				<th>pri</th>
				<th>cooldown</th>
				<th>last used</th>
				<th class="num">{ref ? remainTime(ref.five_hour_reset, '5h') : '5h'}</th>
				<th class="num">{ref ? remainTime(ref.seven_day_reset, '7d') : '7d'}</th>
			</tr></thead><tbody>
			{#each data.accounts as a (a.id)}
				<tr>
					<td><a href="{base}/accounts/{a.id}">{a.email}</a></td>
					<td><span class={tagClass(a.status)}>{a.status}</span></td>
					<td class={a.priority_mode === 'auto' ? 'muted' : ''}>{a.priority_mode === 'auto' ? 'auto' : a.priority}</td>
					<CooldownCell until={a.overloaded_until} />
					<td>{timeAgo(a.last_used_at ?? '')}</td>
					<td class="num">{#if a.status === 'blocked' || a.status === 'disabled'}<span class="muted">&ndash;</span>{:else if a.five_hour_util != null}{@const remain = 100 - a.five_hour_util}<span class={remainClass(remain)}>{remain}%</span>{:else}<span class="muted">&ndash;</span>{/if}</td>
					<td class="num">{#if a.status === 'blocked' || a.status === 'disabled'}<span class="muted">&ndash;</span>{:else if a.seven_day_util != null}{@const remain = 100 - a.seven_day_util}<span class={remainClass(remain)}>{remain}%</span>{:else}<span class="muted">&ndash;</span>{/if}</td>
				</tr>
			{/each}
		</tbody></table>
	{/if}

	<!-- Users -->
	<h2>users
		{#if !showAddUser && !createdUser}
			<button class="link add-link" onclick={() => { showAddUser = true; }}>[+ add]</button>
		{/if}
	</h2>

	{#if showAddUser}
		<div class="bar">
			<input
				type="text"
				placeholder="username"
				bind:value={newUserName}
				onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') createUser(); if (e.key === 'Escape') cancelAddUser(); }}
				disabled={creatingUser}
				style="width:160px;max-width:160px;margin-right:6px;"
			/>
			<button class="link" onclick={createUser} disabled={creatingUser}>[create]</button>
			<button class="link" onclick={cancelAddUser} disabled={creatingUser}>[cancel]</button>
			{#if addUserError}<span class="r" style="margin-left:8px;">{addUserError}</span>{/if}
		</div>
	{/if}

	{#if createdUser}
		<div class="bar">
			<div><span class="g">&#10003; created</span> <b>{createdUser.name}</b> &mdash; token: <span style="user-select:all;">{createdUser.token}</span> <button class="link" onclick={() => { createdUser = null; copied = false; }}>[dismiss]</button></div>
			<div class="r" style="font-size:11px;margin-top:2px;">copy now — this token will not be shown again</div>
			<div class="test-cmd-wrap">
				<button class="copy-btn" onclick={copyCmd}>{copied ? '[copied]' : '[copy]'}</button>
				<pre class="test-cmd">{buildTestCmd(createdUser.token)}</pre>
			</div>
		</div>
	{/if}

	{#if data.users.length === 0}
		<p class="muted">no users</p>
	{:else}
		<table><thead>
			<tr>
				<th>name</th>
				<th>status</th>
				<th>last active</th>
				<th class="num">total cost</th>
			</tr></thead><tbody>
			{#each data.users as u (u.id)}
				<tr>
					<td><a href="{base}/users/{u.id}">{u.name}</a></td>
					<td><span class={tagClass(u.status)}>{u.status}</span></td>
					<td>{timeAgo(u.last_active_at ?? '')}</td>
					<td class="num {u.total_cost === 0 ? 'muted' : ''}">{fmtCost(u.total_cost)}</td>
				</tr>
			{/each}
		</tbody></table>
	{/if}

	<!-- Recent events -->
	<h2>recent events</h2>
	{#if data.events.length === 0}
		<p class="muted">no events yet</p>
	{:else}
		<ul class="event-list">
			{#each data.events as ev (ev.ts)}
				<li>
					<span class="ts">{formatEventTime(ev.ts)}</span>
					<span class={eventTypeColor(ev.type)}>{ev.type.toUpperCase()}</span>
					{ev.message}
				</li>
			{/each}
		</ul>
	{/if}
{/if}

<style>
	.test-cmd-wrap {
		position: relative;
		margin-top: 6px;
	}
	.test-cmd {
		background: #1a1a1a;
		color: #aaa;
		padding: 8px 10px;
		font: 12px/1.6 monospace;
		overflow-x: auto;
		white-space: pre-wrap;
		word-break: break-all;
		border-radius: 3px;
	}
	.copy-btn {
		position: absolute;
		top: 4px;
		right: 4px;
		background: none;
		border: none;
		color: #6b9bff;
		font: 11px monospace;
		cursor: pointer;
		padding: 2px 6px;
		margin: 0;
	}
	.copy-btn:hover {
		text-decoration: underline;
	}
</style>
