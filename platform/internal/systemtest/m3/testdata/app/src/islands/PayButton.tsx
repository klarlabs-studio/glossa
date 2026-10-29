// Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file.
import { T, useGlossa } from "@klarlabs-studio/glossa/react";

export function PayButton() {
  const { t } = useGlossa();
  return (
    <div className="island">
      <p className="line">{t("checkout.note.title")}</p>
      <p className="line"><T id="checkout.shipping.label" /></p>
      <p className="line">{t("checkout.bun.title")}</p>
      <p className="line">{t("checkout.invoice.title")}</p>
      <p className="line">{t("checkout.bread.activity", { name: "Lina" })}</p>
      <p className="line">{t("checkout.shipping.title")}</p>
    </div>
  );
}
