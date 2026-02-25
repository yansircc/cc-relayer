import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	kit: {
		adapter: adapter({
			pages: '../internal/ui/dist',
			assets: '../internal/ui/dist',
			fallback: 'index.html',
			precompress: true
		}),
		paths: {
			base: '/ui'
		}
	}
};

export default config;
