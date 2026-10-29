import { useMemo } from "react";
import { T, useGlossa } from "@klarlabs-studio/glossa/react";

export function PaymentFooter({ total }: { total: number }) {
  const { t } = useGlossa();
  const label = useMemo(() => t("checkout.pay", { amount: total }), [t, total]);
  const hint = () => t("checkout.hint");

  return (
    <footer title={hint()}>
      <T id="checkout.terms" values={{ total }}>
        Es gelten die AGB.
      </T>
      <T id={"checkout.help"} />
      <button type="submit">{label}</button>
    </footer>
  );
}
