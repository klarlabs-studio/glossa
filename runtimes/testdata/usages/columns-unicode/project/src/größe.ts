import { createRuntime } from "@klarlabs-studio/glossa";

const runtime = createRuntime({ edge: "https://edge.example.com", deliveryKey: "pk_fixture" });
const t = (id: string) => runtime.t(id, {});

// Columns count code points: "ö" is 2 UTF-8 bytes, "🥨" is 4 bytes and 2 UTF-16 units.
export const labels = { größe: "Größe", brezel: "🥨🥨" }; export const a = t("cart.size");
export const b = [labels.brezel, "🥨"].join("") + t("cart.pretzel");
	export const c = t("cart.tab");
export const d = t("cart.one"), e = t("cart.two");
