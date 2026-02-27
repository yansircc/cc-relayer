import { a as attr, e as escape_html, s as stringify } from "../../../chunks/root.js";
import { b as base } from "../../../chunks/server.js";
import "../../../chunks/url.js";
import "@sveltejs/kit/internal/server";
function _page($$renderer, $$props) {
  $$renderer.component(($$renderer2) => {
    let provider = "claude";
    let generating = false;
    $$renderer2.push(`<h2>add account</h2> <div class="bar" style="margin-bottom:12px"><label style="margin-right:12px"><input type="radio"${attr("checked", provider === "claude", true)} value="claude"${attr("disabled", false, true)}/> Claude</label> <label><input type="radio"${attr("checked", provider === "codex", true)} value="codex"${attr("disabled", false, true)}/> Codex</label></div> <h2>authorize `);
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
