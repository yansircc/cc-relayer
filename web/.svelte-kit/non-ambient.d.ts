
// this file is generated — do not edit it


declare module "svelte/elements" {
	export interface HTMLAttributes<T> {
		'data-sveltekit-keepfocus'?: true | '' | 'off' | undefined | null;
		'data-sveltekit-noscroll'?: true | '' | 'off' | undefined | null;
		'data-sveltekit-preload-code'?:
			| true
			| ''
			| 'eager'
			| 'viewport'
			| 'hover'
			| 'tap'
			| 'off'
			| undefined
			| null;
		'data-sveltekit-preload-data'?: true | '' | 'hover' | 'tap' | 'off' | undefined | null;
		'data-sveltekit-reload'?: true | '' | 'off' | undefined | null;
		'data-sveltekit-replacestate'?: true | '' | 'off' | undefined | null;
	}
}

export {};


declare module "$app/types" {
	export interface AppTypes {
		RouteId(): "/" | "/accounts" | "/accounts/[id]" | "/add-account" | "/dashboard" | "/login" | "/users" | "/users/[id]";
		RouteParams(): {
			"/accounts/[id]": { id: string };
			"/users/[id]": { id: string }
		};
		LayoutParams(): {
			"/": { id?: string };
			"/accounts": { id?: string };
			"/accounts/[id]": { id: string };
			"/add-account": Record<string, never>;
			"/dashboard": Record<string, never>;
			"/login": Record<string, never>;
			"/users": { id?: string };
			"/users/[id]": { id: string }
		};
		Pathname(): "/" | `/accounts/${string}` & {} | "/add-account" | "/dashboard" | "/login" | `/users/${string}` & {};
		ResolvedPathname(): `${"" | `/${string}`}${ReturnType<AppTypes['Pathname']>}`;
		Asset(): string & {};
	}
}