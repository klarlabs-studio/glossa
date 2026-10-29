import { createRuntime } from "@klarlabs-studio/glossa";

const runtime = createRuntime({ edge: "https://edge.example.com", deliveryKey: "pk_fixture" });
const t = (id: string, values: Record<string, unknown> = {}) => runtime.t(id, values);

// Dynamic keys are never reported: nothing static names the message.
export function dynamic(section: string, key: string): string[] {
  const id = "nav.home";
  return [
    t(key),
    t(id),
    t("nav." + section),
    t(`nav.${section}`),
    t(section === "a" ? "nav.a" : "nav.b"),
    runtime.t(key, {}),
    // A literal that isn't a valid message key is not a usage either.
    t("Hello world"),
    t("Nav.Home"),
    t(),
    // Other functions that merely end in t.
    at("fake.at"),
    tt("fake.tt"),
    format.t2("fake.t2"),
    // Comments and strings that look like calls:
    // t("fake.line-comment")
    /* $t('fake.block-comment') <GlossaText id="fake.block-component" /> */
    'call t("fake.single-quoted")',
    "<glossa-text key=\"fake.in-string\"></glossa-text>",
    `t("fake.template-literal")`,
    // A template literal without substitutions is a literal.
    t(`nav.static`),
  ];
}

declare function at(id: string): string;
declare function tt(id: string): string;
declare const format: { t2(id: string): string };
