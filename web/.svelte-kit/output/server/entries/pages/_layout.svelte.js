import { a as attr, s as stringify } from "../../chunks/root.js";
import { b as base } from "../../chunks/server.js";
import "../../chunks/url.js";
import "@sveltejs/kit/internal/server";
import "@sveltejs/kit/internal";
import "../../chunks/utils.js";
import "../../chunks/exports.js";
import "../../chunks/state.svelte.js";
function _layout($$renderer, $$props) {
  $$renderer.component(($$renderer2) => {
    let { children } = $$props;
    $$renderer2.push(`<h1><a${attr("href", `${stringify(base)}/dashboard`)} style="text-decoration:none;color:inherit;">cc-relayer</a></h1> `);
    children($$renderer2);
    $$renderer2.push(`<!---->`);
  });
}
export {
  _layout as default
};
