export const manifest = (() => {
function __memo(fn) {
	let value;
	return () => value ??= (value = fn());
}

return {
	appDir: "_app",
	appPath: "ui/_app",
	assets: new Set([]),
	mimeTypes: {},
	_: {
		client: {start:"_app/immutable/entry/start.wM3RaHZR.js",app:"_app/immutable/entry/app.hQBGBUX3.js",imports:["_app/immutable/entry/start.wM3RaHZR.js","_app/immutable/chunks/Ov88F790.js","_app/immutable/chunks/xTExy2GW.js","_app/immutable/chunks/CYgJF_JY.js","_app/immutable/chunks/BaD-Jc5Z.js","_app/immutable/entry/app.hQBGBUX3.js","_app/immutable/chunks/xTExy2GW.js","_app/immutable/chunks/C8R120ip.js","_app/immutable/chunks/Cefu_kJh.js","_app/immutable/chunks/Bbanrw_8.js","_app/immutable/chunks/CbSdhAf1.js","_app/immutable/chunks/D_6p-gWw.js","_app/immutable/chunks/C__oTgmm.js"],stylesheets:[],fonts:[],uses_env_dynamic_public:false},
		nodes: [
			__memo(() => import('./nodes/0.js')),
			__memo(() => import('./nodes/1.js')),
			__memo(() => import('./nodes/2.js')),
			__memo(() => import('./nodes/3.js')),
			__memo(() => import('./nodes/4.js')),
			__memo(() => import('./nodes/5.js')),
			__memo(() => import('./nodes/6.js')),
			__memo(() => import('./nodes/7.js'))
		],
		remotes: {
			
		},
		routes: [
			{
				id: "/",
				pattern: /^\/$/,
				params: [],
				page: { layouts: [0,], errors: [1,], leaf: 2 },
				endpoint: null
			},
			{
				id: "/accounts/[id]",
				pattern: /^\/accounts\/([^/]+?)\/?$/,
				params: [{"name":"id","optional":false,"rest":false,"chained":false}],
				page: { layouts: [0,], errors: [1,], leaf: 3 },
				endpoint: null
			},
			{
				id: "/add-account",
				pattern: /^\/add-account\/?$/,
				params: [],
				page: { layouts: [0,], errors: [1,], leaf: 4 },
				endpoint: null
			},
			{
				id: "/dashboard",
				pattern: /^\/dashboard\/?$/,
				params: [],
				page: { layouts: [0,], errors: [1,], leaf: 5 },
				endpoint: null
			},
			{
				id: "/login",
				pattern: /^\/login\/?$/,
				params: [],
				page: { layouts: [0,], errors: [1,], leaf: 6 },
				endpoint: null
			},
			{
				id: "/users/[id]",
				pattern: /^\/users\/([^/]+?)\/?$/,
				params: [{"name":"id","optional":false,"rest":false,"chained":false}],
				page: { layouts: [0,], errors: [1,], leaf: 7 },
				endpoint: null
			}
		],
		prerendered_routes: new Set([]),
		matchers: async () => {
			
			return {  };
		},
		server_assets: {}
	}
}
})();
