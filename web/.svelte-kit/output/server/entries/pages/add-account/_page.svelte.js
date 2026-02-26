import { a as attr, e as escape_html, s as stringify } from "../../../chunks/root.js";
import { b as base } from "../../../chunks/server.js";
import "../../../chunks/url.js";
import "@sveltejs/kit/internal/server";
function _page($$renderer, $$props) {
  $$renderer.component(($$renderer2) => {
    let generating = false;
    $$renderer2.push(`<h2>add account</h2> <h2>authorize `);
    {
      $$renderer2.push("<!--[!-->");
    }
    $$renderer2.push(`<!--]--></h2> `);
    {
      $$renderer2.push("<!--[-->");
      $$renderer2.push(`<p class="hint">generate an OAuth URL, open it in browser, login and authorize.</p> <button class="link"${attr("disabled", generating, true)}>${escape_html("[generate auth url]")}</button> `);
      {
        $$renderer2.push("<!--[!-->");
      }
      $$renderer2.push(`<!--]-->`);
    }
    $$renderer2.push(`<!--]--> `);
    {
      $$renderer2.push("<!--[!-->");
    }
    $$renderer2.push(`<!--]--> <p style="margin-top:16px;font-size:12px"><a${attr("href", `${stringify(base)}/dashboard`)}>← back</a></p>`);
  });
}
export {
  _page as default
};
