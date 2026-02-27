

export const index = 6;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/login/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/login/+page.ts";
export const imports = ["_app/immutable/nodes/6._o1sVNhe.js","_app/immutable/chunks/YPNk2NX5.js","_app/immutable/chunks/ABNvuIHC.js","_app/immutable/chunks/e87nZPu-.js","_app/immutable/chunks/CCxA75cu.js","_app/immutable/chunks/BlzAWoyq.js","_app/immutable/chunks/ct8flr_T.js","_app/immutable/chunks/2sA3hMLD.js","_app/immutable/chunks/CR8Eiw6R.js"];
export const stylesheets = [];
export const fonts = [];
