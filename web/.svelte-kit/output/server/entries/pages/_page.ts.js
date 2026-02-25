import { redirect } from "@sveltejs/kit";
import { b as base } from "../../chunks/server.js";
import "../../chunks/url.js";
import "@sveltejs/kit/internal/server";
import "../../chunks/root.js";
const ssr = false;
function load() {
  redirect(307, `${base}/dashboard`);
}
export {
  load,
  ssr
};
