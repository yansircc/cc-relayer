

export const index = 5;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/dashboard/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/dashboard/+page.ts";
export const imports = ["_app/immutable/nodes/5.CBfsNW-j.js","_app/immutable/chunks/Cefu_kJh.js","_app/immutable/chunks/xTExy2GW.js","_app/immutable/chunks/C8R120ip.js","_app/immutable/chunks/Bbanrw_8.js","_app/immutable/chunks/CbSdhAf1.js","_app/immutable/chunks/NSlhNWv0.js","_app/immutable/chunks/Buwlz4xJ.js","_app/immutable/chunks/CKMoCsps.js","_app/immutable/chunks/BaD-Jc5Z.js","_app/immutable/chunks/DtH33QRI.js"];
export const stylesheets = ["_app/immutable/assets/5.C42YIsoK.css"];
export const fonts = [];
