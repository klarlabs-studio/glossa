// Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file.
import { createApp, h } from "vue";
import { createGlossa } from "@klarlabs-studio/glossa/vue";

import "./styles.css";
import { runtime } from "./runtime";
import SiteHeader from "./components/SiteHeader.vue";
import SiteFooter from "./components/SiteFooter.vue";
import HomePage from "./pages/HomePage.vue";
import ProductsPage from "./pages/ProductsPage.vue";
import ProductPage from "./pages/ProductPage.vue";
import CartPage from "./pages/CartPage.vue";
import CheckoutPage from "./pages/CheckoutPage.vue";
import AccountPage from "./pages/AccountPage.vue";
import HelpPage from "./pages/HelpPage.vue";
import InvoicePage from "./pages/InvoicePage.vue";

/** Route pattern → page, longest concrete path first. */
const routes = [
  { route: "/", match: (p: string) => p === "/", page: HomePage },
  { route: "/produkte", match: (p: string) => p === "/produkte", page: ProductsPage },
  { route: "/produkte/[id]", match: (p: string) => p.startsWith("/produkte/"), page: ProductPage },
  { route: "/warenkorb", match: (p: string) => p === "/warenkorb", page: CartPage },
  { route: "/kasse", match: (p: string) => p === "/kasse", page: CheckoutPage },
  { route: "/konto", match: (p: string) => p === "/konto", page: AccountPage },
  { route: "/hilfe", match: (p: string) => p === "/hilfe", page: HelpPage },
  { route: "/rechnung", match: (p: string) => p === "/rechnung", page: InvoicePage },
] as const;

function current() {
  const path = location.pathname.replace(/\/+$/, "") || "/";
  return routes.find((r) => r.match(path)) ?? routes[0];
}

const app = createApp({
  render: () => h("div", [h(SiteHeader), h("main", [h(current().page)]), h(SiteFooter)]),
});
app.use(createGlossa({ runtime }));
app.mount("#app");
