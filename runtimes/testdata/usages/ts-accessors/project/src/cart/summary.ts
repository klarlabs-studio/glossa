import { createRuntime } from "@klarlabs-studio/glossa";
import { createMessages } from "../generated/messages";

const runtime = createRuntime({ edge: "https://edge.example.com", deliveryKey: "pk_fixture" });
const messages = createMessages((id, values) => runtime.t(id, values));
const m = createMessages((id, values) => runtime.t(id, values));

export function summary(total: number, heading: { title(): string }): string[] {
  return [
    messages.cart.checkout(),
    m.checkout.pay({ amount: total }),
    m.checkout.paymentFailed({ reason: "declined" }),
    messages.nav.home.title(),
    messages.title(),
    heading.title(),
    m.title(),
  ];
}
