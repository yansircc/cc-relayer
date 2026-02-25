export function fmtNum(n: number): string {
	if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M';
	if (n >= 1_000) return (n / 1_000).toFixed(n >= 10_000 ? 0 : 1).replace(/\.0$/, '') + 'K';
	return n.toLocaleString();
}

export function fmtCost(n: number): string {
	if (n >= 100) return '$' + Math.round(n).toLocaleString();
	if (n >= 10) return '$' + n.toFixed(1);
	return '$' + n.toFixed(2);
}

export function fmtDate(s: string): string {
	if (!s) return '-';
	const d = new Date(s);
	return d.toLocaleDateString('en-CA') + ' ' + d.toLocaleTimeString('en-GB', { hour12: false });
}

export function timeAgo(s: string): string {
	if (!s) return '-';
	const diff = Date.now() - new Date(s).getTime();
	if (diff < 0) return 'just now';
	const mins = Math.floor(diff / 60000);
	if (mins < 1) return 'just now';
	if (mins < 60) return `${mins}m ago`;
	const hours = Math.floor(mins / 60);
	if (hours < 24) return `${hours}h ago`;
	const days = Math.floor(hours / 24);
	return `${days}d ago`;
}

export function tagClass(status: string): string {
	switch (status) {
		case 'active': return 'tag tag-active';
		case 'blocked': return 'tag tag-blocked';
		case 'error': return 'tag tag-error';
		case 'disabled': return 'tag tag-disabled';
		case 'overloaded': return 'tag tag-overloaded';
		default: return 'tag';
	}
}

export function statusColor(status: string): string {
	if (status === 'ok' || status === '200') return 'g';
	if (status.startsWith('4') || status === 'error') return 'r';
	if (status.startsWith('retry') || status.startsWith('5') || status === '529') return 'o';
	return '';
}

export function pctClass(pct: number): string {
	if (pct >= 50) return 'g';
	if (pct >= 20) return 'o';
	if (pct > 0) return 'r';
	return 'muted';
}

export function eventTypeColor(type: string): string {
	const t = type.toUpperCase();
	if (t === 'BAN' || t === '403' || t === '401') return 'r';
	if (t === '429' || t === 'RATELIMIT' || t === '5H_STOP' || t === '5H-STOP') return 'o';
	if (t === 'REFRESH' || t === 'RECOVER') return 'g';
	if (t === 'OVERLOAD') return 'o';
	return 'muted';
}

export function shortModel(model: string): string {
	return model.replace('claude-', '').replace(/-\d{8}$/, '');
}
