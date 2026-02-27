

export const index = 4;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/add-account/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/add-account/+page.ts";
export const imports = ["_app/immutable/nodes/4.BHsYagWK.js","_app/immutable/chunks/YPNk2NX5.js","_app/immutable/chunks/ABNvuIHC.js","_app/immutable/chunks/e87nZPu-.js","_app/immutable/chunks/CCxA75cu.js","_app/immutable/chunks/BlzAWoyq.js","_app/immutable/chunks/ct8flr_T.js","_app/immutable/chunks/2sA3hMLD.js","_app/immutable/chunks/CR8Eiw6R.js","_app/immutable/chunks/DtH33QRI.js"];
export const stylesheets = [];
export const fonts = [];
