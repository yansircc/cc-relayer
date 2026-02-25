<script lang="ts">
	import { base } from '$app/paths';

	let token = $state('');
	let error = $state('');
	let loading = $state(false);

	async function handleLogin(e: Event) {
		e.preventDefault();
		if (!token.trim()) return;
		loading = true;
		error = '';
		try {
			const res = await fetch('/admin/login', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ token: token.trim() })
			});
			if (!res.ok) {
				const text = await res.text();
				error = text || `Login failed (${res.status})`;
				return;
			}
			window.location.href = `${base}/dashboard`;
		} catch (err) {
			error = 'Network error';
		} finally {
			loading = false;
		}
	}
</script>

<h2>admin login</h2>

<form onsubmit={handleLogin}>
	<fieldset>
		<legend>authenticate</legend>
		<label for="admin-token">admin token</label>
		<input id="admin-token" type="password" bind:value={token} placeholder="enter admin token...">
		{#if error}
			<p class="error-msg">{error}</p>
		{/if}
		<br>
		<button type="submit" disabled={loading || !token.trim()}>
			{loading ? 'logging in...' : 'login'}
		</button>
	</fieldset>
</form>
