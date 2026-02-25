

export const index = 4;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/add-account/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/add-account/+page.ts";
export const imports = ["_app/immutable/nodes/4.BbdLddkS.js","_app/immutable/chunks/Cefu_kJh.js","_app/immutable/chunks/xTExy2GW.js","_app/immutable/chunks/C8R120ip.js","_app/immutable/chunks/Bbanrw_8.js","_app/immutable/chunks/CbSdhAf1.js","_app/immutable/chunks/Buwlz4xJ.js","_app/immutable/chunks/CKMoCsps.js","_app/immutable/chunks/RY6GoAMc.js","_app/immutable/chunks/DtH33QRI.js"];
export const stylesheets = [];
export const fonts = [];
