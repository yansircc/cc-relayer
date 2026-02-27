

export const index = 3;
let component_cache;
export const component = async () => component_cache ??= (await import('../entries/pages/accounts/_id_/_page.svelte.js')).default;
export const universal = {
  "ssr": false
};
export const universal_id = "src/routes/accounts/[id]/+page.ts";
export const imports = ["_app/immutable/nodes/3.y2vPwspI.js","_app/immutable/chunks/YPNk2NX5.js","_app/immutable/chunks/ABNvuIHC.js","_app/immutable/chunks/e87nZPu-.js","_app/immutable/chunks/CCxA75cu.js","_app/immutable/chunks/BlzAWoyq.js","_app/immutable/chunks/DeGR0Slq.js","_app/immutable/chunks/ct8flr_T.js","_app/immutable/chunks/2sA3hMLD.js","_app/immutable/chunks/BJOaqqeD.js","_app/immutable/chunks/BRuD9YBD.js","_app/immutable/chunks/C-a7_x20.js","_app/immutable/chunks/CYgJF_JY.js","_app/immutable/chunks/CR8Eiw6R.js","_app/immutable/chunks/DtH33QRI.js","_app/immutable/chunks/C2dPT-9p.js","_app/immutable/chunks/CXTilfkr.js"];
export const stylesheets = [];
export const fonts = [];
