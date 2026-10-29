/**
 * `<GlossaText id="cart.checkout">Zur Kasse</GlossaText>`: renders a message,
 * with the default slot as the inline default (runtimes/SPEC.md §3, step 5),
 * then the message ID. Safe MF2 markup becomes elements by the same rules as
 * `@klarlabs-studio/glossa-elements`; translation text is never rendered as HTML. No wrapper
 * element: plain text renders as a text node.
 *
 * Capture mode (RFC 0004 §3.1): while a capture or editor session has an
 * `onRender` hook installed, the content is wrapped in a
 * `<span style="display: contents">` host carrying `data-glossa-id` and
 * `data-glossa-locale`. Outside a session (and always on the server) the
 * output is unchanged.
 */
import { defineComponent, h } from "vue";
import type { PropType, VNodeChild } from "vue";
import { markAttributes, partsToTree, resolveParts } from "@klarlabs-studio/glossa-elements/parts";
import type { TreeNode } from "@klarlabs-studio/glossa-elements/parts";

import { useState } from "./glossa.js";

const toVNodes = (nodes: TreeNode[]): VNodeChild[] =>
  nodes.map((n) => (typeof n === "string" ? n : h(n.tag, toVNodes(n.children))));

export const GlossaText = defineComponent({
  name: "GlossaText",
  props: {
    /** The message ID. */
    id: { type: String, required: true },
    /** The values the message is formatted with. */
    values: { type: Object as PropType<Record<string, unknown>>, default: undefined },
  },
  setup(props, { slots }) {
    const { runtime, version } = useState();
    const content = () => {
      const parts = resolveParts(runtime, props.id, props.values);
      if (!parts) return slots.default ? slots.default() : props.id;
      const tree = partsToTree(parts);
      return tree.every((n) => typeof n === "string") ? tree.join("") : toVNodes(tree);
    };
    return () => {
      void version.value;
      const mark = markAttributes(runtime, props.id);
      return mark ? h("span", { ...mark, style: "display: contents" }, content()) : content();
    };
  },
});
