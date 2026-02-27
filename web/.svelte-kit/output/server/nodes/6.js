

export const index = 6;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/login/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/login/+page.ts";
export const imports = ["_app/immutable/nodes/6.CMUEnAZo.js","_app/immutable/chunks/DSgr-hxR.js","_app/immutable/chunks/lUnH5NgY.js","_app/immutable/chunks/CMUPHsw8.js","_app/immutable/chunks/ChPJZGYw.js","_app/immutable/chunks/uqv2dTtj.js","_app/immutable/chunks/DUKUYMvf.js","_app/immutable/chunks/CId_XQ1p.js","_app/immutable/chunks/NZDo04p-.js"];
export const stylesheets = [];
export const fonts = [];
