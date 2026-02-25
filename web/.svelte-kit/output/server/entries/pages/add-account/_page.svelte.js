import { a as attr, e as escape_html, s as stringify } from "../../../chunks/root.js";
import { b as base } from "../../../chunks/server.js";
import "../../../chunks/url.js";
import "@sveltejs/kit/internal/server";
function _page($$renderer, $$props) {
  $$renderer.component(($$renderer2) => {
    let generating = false;
    let callbackInput = "";
    $$renderer2.push(`<h2>add account</h2> <fieldset><legend>oauth flow</legend> <div style="margin:8px 0;padding:6px 0"><span style="font-weight:bold;margin-right:4px">1.</span> Generate authorization URL <br/> <button${attr("disabled", generating, true)}>${escape_html("generate auth url")}</button> `);
    {
      $$renderer2.push("<!--[!-->");
    }
    $$renderer2.push(`<!--]--> `);
    {
      $$renderer2.push("<!--[!-->");
    }
    $$renderer2.push(`<!--]--></div> <div style="margin:8px 0;padding:6px 0"><span style="font-weight:bold;margin-right:4px">2.</span> Paste the callback URL or code <label for="callback-input">callback url or code</label> <input id="callback-input" type="text"${attr("value", callbackInput)} placeholder="https://platform.claude.com/oauth/code/callback?code=..."/> <div class="hint">email and org info are auto-fetched after token exchange</div> <button${attr("disabled", !callbackInput.trim() || true, true)}>${escape_html("exchange code")}</button> `);
    {
      $$renderer2.push("<!--[!-->");
    }
    $$renderer2.push(`<!--]--> `);
    {
      $$renderer2.push("<!--[!-->");
    }
    $$renderer2.push(`<!--]--></div></fieldset> <p style="margin-top:16px;font-size:12px"><a${attr("href", `${stringify(base)}/dashboard`)}>← back</a></p>`);
  });
}
export {
  _page as default
};
