/**
 * `<T id="cart.checkout">Zur Kasse</T>`: renders a message, with the children
 * as the inline default (runtimes/SPEC.md §3, step 5), then the message ID.
 * Safe MF2 markup becomes elements by the same rules as `@klarlabs-studio/glossa-elements`;
 * translation text is never rendered as HTML. No wrapper element: plain text
 * renders as a text node.
 *
 * Capture mode (RFC 0004 §3.1): while a capture or editor session has an
 * `onRender` hook installed, the content is wrapped in a
 * `<span style="display: contents">` host carrying `data-glossa-id` and
 * `data-glossa-locale`. Outside a session (and always on the server) the
 * output is unchanged.
 */
import { Fragment, createElement } from "react";
import type { ReactNode } from "react";
import { markAttributes, partsToTree, resolveParts } from "@klarlabs-studio/glossa-elements/parts";
import type { TreeNode } from "@klarlabs-studio/glossa-elements/parts";

import { useGlossa } from "./glossa.js";
import type { RegisteredMessages, View } from "./glossa.js";

export interface TProps<K extends keyof RegisteredMessages & string> {
  /** The message ID. */
  id: K;
  /** The values the message is formatted with. */
  values?: RegisteredMessages[K];
  /** The inline default, rendered when no locale of the active chain has the message. */
  children?: ReactNode;
}

// Children as arguments, not an array: React wants no keys for those.
const toNodes = (nodes: TreeNode[]): ReactNode[] =>
  nodes.map((n) =>
    typeof n === "string" ? n : createElement(n.tag, null, ...toNodes(n.children)),
  );

const CONTENTS = { display: "contents" };

/**
 * Every rendering of `<T>` leaves through here. While a capture or editor
 * session is active, the content gets a host element with `data-glossa-id`
 * and `data-glossa-locale`; outside a session it's the identity, so a normal
 * page view has no extra markup.
 */
const host = (g: View, id: string, content: ReactNode): ReactNode => {
  const mark = markAttributes(g, id);
  return mark ? createElement("span", { ...mark, style: CONTENTS }, content) : content;
};

export function T<K extends keyof RegisteredMessages & string>(props: TProps<K>): ReactNode {
  const { id, values, children } = props;
  const g = useGlossa() as unknown as View;
  const parts = resolveParts(g, id, values as Record<string, unknown> | undefined);
  if (!parts) return host(g, id, children ?? id);
  const tree = partsToTree(parts);
  return host(
    g,
    id,
    tree.every((n) => typeof n === "string")
      ? tree.join("")
      : createElement(Fragment, null, ...toNodes(tree)),
  );
}
