<script lang="ts">
	import { base } from '$app/paths';
	import { api } from '$lib/api';

	let generating = $state(false);
	let exchanging = $state(false);
	let sessionId = $state('');
	let authUrl = $state('');
	let callbackInput = $state('');
	let genError = $state('');
	let exchangeError = $state('');
	let result = $state<{ id: string; email: string; status: string } | null>(null);

	async function generateAuthUrl() {
		generating = true;
		genError = '';
		try {
			const data = await api<{ session_id: string; auth_url: string }>('/accounts/generate-auth-url', { method: 'POST' });
			sessionId = data.session_id;
			authUrl = data.auth_url;
		} catch (e: any) {
			genError = e.message;
		} finally {
			generating = false;
		}
	}

	async function exchangeCode() {
		if (!callbackInput.trim()) return;
		exchanging = true;
		exchangeError = '';
		try {
			const data = await api<{ id: string; email: string; status: string }>('/accounts/exchange-code', {
				method: 'POST',
				body: JSON.stringify({
					session_id: sessionId,
					callback_url: callbackInput.trim()
				})
			});
			result = data;
		} catch (e: any) {
			exchangeError = e.message;
		} finally {
			exchanging = false;
		}
	}
</script>

<h2>add account</h2>

<fieldset>
	<legend>oauth flow</legend>

	<div style="margin:8px 0;padding:6px 0">
		<span style="font-weight:bold;margin-right:4px">1.</span> Generate authorization URL
		<br>
		<button onclick={generateAuthUrl} disabled={generating}>
			{generating ? 'generating...' : 'generate auth url'}
		</button>
		{#if genError}
			<p class="error-msg">{genError}</p>
		{/if}
		{#if sessionId}
			<div class="bar" style="margin-top:8px">
				session_id: <b>{sessionId}</b> <span class="muted">(expires in 10m)</span>
				<br><br>
				auth url:<br>
				<a href={authUrl} target="_blank" class="auth-url">{authUrl}</a>
				<br><br>
				<span class="muted">Open this URL in your browser, login and authorize.</span>
			</div>
		{/if}
	</div>

	<div style="margin:8px 0;padding:6px 0">
		<span style="font-weight:bold;margin-right:4px">2.</span> Paste the callback URL or code
		<label for="callback-input">callback url or code</label>
		<input id="callback-input" type="text" bind:value={callbackInput} placeholder="https://platform.claude.com/oauth/code/callback?code=...">
		<div class="hint">email and org info are auto-fetched after token exchange</div>
		<button onclick={exchangeCode} disabled={exchanging || !callbackInput.trim() || !sessionId}>
			{exchanging ? 'exchanging...' : 'exchange code'}
		</button>
		{#if exchangeError}
			<p class="error-msg">{exchangeError}</p>
		{/if}
		{#if result}
			<div class="bar" style="margin-top:8px">
				<span class="g">&#10003; account created</span>
				<br><br>
				email: <b>{result.email}</b><br>
				status: <b class="g">{result.status}</b><br>
				<br>
				<a href="{base}/accounts/{result.id}">view account &rarr;</a>
			</div>
		{/if}
	</div>
</fieldset>

<p style="margin-top:16px;font-size:12px"><a href="{base}/dashboard">&larr; back</a></p>
