

export const index = 3;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/accounts/_id_/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/accounts/[id]/+page.ts";
export const imports = ["_app/immutable/nodes/3.DksL_Wek.js","_app/immutable/chunks/DSgr-hxR.js","_app/immutable/chunks/lUnH5NgY.js","_app/immutable/chunks/CMUPHsw8.js","_app/immutable/chunks/ChPJZGYw.js","_app/immutable/chunks/uqv2dTtj.js","_app/immutable/chunks/CjJWkJbP.js","_app/immutable/chunks/DUKUYMvf.js","_app/immutable/chunks/CId_XQ1p.js","_app/immutable/chunks/BMgpKV-T.js","_app/immutable/chunks/CbltZmeM.js","_app/immutable/chunks/BZPCr9dl.js","_app/immutable/chunks/CYgJF_JY.js","_app/immutable/chunks/NZDo04p-.js","_app/immutable/chunks/DtH33QRI.js","_app/immutable/chunks/nuLSc5cV.js","_app/immutable/chunks/_IyZUv-Q.js"];
export const stylesheets = [];
export const fonts = [];
