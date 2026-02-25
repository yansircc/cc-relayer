

export const index = 7;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/users/_id_/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/users/[id]/+page.ts";
export const imports = ["_app/immutable/nodes/7.DwJ_AWfN.js","_app/immutable/chunks/Cefu_kJh.js","_app/immutable/chunks/xTExy2GW.js","_app/immutable/chunks/C8R120ip.js","_app/immutable/chunks/Bbanrw_8.js","_app/immutable/chunks/CbSdhAf1.js","_app/immutable/chunks/CYqJDZ5k.js","_app/immutable/chunks/Buwlz4xJ.js","_app/immutable/chunks/C__oTgmm.js","_app/immutable/chunks/CkYoN_EF.js","_app/immutable/chunks/gvcDxWpS.js","_app/immutable/chunks/CYgJF_JY.js","_app/immutable/chunks/RY6GoAMc.js","_app/immutable/chunks/DtH33QRI.js","_app/immutable/chunks/VB0zDvg6.js","_app/immutable/chunks/D_6p-gWw.js"];
export const stylesheets = [];
export const fonts = [];
