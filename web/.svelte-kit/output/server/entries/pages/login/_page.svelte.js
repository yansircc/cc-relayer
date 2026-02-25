import { a as attr, e as escape_html } from "../../../chunks/root.js";
import "../../../chunks/url.js";
import "@sveltejs/kit/internal/server";
function _page($$renderer) {
  let token = "";
  $$renderer.push(`<h2>admin login</h2> <form><fieldset><legend>authenticate</legend> <label for="admin-token">admin token</label> <input id="admin-token" type="password"${attr("value", token)} placeholder="enter admin token..."/> `);
  {
    $$renderer.push("<!--[!-->");
  }
  $$renderer.push(`<!--]--> <br/> <button type="submit"${attr("disabled", !token.trim(), true)}>${escape_html("login")}</button></fieldset></form>`);
}
export {
  _page as default
};
