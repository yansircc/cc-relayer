

export const index = 7;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/users/_id_/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/users/[id]/+page.ts";
export const imports = ["_app/immutable/nodes/7.BM9MRvON.js","_app/immutable/chunks/DSgr-hxR.js","_app/immutable/chunks/lUnH5NgY.js","_app/immutable/chunks/CMUPHsw8.js","_app/immutable/chunks/ChPJZGYw.js","_app/immutable/chunks/uqv2dTtj.js","_app/immutable/chunks/CBDcwBok.js","_app/immutable/chunks/DUKUYMvf.js","_app/immutable/chunks/BMgpKV-T.js","_app/immutable/chunks/Bm87gwzg.js","_app/immutable/chunks/B1v15mmW.js","_app/immutable/chunks/CYgJF_JY.js","_app/immutable/chunks/BHL02ULs.js","_app/immutable/chunks/DtH33QRI.js","_app/immutable/chunks/zDGanlY5.js","_app/immutable/chunks/_IyZUv-Q.js"];
export const stylesheets = [];
export const fonts = [];
